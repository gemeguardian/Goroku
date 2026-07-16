package goroku

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

func (c *CustomTelegramClient) Connect() error {
	return c.ConnectContext(context.Background())
}

// ConnectContext connects the client and aborts authentication/startup when ctx
// is canceled. The resulting client connection also inherits ctx.
func (c *CustomTelegramClient) ConnectContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.APIID == 0 || c.APIHash == "" {
		return fmt.Errorf("telegram api_id/api_hash is not configured")
	}

	// Cancel any previous connection attempt so we don't leak goroutines or
	// race against an old client.Run.
	if err := c.Close(ctx); err != nil {
		return fmt.Errorf("close previous Telegram connection: %w", err)
	}
	runCtx, runCancel := context.WithCancel(ctx)
	c.runMu.Lock()
	c.ctx, c.cancel = runCtx, runCancel
	c.runErr = nil
	c.runMu.Unlock()

	connectResult := make(chan error, 1)
	sessionPath := c.SessionPath
	if sessionPath == "" {
		sessionPath = filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", c.TGID))
	}
	storage := &session.FileStorage{Path: sessionPath}

	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		c.cacheEntities(e)
		msg, ok := u.Message.(*tg.Message)
		if !ok {
			return nil
		}

		hMsg := c.buildMessageFromTG(msg)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleCommand(hMsg)
				disp.HandleIncoming(hMsg)
			}
		}
		return nil
	})

	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		c.cacheEntities(e)
		msg, ok := u.Message.(*tg.Message)
		if !ok {
			return nil
		}

		hMsg := c.buildMessageFromTG(msg)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleCommand(hMsg)
				disp.HandleIncoming(hMsg)
			}
		}
		return nil
	})

	editHandler := func(ctx context.Context, e tg.Entities, msg tg.MessageClass) error {
		c.cacheEntities(e)
		m, ok := msg.(*tg.Message)
		if !ok {
			return nil
		}

		hMsg := c.buildMessageFromTG(m)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleIncoming(hMsg)
			}
		}
		return nil
	}

	dispatcher.OnEditMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditMessage) error {
		return editHandler(ctx, e, u.Message)
	})

	dispatcher.OnEditChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateEditChannelMessage) error {
		return editHandler(ctx, e, u.Message)
	})

	dispatcher.OnBotInlineQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotInlineQuery) error {
		c.cacheEntities(e)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleInlineQuery(u)
			}
		}
		return nil
	})

	dispatcher.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotCallbackQuery) error {
		c.cacheEntities(e)
		if c.Loader != nil {
			disp := c.Loader.GetDispatcher()
			if disp != nil {
				disp.HandleCallbackQuery(u)
			}
		}
		return nil
	})

	sysVer := os.Getenv("SYSTEM_VERSION")
	if sysVer == "" {
		sysVer = generateRandomSystemVersion()
	}
	opts := telegram.Options{
		SessionStorage: storage,
		UpdateHandler:  dispatcher,
		Middlewares: []telegram.Middleware{
			telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
				return (&forbiddenInvoker{parent: next, client: c}).Invoke
			}),
		},
		Device: telegram.DeviceConfig{
			SystemVersion: sysVer,
		},
	}
	if resolver, enabled, err := mtProxyResolver(); err != nil {
		runCancel()
		return fmt.Errorf("configure MTProto proxy: %w", err)
	} else if enabled {
		opts.Resolver = resolver
	}
	client := telegram.NewClient(int(c.APIID), c.APIHash, opts)

	c.client = client
	c.rawAPI = client.API()
	runDone := make(chan struct{})
	c.runMu.Lock()
	c.runDone = runDone
	c.runMu.Unlock()

	go func() {
		defer close(runDone)
		err := client.Run(runCtx, func(ctx context.Context) error {
			status, err := client.Auth().Status(ctx)
			if err != nil {
				select {
				case connectResult <- err:
				default:
				}
				return err
			}

			if status.Authorized {
				me, err := client.Self(ctx)
				if err == nil {
					c.TGID = me.ID
					c.Username = me.Username
					c.GorokuMe = me
				}
				_ = c.CacheDialogs()
			}

			select {
			case connectResult <- nil:
			default:
			}
			<-ctx.Done()
			return nil
		})
		if err != nil {
			if strings.Contains(err.Error(), "AUTH_KEY_UNREGISTERED") {
				HandleAuthKeyUnregistered(c.TGID, c.SessionPath)
			}
			L().Error("gotd client run error", zap.Error(err))
			select {
			case connectResult <- err:
			default:
			}
		}
		c.runMu.Lock()
		c.runErr = err
		c.runMu.Unlock()
	}()

	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer connectCancel()
	select {
	case err := <-connectResult:
		if err != nil {
			runCancel()
		}
		return err
	case <-connectCtx.Done():
		runCancel()
		return fmt.Errorf("connect Telegram client: %w", connectCtx.Err())
	}
}
