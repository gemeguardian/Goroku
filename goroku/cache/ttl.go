package cache

// UseCached reports whether a cached record should satisfy a request with the
// given TTL (seconds).
//
// Semantics (unified across entity / full-user / full-channel / perms):
//   - requestTTL == 0: always accept a present cache entry (ignore stored Exp)
//   - requestTTL  > 0: accept only when the stored record is not expired
//
// Force-refresh is handled by callers (force=true skips the cache read entirely).
func UseCached(requestTTL int64, expired bool) bool {
	return requestTTL == 0 || !expired
}

// CacheExpiryUnix returns the absolute Unix expiry for a newly written record.
// requestTTL == 0 stores Exp = now so a later positive-TTL read still revalidates;
// a zero-TTL read continues to accept the entry via UseCached.
func CacheExpiryUnix(nowUnix, requestTTL int64) int64 {
	return nowUnix + requestTTL
}
