package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
