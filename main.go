package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/ssgreg/journalhook"
)

var (
	name = "lxd-snapshot-server"

	client = InitClient()

	persistedOperations = NewPersistentOperations()

	fileDest string

	// global logger
	log *logrus.Logger
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
	ID  string
	w   http.ResponseWriter
	r   *http.Request
	log *logrus.Entry
}

func main() {

	log = logrus.New()
	log.SetLevel(logrus.DebugLevel)

	hook, err := journalhook.NewJournalHook()
	if err == nil {
		log.Hooks.Add(hook)
	}

	fileDest = os.Getenv("FILE_DESTINATION")
	if fileDest == "" {
		log.Fatal("No FILE_DESTINATION set; aborting")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", handleRequest)

	log.Infof("starting server on port :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))

}

func handleRequest(hw http.ResponseWriter, hr *http.Request) {
	reqID := genID()

	req := Request{
		ID: reqID,
		w:  hw,
		r:  hr,
		log: log.WithFields(logrus.Fields{
			"reqID":  reqID,
			"url":    hr.URL.Path,
			"method": hr.Method,
		}),
	}

	req.w.Header().Set("Request-ID", req.ID)
	req.log.Info()

	switch req.r.URL.Path {
	case "/backup":
		switch req.r.Method {
		case http.MethodGet:
			id := req.r.Header.Get("Request-Id")

			if id == "" {
				req.w.WriteHeader(http.StatusBadRequest)
				return
			}

			status, err := persistedOperations.Get(id)
			req.w.WriteHeader(status)

			if err != nil {
				req.log.WithError(err).Error()
				req.w.Write([]byte(fmt.Sprintf("%s\n", err.Error())))
			}

			return

		case http.MethodPost:
			body, err := getBodyFromReq(req.w, req.r.Body)

			if err != nil {
				return
			}

			cmd := BackupCommand{}
			err = json.Unmarshal(body.Bytes(), &cmd)

			if err != nil {
				req.w.WriteHeader(http.StatusInternalServerError)
				return
			}

			cmd.id = req.ID
			cmd.timestamp = time.Now()

			cmd.Handle(req)
			return

		}
	case "/backup/list":
		if req.r.Method != http.MethodGet {
			req.w.WriteHeader(http.StatusBadRequest)
			return
		}

		responseBody, _ := json.Marshal(persistedOperations.Keys())
		req.w.WriteHeader(http.StatusOK)
		req.w.Write(responseBody)
		return
	}

	req.w.WriteHeader(http.StatusNotFound)
	return
}

func getBodyFromReq(w http.ResponseWriter, r io.ReadCloser) (bytes.Buffer, error) {
	rawBody := new(bytes.Buffer)
	b, err := rawBody.ReadFrom(r)

	if err != nil {
		log.WithFields(logrus.Fields{
			"bytes": b,
		}).WithError(err).Warning()
		w.WriteHeader(http.StatusBadRequest)
		return *rawBody, err
	}

	return *rawBody, err
}
