package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

func genID() string {
	id := make([]byte, 8)
	rand.Read(id)
	hexID := hex.EncodeToString(id)

	return hexID
}

type MemoryBuffer struct {
	bytes.Buffer
}

func (m *MemoryBuffer) Close() (err error) {
	fmt.Println("closing buffer...")
	return nil
}

type PersistentOperations struct {
	cmds  map[string]*BackupCommand
	addCh chan *BackupCommand
}

func (ps PersistentOperations) Add(cmd *BackupCommand) chan error {
	cmd.log.Debug("adding operation")
	cmd.err = make(chan error)

	ps.addCh <- cmd

	cmd.log.Debug("starting operations")
	go cmd.process()
	return cmd.err
}

func NewPersistentOperations() *PersistentOperations {
	ps := PersistentOperations{
		cmds:  make(map[string]*BackupCommand),
		addCh: make(chan *BackupCommand),
	}

	go ps.Run()
	go ps.Prune()
	return &ps
}

func (ps PersistentOperations) Run() {
	for {
		cmd := <-ps.addCh
		cmd.log.Debug("captured persistent operation")
		ps.cmds[cmd.id] = cmd
	}

}

func (ps PersistentOperations) Prune() {
	for {
		timeout := time.After(120 * time.Minute)
		select {
		case <-timeout:
			for id, cmd := range ps.cmds {
				since := time.Since(cmd.timestamp)
				if since > (120 * time.Minute) {
					cmd.log.Debug("deleting old cmd")
					ps.Delete(id)
				}
			}
		}
	}

}

func (ps PersistentOperations) Delete(id string) {
	cmd, ok := ps.cmds[id]

	if !ok {
		log.Error("tried to delete non-existant op")
		return
	}

	delete(ps.cmds, id)

	cmd.log.Debug("deleted")

}

func (ps PersistentOperations) Get(id string) (int, error) {
	p, ok := ps.cmds[id]

	if !ok {
		return http.StatusNotFound, nil
	}

	select {
	case err, more := <-p.err:
		if more && err == nil {
			return http.StatusProcessing, nil
		}

		if !more && err == nil {
			ps.Delete(id)
			return http.StatusOK, nil
		}

		if err != nil {
			return http.StatusInternalServerError, err
		}

	default:
		return http.StatusProcessing, nil
	}

	ps.Delete(id)
	return http.StatusInternalServerError, errors.New("unknown error")
}

func (ps PersistentOperations) Keys() []string {
	keys := []string{}
	for k := range ps.cmds {
		keys = append(keys, k)
	}

	return keys
}

type HttpError struct {
	error
}
