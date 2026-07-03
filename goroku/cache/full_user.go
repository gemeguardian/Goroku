package cache

import (
	"time"

	"github.com/gotd/td/tg"
)

type CacheRecordFullUser struct {
	User *tg.UsersUserFull
	Exp  int64
	TS   int64
}

func (r CacheRecordFullUser) Expired() bool {
	return r.Exp < time.Now().Unix()
}
