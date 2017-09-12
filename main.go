package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	log "github.com/sirupsen/logrus"
)

var (
	name = "lxd-snapshot-server"

	client = InitClient()

	urlMaxLength = int64(240)

	persistedOperations = make(map[string]*lxd.RemoteOperation)
)

// Exit codes
const (
	ErrOk = iota
	ErrLog
	ErrConn
	ErrRandomIo
	ErrCommandFailed
	ErrUnableToCreateRequest
	ErrUnableToSubmitRequest
	ErrSubmitFailed
	ErrInvalidArguments
	ErrConfig
	ErrConnectionFailed
	ErrServer
)

type Request struct {
	reqID string
	w     http.ResponseWriter
	r     *http.Request
	log   *log.Entry
}

func (r Request) Error(err error, message string) {
	r.log.WithError(err).Error(message)
}

func main() {

	log.SetLevel(log.DebugLevel)
	// cts := client.GetContainers()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", handleRequest)

	log.Debugf("starting server on port :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))

}

func handleRequest(hw http.ResponseWriter, hr *http.Request) {
	reqID := genID()

	h := Request{
		reqID: reqID,
		w:     hw,
		r:     hr,
		log:   log.WithFields(log.Fields{"request_id": reqID}),
	}

	h.w.Header().Set("Request-ID", reqID)

	switch h.r.Method {
	case http.MethodPost:
		routePost(h)
		return

	default:
		h.w.WriteHeader(http.StatusNotFound)
		return
	}
}

func getBodyFromReq(w http.ResponseWriter, r io.ReadCloser) (bytes.Buffer, error) {
	rawBody := new(bytes.Buffer)
	b, err := rawBody.ReadFrom(r)

	if err != nil {
		errStr := fmt.Sprintf("[%x] unable to read body (%d bytes); aborting",
			w.Header().Get("Request-ID"),
			b)
		err = errors.New(errStr)
		log.Warning(err)
		w.WriteHeader(http.StatusBadRequest)
		return *rawBody, err
	}

	return *rawBody, err
}

// Routing for POST requests
func routePost(h Request) {
	h.log.WithFields(log.Fields{
		"url": h.r.URL.Path,
	}).Debug()

	switch h.r.URL.Path {
	case "/snapshot":
		// Take a snapshot. If `Command` is given, the command is executed inside
		// the copy and the result is passed back.
		body, err := getBodyFromReq(h.w, h.r.Body)

		if err != nil {
			return
		}

		s := BackupCommand{}
		json.Unmarshal(body.Bytes(), &s)
		h.log.WithFields(log.Fields{
			"body": s,
		}).Debug()

		s.Handle(h)
		return

	default:
		h.w.WriteHeader(http.StatusNotFound)
		return
	}
}

// BackupCommand Request body when requesting a snapshot of a container.
type BackupCommand struct {
	// Name of the LXC we want to operate on
	Name string
	// Name of copied LXC
	destName string
	// Whether to remove the copy on shutdown; should probably always be true
	Ephemeral bool
	// Profiles to apply to the copy, e.g. a profile with no ethernet devices
	Profiles []string
	// Command to run in LXC copy; should return a file path of the resulting
	// backup
	Command []string
	// Where to copy the result to in the original LXC
	Destination string

	copyOp *lxd.RemoteOperation
}

func (s BackupCommand) Handle(h Request) {
	cts := client.GetContainers()
	ct, ok := cts[s.Name]

	if !ok {
		h.w.WriteHeader(http.StatusNotFound)
		return
	}

	var op *lxd.RemoteOperation

	// Generate a unique and timestamped name for our copy
	s.destName = fmt.Sprintf("%s-backup-%s", s.Name, h.reqID)

	h.log = h.log.WithFields(log.Fields{
		"container": ct.Name,
		"copy":      s.destName})

	h.log.Debug()

	args := lxd.ContainerCopyArgs{
		Name: s.destName,
		// Default value as of lxc 2.17 is "pull" -- we'll use that
		Mode: "pull",
		// Don't copy stateful; no need to dump memory
		Live: false,
		// We don't want to copy any snapshots, just the running instance
		ContainerOnly: true,
	}

	// The following is copied almost verbatim from the lxc source code:
	// https://github.com/lxc/lxd/blob/b5678b80f32d2de619c88009a518bbdfca21d9d8/lxc/copy.go

	// Allow adding additional profiles
	if s.Profiles != nil {
		ct.Profiles = append(ct.Profiles, s.Profiles...)
	}

	// // Allow setting additional config keys
	// if s.Config != nil {
	//	for key, value := range s.Config {
	//		ct.Config[key] = value
	//	}
	// }

	ct.Ephemeral = s.Ephemeral

	// Strip any volatile keys. LXC uses volatile keys for things that should not
	// be transferred when copying a container, e.g. MAC addresses
	for k := range ct.Config {
		if k == "volatile.base_image" {
			continue
		}

		if strings.HasPrefix(k, "volatile") {
			delete(ct.Config, k)
		}
	}

	// Do the actual copy
	op, err := client.d.CopyContainer(client.d, ct, &args)
	if err != nil {
		h.log.WithError(err).Error("unable to copy container")
		return
	}

	// FIXME: Why do we have to do this?
	s.copyOp = op

	opFinished := make(chan bool)

	h.log.Debug("starting copy")
	go s.waitForOp(h, opFinished)

	select {
	case _ = <-opFinished:

		break

	case <-time.After(10 * time.Second):
		// The operation is taking too long. Save the operation along with the reqID
		// so that the caller may query the status at a later point.
		h.persistOperation(op)
	}
}

func (s BackupCommand) waitForOp(r Request, ch chan<- bool) (err error) {
	// Wait for the copy to complete
	err = s.copyOp.Wait()

	r.log.Debug("copy finished")

	startReq := api.ContainerStatePut{
		Action:   "start",
		Timeout:  120,
		Force:    false,
		Stateful: false,
	}

	startOp, err := client.d.UpdateContainerState(s.destName, startReq, "")
	if err != nil {
		r.Error(err, "failed to update lxc state")
		return err
	}

	err = startOp.Wait()
	if err != nil {
		r.Error(err, "starting lxc failed")
		return err
	}

	r.log.Debug("starting copied lxc")

	// FIXME: Default values for HOME and USER are now handled by LXD.
	// This code should be removed after most users upgraded.
	//
	// NOTE: This was added on 2017-01-30 in
	// 22f3d0e2e0df8fc882167d709d4d5f19438438f8; version 2.9 is the first tagged
	// release to have this.
	env := map[string]string{"HOME": "/root", "USER": "root"}

	// FIXME: Do we need to do it this way, or can we simply pass nil?
	var stdin io.ReadCloser
	stdin = os.Stdin
	stdin = ioutil.NopCloser(bytes.NewReader(nil))

	// FIXME: set up a buffer for stdout
	// stdout := bytes.NewBuffer()
	stdout := new(MemoryBuffer)

	// Run the associated command in the container copy
	execReq := api.ContainerExecPost{
		Command:     s.Command,
		WaitForWS:   true,
		Interactive: false,
		Environment: env,
		Width:       0,
		Height:      0,
	}

	execArgs := lxd.ContainerExecArgs{
		Stdin:  stdin,
		Stdout: stdout,
		// FIXME: Should we use a new io.ReadCloser here?
		Stderr: os.Stderr,
		// Since we're not interactive we don't need a handler
		Control:  nil,
		DataDone: make(chan bool),
	}

	r.log.Debug("sending exec operation to copied lxc")

	// Run the command in the container
	// https://github.com/lxc/lxd/blob/b5678b80f32d2de619c88009a518bbdfca21d9d8/lxc/exec.go
	execOp, err := client.d.ExecContainer(s.destName, execReq, &execArgs)
	if err != nil {
		r.Error(err, "failed to send exec operation")
		return err
	}

	r.log.Debug("exec operation sent")

	// Wait for the operation to complete
	err = execOp.Wait()
	if err != nil {
		r.Error(err, "exec operation failed")
		return err
	}

	r.log.Debug("exec operation completed; waiting for buffers to be flushed")

	// Wait for any remaining I/O to be flushed
	<-execArgs.DataDone

	r.log.WithFields(log.Fields{
		"bufsize": stdout.Len(),
	}).Debug("exec operation finished")

	r.log.Debug("copying files")

	files := strings.Split(stdout.String(), "\n")

	for i, filename := range files {
		if strings.TrimSpace(filename) == "" {
			continue
		}

		source := fmt.Sprintf("%s%s", s.Name, filename)
		destination := fmt.Sprintf("%s%s", s.destName,
			path.Join(s.Destination, path.Base(filename)))

		flog := r.log.WithFields(log.Fields{
			"fileno":      i,
			"filename":    filename,
			"source":      source,
			"destination": destination,
		})

		if filename[0] != '/' {
			flog.Error("invalid filename")
			continue
		}

		flog.Debug("copying...")

		fc := FileCmd{}

		args := []string{
			source,
			destination,
		}

		err = LXCPushFile(&fc, client.conf, true, args)
		if err != nil {
			flog.WithError(err).Error("copy failed")
			continue
		}

		flog.Debug("copied")
	}

	r.log.Debug("stopping lxc")

	stopReq := api.ContainerStatePut{
		Action:   "stop",
		Timeout:  2,
		Force:    true,
		Stateful: false,
	}

	stopOp, err := client.d.UpdateContainerState(s.destName, stopReq, "")
	if err != nil {
		r.Error(err, "failed to update lxc state")
		return err
	}

	err = stopOp.Wait()
	if err != nil {
		r.Error(err, "stopping lxc failed")
		return err
	}

	r.log.Debug("lxc stopped")

	// And finally we signal a return
	ch <- true
	return
}

// persistOperation adds the given operation to the internal state so that a
// caller may later query for status. Useful if the operation takes longer than
// e.g. HTTP timeout.
func (h Request) persistOperation(op *lxd.RemoteOperation) {
	persistedOperations[h.reqID] = op
}
