package goroku

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goroku/goroku/utils"
	"goroku/goroku/webiface"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

func (h *Goroku) finishWebLogin(pending webiface.TelegramClient, factories []ModuleFactory) error {
	ctx := h.lifecycleContext()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := h.beginLifecycleOperation(); err != nil {
		return err
	}
	defer h.endLifecycleOperation()
	pendingClient, ok := pending.(*CustomTelegramClient)
	if !ok || pendingClient == nil {
		return fmt.Errorf("unexpected pending client type %T", pending)
	}

	tgID := pendingClient.TGID
	if tgID == 0 {
		me, err := pendingClient.GetMe()
		if err != nil {
			return fmt.Errorf("failed to get authorized user: %v", err)
		}
		if user, ok := me.(*tg.User); ok {
			tgID = user.ID
		}
	}
	if tgID == 0 {
		return fmt.Errorf("authorized Telegram user id is unknown")
	}

	h.lifecycleMu.Lock()
	clients := append([]*CustomTelegramClient(nil), h.Clients...)
	h.lifecycleMu.Unlock()
	for _, existing := range clients {
		if existing.TGID == tgID {
			return fmt.Errorf("client %d is already running", tgID)
		}
	}

	if err := pendingClient.Disconnect(); err != nil { // waits for client.Run to finish via runDone
		return fmt.Errorf("disconnect temporary client: %w", err)
	}

	oldSession := filepath.Join(BaseDir, "goroku-0.session")
	newSession := filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", tgID))
	if oldSession != newSession {
		if _, err := os.Stat(oldSession); err == nil {
			if err := os.Rename(oldSession, newSession); err != nil {
				return fmt.Errorf("failed to rename temporary session: %v", err)
			}
			utils.SecureFile(newSession)
			L().Info("Renamed temporary session", zap.String("file", filepath.Base(newSession)))
		}
	}

	client, err := h.initClientContext(ctx, tgID, newSession, factories)
	if err != nil {
		return err
	}

	if h.Web != nil {
		if err := h.registerWebRuntime(client); err != nil {
			return err
		}
	}

	L().Info("Web login client initialized without restart", zap.Int64("tg_id", client.TGID))
	return nil
}

func getTGIDFromSessionPath(path string) (int64, error) {
	base := filepath.Base(path)
	var idStr string
	switch {
	case strings.HasPrefix(base, "goroku-") && strings.HasSuffix(base, ".session"):
		idStr = strings.TrimSuffix(strings.TrimPrefix(base, "goroku-"), ".session")
	case strings.HasPrefix(base, "heroku-") && strings.HasSuffix(base, ".session"):
		idStr = strings.TrimSuffix(strings.TrimPrefix(base, "heroku-"), ".session")
	case strings.HasPrefix(base, "hikka-") && strings.HasSuffix(base, ".session"):
		idStr = strings.TrimSuffix(strings.TrimPrefix(base, "hikka-"), ".session")
	default:
		return 0, fmt.Errorf("invalid session filename format")
	}
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *Goroku) startCliLogin(ctx context.Context, factories []ModuleFactory) error {
	if h.NoAuth {
		L().Info("No active sessions and --no-auth set; skipping interactive CLI login")
		fmt.Println("No active sessions. --no-auth skips interactive login; provide a session or remove --no-auth.")
		return nil
	}

	client := NewCustomTelegramClient(0)
	client.APIID = h.APIID
	client.APIHash = h.APIHash
	client.SessionPath = filepath.Join(BaseDir, "goroku-0.session")

	if err := client.ConnectContext(ctx); err != nil {
		return fmt.Errorf("connect Telegram client: %w", err)
	}

	useQR := h.QRLogin
	if !useQR {
		fmt.Println("\033[0;96mYou can use QR-code to login from another device (your friend's phone, for example).\033[0m")
		userChoice := promptInput("\033[0;96mUse QR code? [y/N]: \033[0m")
		if err := ctx.Err(); err != nil {
			return err
		}
		useQR = strings.ToLower(userChoice) == "y"
	}

	if !useQR {
		return h.cliPhoneLogin(ctx, client, factories)
	}

	fmt.Println("\033[0;96mLoading QR code...\033[0m")
	url, err := client.QRLogin()
	if err != nil {
		return fmt.Errorf("initialize QR login: %w", err)
	}

	printQR := func(qrUrl string) {
		qr := NewQRCode()
		qr.AddData(qrUrl)
		fmt.Print("\033[2J\033[3;1f")
		qr.PrintASCII(true) // invert = true matching Python
		fmt.Println("\033[0;96mScan the QR code above to log in.\033[0m")
		fmt.Println("\033[0;96mPress Ctrl+C to cancel.\033[0m")
	}

	printQR(url)

	deadline := time.Now().Add(90 * time.Second)
	success := false
	lastRecreate := time.Now()
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := client.QRLoginStatus()
		if err != nil {
			if strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") || strings.Contains(strings.ToLower(err.Error()), "password") {
				PrintBanner("2fa.txt")
				password := promptInput("\033[0;96mEnter 2FA password: \033[0m")
				if err := client.SignIn("", "", password); err != nil {
					return fmt.Errorf("2FA login: %w", err)
				}
				success = true
				break
			}
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "canceled") || strings.Contains(errStr, "closed") || strings.Contains(errStr, "dead") {
				return fmt.Errorf("Telegram client connection closed: %w", err)
			}
		} else if status == "SUCCESS" {
			success = true
			break
		}

		if time.Since(lastRecreate) >= 15*time.Second {
			newUrl, err := client.QRLogin()
			if err == nil {
				url = newUrl
				printQR(url)
				lastRecreate = time.Now()
			}
		}

		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if !success {
		return fmt.Errorf("QR login timeout")
	}

	PrintBanner("success.txt")
	fmt.Println("\033[0;92mLogged in successfully!\033[0m")

	tgID := client.TGID
	if tgID == 0 {
		me, err := client.GetMe()
		if err == nil {
			if user, ok := me.(*tg.User); ok {
				tgID = user.ID
				client.TGID = tgID
			}
		}
	}
	if tgID == 0 {
		return fmt.Errorf("login failed: authorized Telegram user ID is 0")
	}

	return h.cliSaveClientSession(ctx, client, factories)
}

