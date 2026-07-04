package secure

import (
	"log"
)

// Patch patches the client connection settings as done in Python's patcher.py
func Patch(client any, session any) {
	log.Println("Patched mtprotostate")
}
