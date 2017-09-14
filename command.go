package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	log "github.com/sirupsen/logrus"
)

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

	id        string
	timestamp time.Time
	log       *log.Entry

	srcCt api.Container

	err chan error
}

func (cmd BackupCommand) Error(err error, message string) {
	defer close(cmd.err)

	if err != nil {
		cmd.log.WithError(err).Error(message)
		cmd.err <- err
	}

	stopReq := api.ContainerStatePut{
		Action:   "stop",
		Timeout:  2,
		Force:    true,
		Stateful: false,
	}

	stopOp, err := client.d.UpdateContainerState(cmd.destName, stopReq, "")
	if err != nil {
		cmd.log.WithError(err).Error("failed to update lxc state")
		return
	}

	err = stopOp.Wait()
	if err != nil {
		cmd.log.WithError(err).Error("stopping lxc failed")
		return
	}

	cmd.log.Debug("lxc stopped")
}

func (cmd BackupCommand) Handle(req Request) {
	cts := client.GetContainers()
	ct, ok := cts[cmd.Name]

	if !ok {
		req.w.WriteHeader(http.StatusNotFound)
		return
	}

	cmd.srcCt = ct
	// Generate a unique and timestamped name for our copy
	cmd.destName = fmt.Sprintf("%s-backup-%s", cmd.Name, cmd.id)
	cmd.log = req.log.WithFields(log.Fields{
		"container": ct.Name,
		"copy":      cmd.destName})

	cmd.log.Debug()

	opErr := persistedOperations.Add(&cmd)

	select {
	case err := <-opErr:
		if err != nil {
			responseBody, _ := json.Marshal(HttpError{err})
			req.w.Write(responseBody)
			req.w.WriteHeader(http.StatusInternalServerError)
			return
		}

		break

	case <-time.After(1e6 * time.Nanosecond):
		req.w.WriteHeader(http.StatusAccepted)
	}

}

func (cmd BackupCommand) process() {
	cmd.log.Debug()

	args := lxd.ContainerCopyArgs{
		Name: cmd.destName,
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
	if cmd.Profiles != nil {
		cmd.srcCt.Profiles = append(cmd.srcCt.Profiles, cmd.Profiles...)
	}

	// // Allow setting additional config keys
	// if cmd.Config != nil {
	//	for key, value := range cmd.Config {
	//		ct.Config[key] = value
	//	}
	// }

	cmd.srcCt.Ephemeral = cmd.Ephemeral

	// Strip any volatile keys. LXC uses volatile keys for things that should not
	// be transferred when copying a container, e.g. MAC addresses
	for k := range cmd.srcCt.Config {
		if k == "volatile.base_image" {
			continue
		}

		if strings.HasPrefix(k, "volatile") {
			delete(cmd.srcCt.Config, k)
		}
	}

	// Do the actual copy
	copyOp, err := client.d.CopyContainer(client.d, cmd.srcCt, &args)
	if err != nil {
		cmd.log.WithError(err).Error("failed to send copy command")
		return
	}

	// Wait for the copy to complete
	err = copyOp.Wait()

	cmd.log.Debug("copy finished")

	startReq := api.ContainerStatePut{
		Action:   "start",
		Timeout:  120,
		Force:    false,
		Stateful: false,
	}

	startOp, err := client.d.UpdateContainerState(cmd.destName, startReq, "")
	if err != nil {
		cmd.Error(err, "failed to update lxc state")
		return
	}

	err = startOp.Wait()
	if err != nil {
		cmd.Error(err, "starting lxc failed")
		return
	}

	cmd.log.Debug("starting copied lxc")

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
		Command:     cmd.Command,
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

	cmd.log.Debug("sending exec operation to copied lxc")

	// Run the command in the container
	// https://github.com/lxc/lxd/blob/b5678b80f32d2de619c88009a518bbdfca21d9d8/lxc/exec.go
	execOp, err := client.d.ExecContainer(cmd.destName, execReq, &execArgs)
	if err != nil {
		cmd.Error(err, "failed to send exec operation")
		return
	}

	cmd.log.Debug("exec operation sent")

	// Wait for the operation to complete
	err = execOp.Wait()
	if err != nil {
		cmd.Error(err, "exec operation failed")
		return
	}

	cmd.log.Debug("exec operation completed; waiting for buffers to be flushed")

	// Wait for any remaining I/O to be flushed
	<-execArgs.DataDone

	cmd.log.WithFields(log.Fields{
		"bufsize": stdout.Len(),
	}).Debug("exec operation finished")

	cmd.log.Debug("copying files")

	files := strings.Split(stdout.String(), "\n")

	var sources []string
	for _, filename := range files {
		if strings.TrimSpace(filename) == "" {
			continue
		}

		if filename[0] != '/' {
			log.Error("invalid filename")
			continue
		}

		sources = append(sources, filename)
	}

	log.Debug("copying...")

	err = LXCPullFile(cmd.log, &cmd.srcCt, cmd.destName, sources, fileDest)
	if err != nil {
		cmd.Error(err, "copy failed")
	}

	cmd.Error(nil, "")

	return
}