func (h *Goroku) cliPhoneLogin(ctx context.Context, client *CustomTelegramClient, factories []ModuleFactory) error {
	phone := promptInput("\033[0;96mEnter phone: \033[0m")
	if err := ctx.Err(); err != nil {
		return err
	}

	err := client.SendCodeRequest(phone)
	if err != nil {
		return fmt.Errorf("send login code: %w", err)
	}

	fmt.Println("A verification code has been sent to your Telegram app or phone.")
	code := promptInput("Enter verification code: ")
	if err := ctx.Err(); err != nil {
		return err
	}

	err = client.SignIn(phone, code, "")
	if err != nil {
		if strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") || strings.Contains(strings.ToLower(err.Error()), "password") {
			PrintBanner("2fa.txt")
			password := promptInput("\033[0;96mEnter 2FA password: \033[0m")
			if err := client.SignIn(phone, code, password); err != nil {
				return fmt.Errorf("2FA login: %w", err)
			}
		} else {
			return fmt.Errorf("login: %w", err)
		}
	}

	PrintBanner("success.txt")
	fmt.Println("\033[0;92mLogged in successfully!\033[0m")

	tgID := client.TGID
	if tgID == 0 {
		me, err := client.GetMe()
		if err == nil {
			if user, ok := me.(*tg.User); ok {
				tgID = user.ID
				client.TGID = tgID
			}
		}
	}
	if tgID == 0 {
		return fmt.Errorf("login failed: authorized Telegram user ID is 0")
	}

	return h.cliSaveClientSession(ctx, client, factories)
}

func (h *Goroku) cliSetupBot(ctx context.Context, client *CustomTelegramClient, db *Database) error {
	for {
		bot := promptInput("You can enter a custom bot username or leave it empty and Goroku will generate a random one: ")
		if err := ctx.Err(); err != nil {
			return err
		}
		if bot == "" {
			return nil
		}
		bot = strings.TrimSpace(bot)
		bot = strings.TrimPrefix(bot, "@")

		invalid := false
		for _, ch := range bot {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
				invalid = true
				break
			}
		}
		if invalid {
			fmt.Println("Invalid username: use only ASCII letters, digits and underscore (_).")
			continue
		}
		if !strings.HasSuffix(strings.ToLower(bot), "bot") {
			fmt.Println("Invalid username: must end with 'bot'.")
			continue
		}

		fmt.Println("Checking bot username...")
		owned, err := client.CheckBot(bot)
		if err == nil && owned {
			if err := db.Set("goroku.inline", "custom_bot", bot); err != nil {
				L().Error("Failed to persist bot username", zap.String("username", bot), zap.Error(err))
				fmt.Println("Could not save bot username. Try again or leave it empty.")
				continue
			}
			fmt.Println("Bot username saved!")
			break
		} else {
			fmt.Println("Bot username is occupied. Try again or leave it empty")
		}
	}
	return nil
}

func (h *Goroku) cliSaveClientSession(ctx context.Context, client *CustomTelegramClient, factories []ModuleFactory) error {
	tgID := client.TGID
	if tgID == 0 {
		return fmt.Errorf("login failed: authorized Telegram user ID is 0")
	}

	if err := client.Disconnect(); err != nil { // waits for client.Run to finish via runDone
		L().Warn("Failed to disconnect temporary client", zap.Error(err))
	}

	oldSession := filepath.Join(BaseDir, "goroku-0.session")
	newSession := filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", tgID))
	if err := os.Rename(oldSession, newSession); err != nil {
		L().Warn("Failed to rename temporary session", zap.String("source", oldSession), zap.String("destination", newSession), zap.Error(err))
	}
	utils.SecureFile(newSession)

	L().Info("Booting userbot", zap.Int64("tg_id", tgID))
	if _, err := h.initClientContext(ctx, tgID, newSession, factories); err != nil {
		return fmt.Errorf("initialize client %d: %w", tgID, err)
	}

	return h.cliSetupBot(ctx, h.Clients[len(h.Clients)-1], h.DBs[len(h.DBs)-1])
}

func promptInput(prompt string) string {
	// A terminal read cannot be canceled portably without closing shared stdin.
	// Production startup runs in the app-owned startup worker, so Run can honor
	// its shutdown deadline even if an interactive reader never returns.
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}
