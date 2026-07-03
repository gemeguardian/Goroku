package cache

import (
	"time"

	"github.com/gotd/td/tg"
)

type CacheRecordFullChannel struct {
	Channel *tg.MessagesChatFull
	Exp     int64
	TS      int64
}

func (r CacheRecordFullChannel) Expired() bool {
	return r.Exp < time.Now().Unix()
}
