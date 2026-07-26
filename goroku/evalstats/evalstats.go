// Package evalstats counts Yaegi eval goroutines that outlived their timeout.
//
// .eval runs inside the bot process and Yaegi has no way to cancel a running
// interpretation. When an eval exceeds its deadline the handler returns a
// timeout, but the goroutine keeps running until the process exits. The
// operator has to be able to see that accumulating, so the count is reported by
// the web /health endpoint.
//
// It lives in its own leaf package because the modules package (which produces
// the count) imports goroku, and goroku imports web (which reports it) — a
// shared counter anywhere else would be an import cycle.
package evalstats

import "sync/atomic"

var stuck atomic.Int64

// Enter records an eval goroutine abandoned after its deadline expired.
func Enter() { stuck.Add(1) }

// Leave records an abandoned goroutine that finished after all.
func Leave() { stuck.Add(-1) }

// Stuck reports how many abandoned eval goroutines are still running.
func Stuck() int64 { return stuck.Load() }
