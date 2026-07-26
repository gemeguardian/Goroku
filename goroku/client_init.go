package goroku

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"goroku/goroku/web"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

func (h *Goroku) initClient(tgID int64, sessionPath string, factories []ModuleFactory) (*CustomTelegramClient, error) {
	return h.initClientContext(context.Background(), tgID, sessionPath, factories)
}

func (h *Goroku) initClientContext(ctx context.Context, tgID int64, sessionPath string, factories []ModuleFactory) (*CustomTelegramClient, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := h.beginLifecycleOperation(); err != nil {
		return nil, err
	}
	defer h.endLifecycleOperation()
	utils.SecureFile(sessionPath)
	db := NewDatabase(tgID)
	redisURI := os.Getenv("REDIS_URL")
	if redisURI == "" {
		if val := GetConfigKey("redis_uri"); val != nil {
			redisURI = fmt.Sprintf("%v", val)
		}
	}
	if err := db.Init(redisURI); err != nil {
		return nil, joinDatabaseCloseError(fmt.Errorf("initialize database: %w", err), db)
	}
	if err := ctx.Err(); err != nil {
		return nil, joinDatabaseCloseError(err, db)
	}

	client := NewCustomTelegramClient(tgID)
	client.APIID = h.APIID
	client.APIHash = h.APIHash
	client.SessionPath = sessionPath
	client.GorokuDB = db
	client.Web = h.Web

	connect := h.connectClient
	if connect == nil {
		connect = func(ctx context.Context, client *CustomTelegramClient) error { return client.ConnectContext(ctx) }
	}
	if err := connect(ctx, client); err != nil {
		_ = client.Disconnect()
		return nil, joinDatabaseCloseError(err, db)
	}
	// From here on a dead transport must either come back or take the process
	// with it; silently standing dead is what /readyz used to hide.
	h.superviseConnection(client)
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, h.cleanupUnregisteredRuntime(client, nil, db))
	}

	loader := NewModules(client, db)
	client.Loader = loader
	// Wire narrow DB hooks only after loader exists (no Database -> full client).
	db.AttachRuntime(loader, loader, newTelegramAssetTransport(client))

	inlineMgr := inline.NewInlineManager(client, db, NewInlineModulesAdapter(loader))
	client.GorokuInline = inlineMgr

	for _, factory := range factories {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, h.cleanupUnregisteredRuntime(client, loader, db))
		}
		if factory == nil {
			continue
		}
		mod := factory()
		if mod == nil {
			continue
		}
		if err := loader.RegisterModule(mod); err != nil {
			L().Error("Failed to register module", zap.String("module", mod.Name()), zap.Error(err))
		}
	}

	disp, err := NewCommandDispatcherChecked(loader, client, db)
	if err != nil {
		cleanupErr := h.cleanupUnregisteredRuntime(client, loader, db)
		return nil, errors.Join(fmt.Errorf("initialize command dispatcher: %w", err), cleanupErr)
	}
	loader.SetDispatcher(disp)
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, h.cleanupUnregisteredRuntime(client, loader, db))
	}

	loader.SendReady()
	if h.clientReadyHook != nil {
		if err := h.clientReadyHook(ctx, client, db); err != nil {
			L().Error("Client ready hook failed", zap.Error(err))
		}
	}

	h.sendBadge(client, db)

	h.lifecycleMu.Lock()
	if h.shuttingDown {
		h.lifecycleMu.Unlock()
		cleanupErr := h.removeAndShutdownRuntime(context.Background(), client, loader, db)
		return nil, errors.Join(fmt.Errorf("goroku is shutting down"), cleanupErr)
	}
	h.Clients = append(h.Clients, client)
	h.DBs = append(h.DBs, db)
	h.Loaders = append(h.Loaders, loader)
	h.lifecycleMu.Unlock()

	return client, nil
}

func (h *Goroku) cleanupUnregisteredRuntime(client *CustomTelegramClient, loader *Modules, db *Database) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.removeAndShutdownRuntime(ctx, client, loader, db)
}

type contextCloser interface {
	Close(context.Context) error
}

// ErrRuntimeCleanupDeferred reports that teardown is continuing in the
// background because the caller's context expired at a lifecycle barrier.
var ErrRuntimeCleanupDeferred = errors.New("runtime cleanup deferred")

func joinDatabaseCloseError(primary error, closer contextCloser) error {
	if closer == nil {
		return primary
	}
	return errors.Join(primary, closer.Close(context.Background()))
}

func (h *Goroku) registerWebRuntime(client *CustomTelegramClient) error {
	if h.Web == nil || client == nil {
		return nil
	}
	loader := client.Loader
	db := client.GorokuDB
	h.lifecycleMu.Lock()
	if h.shuttingDown {
		h.lifecycleMu.Unlock()
		return errors.Join(context.Canceled, h.cleanupUnregisteredRuntime(client, loader, db))
	}
	err := h.Web.RegisterClient(web.RuntimeClient{ID: client.TGIDValue(), Client: client, Loader: loader, Database: db})
	h.lifecycleMu.Unlock()
	if err == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cleanupErr := h.removeAndShutdownRuntime(ctx, client, loader, db)
	return errors.Join(fmt.Errorf("register web runtime: %w", err), cleanupErr)
}

