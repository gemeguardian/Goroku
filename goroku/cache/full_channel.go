package cache

import "time"

type CacheRecordFullChannel struct {
	ChannelID   interface{}
	FullChannel interface{}
	Exp         int64
	TS          int64
}

func (r CacheRecordFullChannel) Expired() bool {
	return r.Exp < time.Now().Unix()
}
