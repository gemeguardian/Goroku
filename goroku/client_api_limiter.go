package goroku

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// waitContext sleeps for d unless ctx ends first, in which case it reports the
// context error. time.Sleep here ignored cancellation entirely.
func waitContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type forbiddenInvoker struct {
	parent tg.Invoker
	client *CustomTelegramClient
}

func (f *forbiddenInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if input != nil {
		if t, ok := input.(interface{ TypeID() uint32 }); ok {
			typeID := t.TypeID()
			for _, forbidden := range f.client.forbiddenConstructorsSnapshot() {
				if typeID == forbidden {
					L().Warn("Blocked forbidden constructor call", zap.Int64("type_id", int64(typeID)))
					return fmt.Errorf("constructor %d is forbidden", typeID)
				}
			}

			// Rate limiting check
			db := f.client.GorokuDB
			if db != nil {
				disableProtection := db.GetBool("APILimiter", "disable_protection", true)
				if !disableProtection {
					f.client.RatelimitMu.Lock()
					bypassed := time.Now().Before(f.client.BypassSuspendUntil)
					f.client.RatelimitMu.Unlock()

					if !bypassed {
						// If currently suspended, wait — but honour ctx, or a
						// shutdown waits out the whole local floodwait for
						// nothing.
						f.client.RatelimitMu.Lock()
						for time.Now().Before(f.client.SuspendUntil) {
							dur := time.Until(f.client.SuspendUntil)
							f.client.RatelimitMu.Unlock()
							if err := waitContext(ctx, dur); err != nil {
								return err
							}
							f.client.RatelimitMu.Lock()
						}
						f.client.RatelimitMu.Unlock()

						typeName := fmt.Sprintf("%T", input)
						isTargetRequest := strings.HasPrefix(typeName, "*tg.Messages") ||
							strings.HasPrefix(typeName, "*tg.Channels") ||
							strings.HasPrefix(typeName, "*tg.Account")

						if isTargetRequest {
							f.client.RatelimitMu.Lock()
							now := time.Now()
							f.client.Ratelimiter = append(f.client.Ratelimiter, RateLimitRecord{Name: typeName, TS: now})

							// Filter records within time sample
							timeSampleSec := db.GetInt("APILimiter", "time_sample", 15)
							cutoff := now.Add(-time.Duration(timeSampleSec) * time.Second)
							var filtered []RateLimitRecord
							for _, r := range f.client.Ratelimiter {
								if r.TS.After(cutoff) {
									filtered = append(filtered, r)
								}
							}
							f.client.Ratelimiter = filtered

							threshold := db.GetInt("APILimiter", "threshold", 100)
							localFloodWait := db.GetInt("APILimiter", "local_floodwait", 30)

							if len(f.client.Ratelimiter) > threshold && !f.client.FloodWaitLock {
								f.client.FloodWaitLock = true
								f.client.SuspendUntil = now.Add(time.Duration(localFloodWait) * time.Second)

								// Copy Ratelimiter slice to prevent data race with concurrent reads/writes
								limiterCopy := make([]RateLimitRecord, len(f.client.Ratelimiter))
								copy(limiterCopy, f.client.Ratelimiter)

								f.client.RatelimitMu.Unlock()

								// Dump report and send
								reportBytes, _ := json.MarshalIndent(limiterCopy, "", "  ")
								caption := fmt.Sprintf("⚠️ <b>Goroku local floodwait triggered!</b>\n"+
									"Suspended all target calls for %d seconds to prevent API ban.", localFloodWait)

								// Send report via Bot API if available to bypass gotd suspension block, otherwise fall back to SendFile
								im := f.client.InlineManager()
								if im != nil && im.GetBotAPI() != nil {
									botClient := im.GetBotAPI()
									fb := tgbotapi.FileBytes{Name: "report.json", Bytes: reportBytes}
									go func() {
										doc := tgbotapi.NewDocument(f.client.TGIDValue(), fb)
										doc.Caption = caption
										doc.ParseMode = tgbotapi.ModeHTML
										_, _ = botClient.Send(doc)
									}()
								} else {
									go func(data []byte, capText string) {
										_, _ = f.client.SendFile(ChatRefID(f.client.TGIDValue()), data, capText)
									}(reportBytes, caption)
								}

								// Hold off for the local floodwait, but let a
								// cancelled context cut it short: Close() used
								// to wait out the full 30s default.
								waitErr := waitContext(ctx, time.Duration(localFloodWait)*time.Second)

								f.client.RatelimitMu.Lock()
								f.client.FloodWaitLock = false
								f.client.Ratelimiter = nil
								f.client.RatelimitMu.Unlock()
								if waitErr != nil {
									return waitErr
								}
							} else {
								f.client.RatelimitMu.Unlock()
							}
						}
					}
				}
			}
		}
	}
	err := f.parent.Invoke(ctx, input, output)
	if err != nil {
		if strings.Contains(err.Error(), "AUTH_KEY_UNREGISTERED") {
			HandleAuthKeyUnregistered(f.client.TGIDValue(), f.client.SessionPath)
		}
	}
	return err
}

// SetForbiddenConstructors replaces the blocked constructor list. The slice is
// copied and published atomically, so an RPC in flight sees either the old list
// or the new one — never a half-written one.
func (c *CustomTelegramClient) SetForbiddenConstructors(constructors []uint32) {
	snapshot := append([]uint32(nil), constructors...)
	c.forbiddenConstructors.Store(&snapshot)
}

// ForbiddenConstructors returns a snapshot of the blocked constructor list.
func (c *CustomTelegramClient) ForbiddenConstructors() []uint32 {
	current := c.forbiddenConstructors.Load()
	if current == nil {
		return nil
	}
	return append([]uint32(nil), *current...)
}

// forbiddenConstructorsSnapshot returns the published slice without copying it.
// The slice is never mutated after publication, so readers on the RPC hot path
// can use it directly.
func (c *CustomTelegramClient) forbiddenConstructorsSnapshot() []uint32 {
	current := c.forbiddenConstructors.Load()
	if current == nil {
		return nil
	}
	return *current
}

func (c *CustomTelegramClient) ForbidConstructor(constructor uint32) {
	c.ForbidConstructors([]uint32{constructor})
}

// ForbidConstructors appends to the blocked list. Compare-and-swap rather than
// append-in-place: two modules forbidding constructors at once must not lose
// one of the updates.
func (c *CustomTelegramClient) ForbidConstructors(constructors []uint32) {
	if len(constructors) == 0 {
		return
	}
	for {
		current := c.forbiddenConstructors.Load()
		var updated []uint32
		if current != nil {
			updated = append(updated, *current...)
		}
		updated = append(updated, constructors...)
		if c.forbiddenConstructors.CompareAndSwap(current, &updated) {
			return
		}
	}
}
