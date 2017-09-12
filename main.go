package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	logging "github.com/op/go-logging"
)

var (
	name = "lxd-snapshot-server"
	log  = logging.MustGetLogger(name)

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

func main() {
	startLogger()

	cts := c.GetContainers()

	log.Debugf("%+v", cts["android"])

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", handleRequest)

	log.Debugf("starting server on port :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))

}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	reqID := genID()

	w.Header().Set("Request-ID", reqID)

	switch r.Method {
	case http.MethodPost:
		routePost(w, r)
		w.WriteHeader(http.StatusCreated)
		return
	default:
		w.WriteHeader(http.StatusNotFound)
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
func routePost(w http.ResponseWriter, r *http.Request) {
	reqID := w.Header().Get("Request-ID")
	log.Debugf("[%s] POST %s",
		reqID,
		r.URL.Path,
	)

	switch r.URL.Path {
	case "/snapshot":
		body, err := getBodyFromReq(w, r.Body)

		log.Debugf("%+v", body)
		if err != nil {
			return
		}

	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
}
