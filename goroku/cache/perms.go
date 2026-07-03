package cache

import "time"

type CacheRecordPerms struct {
	Perms any
	Exp   int64
	TS    int64
}

func (r CacheRecordPerms) Expired() bool {
	return r.Exp < time.Now().Unix()
}
