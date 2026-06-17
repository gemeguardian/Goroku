package cache

import "time"

type CacheRecordEntity struct {
	Entity interface{}
	Exp    int64
	TS     int64
}

func (r CacheRecordEntity) Expired() bool {
	return r.Exp < time.Now().Unix()
}
