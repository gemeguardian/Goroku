package cache

import "time"

type CacheRecordFullUser struct {
	UserID   interface{}
	FullUser interface{}
	Exp      int64
	TS       int64
}

func (r CacheRecordFullUser) Expired() bool {
	return r.Exp < time.Now().Unix()
}
