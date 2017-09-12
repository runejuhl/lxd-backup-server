package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	log "github.com/sirupsen/logrus"
)

var (
	name = "lxd-snapshot-server"

	c = InitClient()

	urlMaxLength = int64(240)
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
	w   http.ResponseWriter
	r   *http.Request
	log *log.Entry
}

func main() {

	log.SetLevel(log.DebugLevel)
	cts := c.GetContainers()

	log.Debug(cts["android"])

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
		w:   hw,
		r:   hr,
		log: log.WithFields(log.Fields{"request_id": reqID}),
	}

	h.w.Header().Set("Request-ID", reqID)

	switch h.r.Method {
	case http.MethodPost:
		routePost(h)
		h.w.WriteHeader(http.StatusCreated)
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
		body, err := getBodyFromReq(h.w, h.r.Body)

		log.Debugf("%+v", body)
		if err != nil {
			return
		}

	default:
		h.w.WriteHeader(http.StatusNotFound)
		return
	}
}
