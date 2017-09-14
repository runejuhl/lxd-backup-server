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

	cmd.log.Debug("deleted persistent operation")

}

func (ps PersistentOperations) Get(id string) (int, error) {
	cmd, ok := ps.cmds[id]

	if !ok {
		return http.StatusNotFound, nil
	}

	select {
	case err, more := <-cmd.err:
		if more && err == nil {
			return http.StatusProcessing, nil
		}

		if !more && err == nil {
			defer ps.Delete(id)
			return http.StatusOK, nil
		}

		if err != nil {
			defer ps.Delete(id)
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

// SimpleSet A set type for strings
type SimpleSet struct {
	set map[string]bool
}

// ToArray Convert a SimpleSet to []string
func (ss *SimpleSet) ToArray() []string {
	a := []string{}
	for k := range ss.set {
		a = append(a, k)
	}

	return a
}

// NewSimpleSet Create a new SimpleSet
func NewSimpleSet() SimpleSet {
	return SimpleSet{
		set: make(map[string]bool),
	}
}

// Add Add an item to a SimpleSet
func (ss *SimpleSet) Add(s string) {
	ss.set[s] = true
}

// Remove Remove an item from a SimpleSet
func (ss *SimpleSet) Remove(s string) {
	delete(ss.set, s)
}

// Flush Remove all items in a set
func (ss *SimpleSet) Flush() {
	ss.set = make(map[string]bool)
}
