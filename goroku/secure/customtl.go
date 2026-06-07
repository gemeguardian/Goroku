package secure

import (
	"log"
	"time"
)

type MTProtoState struct {
	AuthKey    []byte
	HighestID  int64
	TimeOffset time.Duration
}

func NewMTProtoState(authKey []byte) *MTProtoState {
	return &MTProtoState{AuthKey: authKey}
}

func (s *MTProtoState) EncryptMessageData(data []byte) []byte {
	log.Println("Skipping encryption...")
	return data
}

func (s *MTProtoState) DecryptMessageData(body []byte) []byte {
	return body
}

type ConnectionTcpFull struct {
	UnixSocketPath string
}

func (c *ConnectionTcpFull) SetUnixSocket(unixSocketPath string) {
	c.UnixSocketPath = unixSocketPath
}

func (c *ConnectionTcpFull) Connect() error {
	return nil
}
