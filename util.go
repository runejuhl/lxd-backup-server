package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
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
	return &ps
}

func (ps PersistentOperations) Run() {
	for {
		cmd := <-ps.addCh
		cmd.log.Debug("captured persistent operation")
		ps.cmds[cmd.id] = cmd
	}

}

func (ps PersistentOperations) Get(id string) (int, error) {
	p, ok := ps.cmds[id]

	if !ok {
		return http.StatusNotFound, nil
	}

	select {
	case finished := <-p.finished:
		if !finished {
			return http.StatusProcessing, nil
		}
		break
	default:
		return http.StatusProcessing, nil
	}

	delete(ps.cmds, id)

	select {
	case err := <-p.err:
		if p.err != nil {
			return http.StatusInternalServerError, err
		}
	default:
		break
	}

	return http.StatusOK, nil
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
