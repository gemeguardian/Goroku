package goroku

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goroku/goroku/utils"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

func (h *Goroku) finishWebLogin(pending interface{}, customModules []Module) error {
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

	for _, existing := range h.Clients {
		if existing.TGID == tgID {
			return fmt.Errorf("client %d is already running", tgID)
		}
	}

	_ = pendingClient.Disconnect() // waits for client.Run to finish via runDone

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

	client, err := h.initClient(tgID, newSession, customModules)
	if err != nil {
		return err
	}

	if h.Web != nil {
		loader := h.Loaders[len(h.Loaders)-1]
		db := h.DBs[len(h.DBs)-1]
		h.Web.AddLoader(client, loader, db)
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

func (h *Goroku) startCliLogin(customModules []Module) {
	client := NewCustomTelegramClient(0)
	client.APIID = h.APIID
	client.APIHash = h.APIHash
	client.SessionPath = filepath.Join(BaseDir, "goroku-0.session")

	if err := client.Connect(); err != nil {
		L().Fatal("Failed to connect Telegram client", zap.Error(err))
	}

	fmt.Println("\033[0;96mYou can use QR-code to login from another device (your friend's phone, for example).\033[0m")
	userChoice := promptInput("\033[0;96mUse QR code? [y/N]: \033[0m")

	if strings.ToLower(userChoice) != "y" {
		h.cliPhoneLogin(client, customModules)
		return
	}

	fmt.Println("\033[0;96mLoading QR code...\033[0m")
	url, err := client.QRLogin()
	if err != nil {
		L().Fatal("QR login init failed", zap.Error(err))
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
		status, err := client.QRLoginStatus()
		if err != nil {
			if strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") || strings.Contains(strings.ToLower(err.Error()), "password") {
				PrintBanner("2fa.txt")
				password := promptInput("\033[0;96mEnter 2FA password: \033[0m")
				if err := client.SignIn("", "", password); err != nil {
					L().Fatal("2FA Login failed", zap.Error(err))
				}
				success = true
				break
			}
			errStr := strings.ToLower(err.Error())
			if strings.Contains(errStr, "canceled") || strings.Contains(errStr, "closed") || strings.Contains(errStr, "dead") {
				L().Fatal("Telegram client connection closed", zap.Error(err))
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

		time.Sleep(2 * time.Second)
	}

	if !success {
		L().Fatal("QR login timeout")
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
		L().Fatal("Login failed: authorized Telegram user ID is 0")
	}

	h.cliSaveClientSession(client, customModules)
}

func (h *Goroku) cliPhoneLogin(client *CustomTelegramClient, customModules []Module) {
	phone := promptInput("\033[0;96mEnter phone: \033[0m")

	err := client.SendCodeRequest(phone)
	if err != nil {
		L().Fatal("Failed to send code", zap.Error(err))
	}

	fmt.Println("A verification code has been sent to your Telegram app or phone.")
	code := promptInput("Enter verification code: ")

	err = client.SignIn(phone, code, "")
	if err != nil {
		if strings.Contains(err.Error(), "SESSION_PASSWORD_NEEDED") || strings.Contains(strings.ToLower(err.Error()), "password") {
			PrintBanner("2fa.txt")
			password := promptInput("\033[0;96mEnter 2FA password: \033[0m")
			if err := client.SignIn(phone, code, password); err != nil {
				L().Fatal("2FA Login failed", zap.Error(err))
			}
		} else {
			L().Fatal("Login failed", zap.Error(err))
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
		L().Fatal("Login failed: authorized Telegram user ID is 0")
	}

	h.cliSaveClientSession(client, customModules)
}

func (h *Goroku) cliSetupBot(client *CustomTelegramClient, db *Database) {
	for {
		bot := promptInput("You can enter a custom bot username or leave it empty and Goroku will generate a random one: ")
		if bot == "" {
			break
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
			db.Set("goroku.inline", "custom_bot", bot)
			fmt.Println("Bot username saved!")
			break
		} else {
			fmt.Println("Bot username is occupied. Try again or leave it empty")
		}
	}
}

func (h *Goroku) cliSaveClientSession(client *CustomTelegramClient, customModules []Module) {
	tgID := client.TGID
	if tgID == 0 {
		L().Fatal("Login failed: authorized Telegram user ID is 0")
	}

	_ = client.Disconnect() // waits for client.Run to finish via runDone

	oldSession := filepath.Join(BaseDir, "goroku-0.session")
	newSession := filepath.Join(BaseDir, fmt.Sprintf("goroku-%d.session", tgID))
	_ = os.Rename(oldSession, newSession)
	utils.SecureFile(newSession)

	L().Info("Booting userbot", zap.Int64("tg_id", tgID))
	if _, err := h.initClient(tgID, newSession, customModules); err != nil {
		L().Fatal("Failed to init client", zap.Int64("tg_id", tgID), zap.Error(err))
	}

	h.cliSetupBot(h.Clients[len(h.Clients)-1], h.DBs[len(h.DBs)-1])
}

func promptInput(prompt string) string {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}
