package main

import (
	"crypto/rand"
	"encoding/hex"
)

func genID() string {
	id := make([]byte, 8)
	rand.Read(id)
	hexID := hex.EncodeToString(id)

	return hexID
}
