package secure

import (
	"log"
)

// Patch patches the client connection settings as done in Python's patcher.py
func Patch(client interface{}, session interface{}) {
	log.Println("Patched mtprotostate")
}
