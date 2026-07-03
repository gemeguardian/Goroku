package cache

import (
	"time"

	"github.com/gotd/td/tg"
)

type CacheRecordEntity struct {
	Entity tg.InputPeerClass
	Exp    int64
	TS     int64
}

func (r CacheRecordEntity) Expired() bool {
	return r.Exp < time.Now().Unix()
}