func (h *Goroku) removeAndShutdownRuntime(ctx context.Context, client *CustomTelegramClient, loader *Modules, db *Database) error {
	h.lifecycleMu.Lock()
	for i, existing := range h.Clients {
		if existing == client {
			h.Clients = append(h.Clients[:i], h.Clients[i+1:]...)
			break
		}
	}
	for i, existing := range h.Loaders {
		if existing == loader {
			h.Loaders = append(h.Loaders[:i], h.Loaders[i+1:]...)
			break
		}
	}
	for i, existing := range h.DBs {
		if existing == db {
			h.DBs = append(h.DBs[:i], h.DBs[i+1:]...)
			break
		}
	}
	h.lifecycleMu.Unlock()

	cleanup := func(waitCtx context.Context) (error, bool) {
		var cleanupErrs []error
		if loader != nil {
			if dispatcher := loader.GetDispatcher(); dispatcher != nil {
				if err := dispatcher.Close(waitCtx); err != nil {
					cleanupErrs = append(cleanupErrs, err)
					if waitCtx.Err() != nil {
						return errors.Join(cleanupErrs...), true
					}
				}
			}
		}
		if client != nil {
			if inlineManager, ok := client.GorokuInline.(contextCloser); ok {
				if err := inlineManager.Close(waitCtx); err != nil {
					cleanupErrs = append(cleanupErrs, err)
					if waitCtx.Err() != nil {
						return errors.Join(cleanupErrs...), true
					}
				}
			}
		}
		if loader != nil {
			if err := loader.Shutdown(waitCtx); err != nil {
				cleanupErrs = append(cleanupErrs, err)
				if waitCtx.Err() != nil {
					return errors.Join(cleanupErrs...), true
				}
			}
		}
		if err := waitCtx.Err(); err != nil {
			cleanupErrs = append(cleanupErrs, err)
			return errors.Join(cleanupErrs...), true
		}
		if client != nil {
			client.GracefulStop(waitCtx)
		}
		if err := waitCtx.Err(); err != nil {
			cleanupErrs = append(cleanupErrs, err)
			return errors.Join(cleanupErrs...), true
		}
		if db != nil {
			if err := db.Close(waitCtx); err != nil {
				cleanupErrs = append(cleanupErrs, err)
				if waitCtx.Err() != nil {
					return errors.Join(cleanupErrs...), true
				}
			}
		}
		return errors.Join(cleanupErrs...), false
	}
	cleanupErr, deferred := cleanup(ctx)
	if deferred {
		go func() {
			if deferredErr, _ := cleanup(context.Background()); deferredErr != nil {
				L().Error("Deferred runtime cleanup failed", zap.Error(deferredErr))
			}
		}()
		return errors.Join(ErrRuntimeCleanupDeferred, cleanupErr)
	}
	return cleanupErr
}

func (h *Goroku) sendBadge(client *CustomTelegramClient, db *Database) {
	me, err := client.GetMe()
	if err != nil {
		return
	}
	var name string
	if u, ok := me.(*tg.User); ok {
		if u.FirstName != "" {
			name = u.FirstName
		} else {
			name = u.Username
		}
	} else {
		name = client.Username
	}

	uptime := utils.FormattedUptime()
	platform := utils.GetPlatformName()
	emoji := utils.GetPlatformEmoji()

	msg := fmt.Sprintf(
		"🪐 <b>Goroku Userbot</b> started!\n\n"+
			"👤 <b>Account:</b> %s\n"+
			"🖥 <b>Platform:</b> %s %s\n"+
			"⏱ <b>Uptime:</b> %s\n"+
			"📦 <b>Version:</b> %s",
		name, platform, emoji, uptime, GetVersionString(),
	)

	_, _ = client.SendMessage(ChatRefID(client.TGID), msg)
}

func GenerateAppName() string {
	latin := []string{
		"Amor", "Arbor", "Astra", "Aurum", "Bellum", "Caelum",
		"Calor", "Candor", "Carpe", "Celer", "Certo", "Cibus",
		"Civis", "Clemens", "Coetus", "Cogito", "Conexus",
	}
	return fmt.Sprintf("%s %s %s", latin[rand.Intn(len(latin))], latin[rand.Intn(len(latin))], latin[rand.Intn(len(latin))]) //nolint:gosec
}

func generateRandomSystemVersion() string {
	systems := []string{
		"Ubuntu 22.04", "Ubuntu 24.04", "Fedora 38",
		"Debian 12 Bookworm", "Arch Linux", "CentOS Stream 9",
		"openSUSE Leap 15.5", "Manjaro 23.0", "Pop!_OS 22.04",
		"Linux Mint 21.2", "Kali Linux 2023.3",
	}
	return systems[rand.Intn(len(systems))] //nolint:gosec
}
