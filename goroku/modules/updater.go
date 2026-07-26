package modules

import (
	"encoding/json"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type Updater struct {
	goroku.Base
	notified string
	restart  func()
	// Configs
	gitOriginUrl         string
	disableNotifications bool
	autoupdate           bool
}

func (m *Updater) Name() string {
	return "Updater"
}

func (m *Updater) Strings() map[string]string {
	return map[string]string{
		"name":                       "Updater Module",
		"_cfg_GIT_ORIGIN_URL":        "Git origin URL, for where to update from",
		"_cfg_disable_notifications": "Disable update notifications",
		"_cfg_autoupdate":            "Automatic updates for your Goroku",
	}
}

func (m *Updater) ClientReady() error {
	pollLoop := goroku.NewInfiniteLoop(m.pollerTick, 60*time.Second, m.Name(), true)
	announcementLoop := goroku.NewInfiniteLoop(m.announcementTick, 60*time.Second, m.Name(), true)

	if loader := m.Client.Loader; loader != nil {
		loader.RegisterLoop(pollLoop)
		loader.RegisterLoop(announcementLoop)
	}

	m.handlePostRestart()

	if !m.DB.GetBool("Updater", "do_not_create", false) {
		go func() {
			if err := m.Client.CreateGorokuFolder(m.Client.TGIDValue()); err != nil {
				return
			}
			if err := m.DB.SetBool("Updater", "do_not_create", true); err != nil {
				m.logBackgroundWrite("set", "do_not_create", err)
			}
		}()
	}
	return nil
}

var _ goroku.ModuleWithConfigSchema = (*Updater)(nil)

// ConfigSchema is the M7 typed config surface for Updater.
func (m *Updater) ConfigSchema() []goroku.ConfigField {
	return []goroku.ConfigField{
		{Key: "GIT_ORIGIN_URL", Type: "string", Default: "https://github.com/gemeguardian/Goroku", Validator: &goroku.StringValidator{}},
		{Key: "disable_notifications", Type: "bool", Default: false, Validator: &goroku.BooleanValidator{}},
		{Key: "autoupdate", Type: "bool", Default: false, Validator: &goroku.BooleanValidator{}},
	}
}

func (m *Updater) ConfigReady(config map[string]any) error {
	if val, ok := config["disable_notifications"].(bool); ok {
		m.disableNotifications = val
	}
	if val, ok := config["autoupdate"].(bool); ok {
		m.autoupdate = val
	}
	if val, ok := config["GIT_ORIGIN_URL"].(string); ok && val != "" {
		if val != m.gitOriginUrl {
			m.gitOriginUrl = val
			repoDir := m.getRepoDir()
			cmd := exec.Command("git", "remote", "set-url", "origin", val) //nolint:gosec
			cmd.Dir = repoDir
			_ = cmd.Run()
		}
	}
	return nil
}

func (m *Updater) OnDlmod() error { return nil }

func (m *Updater) Commands() map[string]goroku.CommandHandler {
	return map[string]goroku.CommandHandler{
		"update":     m.UpdateCmd,
		"restart":    m.RestartCmd,
		"changelog":  m.ChangelogCmd,
		"autoupdate": m.AutoupdateCmd,
		"source":     m.SourceCmd,
		"rollback":   m.RollbackCmd,
		"ubstop":     m.UbstopCmd,
	}
}

func (m *Updater) getRepoDir() string {
	execPath, err := os.Executable()
	if err != nil {
		return "."
	}
	repoDir := filepath.Dir(execPath)
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		repoDir = filepath.Dir(repoDir)
	}
	return repoDir
}

func (m *Updater) noGit() bool {
	return os.Getenv("GOROKU_NO_GIT") == "1"
}

func (m *Updater) gitCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Dir = m.getRepoDir()
	return cmd
}

func (m *Updater) gitOutput(args ...string) (string, error) {
	out, err := m.gitCommand(args...).Output()
	return strings.TrimSpace(string(out)), err
}

func (m *Updater) gitCombinedOutput(args ...string) (string, error) {
	out, err := m.gitCommand(args...).CombinedOutput()
	return string(out), err
}

func scheduleGorokuRestart() {
	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()
}

func (m *Updater) scheduleRestart() {
	if m.restart != nil {
		m.restart()
		return
	}
	scheduleGorokuRestart()
}

func (m *Updater) logBackgroundWrite(operation, key string, err error) {
	goroku.L().Error("background database write failed",
		zap.String("operation", operation),
		zap.String("owner", "Updater"),
		zap.String("key", key),
		zap.Error(err),
	)
}

func (m *Updater) persistRestartMetadata(message string, timestamp int64, moduleCount int, secureBoot bool) error {
	return m.DB.Update(map[string]map[string]any{
		"Updater": {"selfupdatemsg": message, "restart_ts": timestamp, "modules_count": moduleCount, "secure_boot": secureBoot},
		"Loader":  {"secure_boot": secureBoot},
	})
}

func (m *Updater) prepareRestart(message string, secureBoot ...bool) error {
	secure := len(secureBoot) > 0 && secureBoot[0]
	moduleCount := 0
	if m.Client != nil {
		if loader := m.Client.Loader; loader != nil {
			moduleCount = len(loader.GetModules())
		}
	}
	if err := m.persistRestartMetadata(message, time.Now().Unix(), moduleCount, secure); err != nil {
		return err
	}
	m.scheduleRestart()
	return nil
}

func (m *Updater) getLatestHash() string {
	if m.noGit() {
		return ""
	}
	_ = m.gitCommand("fetch", "--quiet").Run()

	branch := goroku.GetVersionBranch()
	out, err := m.gitOutput("rev-parse", "origin/"+branch)
	if err != nil {
		out, err = m.gitOutput("rev-parse", "HEAD")
		if err != nil {
			return ""
		}
	}
	return out
}

func (m *Updater) getCurrentHash() string {
	if m.noGit() {
		return ""
	}
	out, err := m.gitOutput("rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

func (m *Updater) getChangelog() string {
	if m.noGit() {
		return ""
	}
	branch := goroku.GetVersionBranch()
	out, err := m.gitOutput("log", "HEAD..origin/"+branch, "--oneline", "--format=<b>%h</b>: <i>%s</i>", "-10")
	if err != nil || out == "" {
		return ""
	}
	return out
}

func (m *Updater) pollerTick() error {
	if m.noGit() {
		return nil
	}
	if utils.IsWrongUpstreamOrigin() {
		return nil
	}

	if m.disableNotifications && !m.autoupdate {
		return nil
	}

	changelog := m.getChangelog()
	if changelog == "" {
		return nil
	}

	latest := m.getLatestHash()
	current := m.getCurrentHash()

	if latest == "" || latest == current {
		return nil
	}

	ignorePermanent := m.DB.GetString("Updater", "ignore_permanent", "")
	if ignorePermanent != "" && ignorePermanent == latest {
		return nil
	}

	if latest == m.notified {
		return nil
	}

	if m.autoupdate {
		if out, err := m.gitCombinedOutput("pull"); err == nil && !strings.Contains(out, "Already up to date") {
			if err := m.prepareRestart(""); err != nil {
				m.logBackgroundWrite("update", "selfupdatemsg,restart_ts", err)
				return err
			}
			_, _ = m.Client.SendMessage(goroku.ChatRefID(m.Client.TGIDValue()),
				fmt.Sprintf("🔄 <b>Auto-updated to</b> <code>%s</code>\n\n%s", latest[:6], changelog))
			m.notified = latest
		}
		return nil
	}

	if err := m.DB.SetString("Updater", "ignore_permanent", ""); err != nil {
		m.logBackgroundWrite("set", "ignore_permanent", err)
		return err
	}
	_, _ = m.Client.SendMessage(goroku.ChatRefID(m.Client.TGIDValue()),
		fmt.Sprintf(
			"🪐 <b>Goroku update available!</b>\n\n"+
				"📌 <b>Current:</b> <code>%s</code>\n"+
				"🆕 <b>Latest:</b> <code>%s</code>\n\n"+
				"<b>Changelog:</b>\n%s\n\n"+
				"Run <code>.update -f</code> to update now.",
			current[:6], latest[:6], changelog,
		),
	)
	m.notified = latest
	return nil
}

func (m *Updater) announcementTick() error {
	url := "https://api.github.com/repos/gemeguardian/Goroku/contents/goroku/assets/announcment.txt"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3.raw")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	announcement := strings.TrimSpace(string(body))
	previous := m.DB.GetString("Updater", "announcement", "")

	if announcement != "" && announcement != previous {
		_, _ = m.Client.SendMessage(goroku.ChatRefID(m.Client.TGIDValue()),
			fmt.Sprintf("📢 <b>Goroku Announcement:</b>\n\n%s", announcement))
		if err := m.DB.SetString("Updater", "announcement", announcement); err != nil {
			m.logBackgroundWrite("set", "announcement", err)
			return err
		}
	}
	return nil
}

func (m *Updater) handlePostRestart() {
	selfUpdateMsg := m.DB.GetString("Updater", "selfupdatemsg", "")
	if selfUpdateMsg == "" {
		return
	}

	startTS := m.DB.GetInt64("Updater", "restart_ts", 0)
	var took string
	if startTS != 0 {
		took = fmt.Sprintf("%d", time.Now().Unix()-startTS)
	} else {
		took = "n/a"
	}

	platform := "Goroku"
	me, err := m.Client.GetMe()
	if err == nil {
		if tgUser, ok := me.(*tg.User); ok {
			if tgUser.Premium {
				platform = utils.GetPlatformEmoji()
			}
		}
	}
	if loader := m.Client.Loader; loader != nil {
		if loaderModule, ok := loader.LookupByName("Loader").(*LoaderModule); ok && !loaderModule.RestoreComplete() {
			pendingTpl := m.T("success", "✅ <b>Restart successful! {}</b>\n<i>But still loading modules...</i>\n<i>Restart took {}s</i>")
			_ = m.editRestartMessage(selfUpdateMsg, formatTrans(pendingTpl, platform, took))
			go func() {
				<-loaderModule.WaitForRestore()
				m.handlePostRestart()
			}()
			return
		}
	}
	expectedModules := m.DB.GetInt("Updater", "modules_count", 0)
	actualModules := 0
	if loader := m.Client.Loader; loader != nil {
		actualModules = len(loader.GetModules())
	}
	if expectedModules == 0 {
		expectedModules = actualModules
	}
	msg := ""
	loaderFullyLoaded := true
	if loader := m.Client.Loader; loader != nil {
		if loaderModule, ok := loader.LookupByName("Loader").(*LoaderModule); ok {
			loaderFullyLoaded = loaderModule.FullyLoaded()
		}
	}
	secureBoot := m.DB.GetBool("Updater", "secure_boot", false)
	if secureBoot {
		secureTpl := m.T("secure_boot_complete", "🛡 <b>Secure Boot complete! {}</b>\n<i>User modules were skipped. Restart took {}s</i>")
		msg = formatTrans(secureTpl, platform, took)
	} else if actualModules >= expectedModules && loaderFullyLoaded {
		successTpl := m.T("full_success", "✅ <b>Userbot is fully loaded! {}</b>\n<i>Full restart took {}s</i>")
		msg = formatTrans(successTpl, platform, took)
	} else {
		failedModules := expectedModules - actualModules
		if failedModules < 1 {
			failedModules = 1
		}
		failureTpl := m.T("full_fail", "❌ <b>Userbot loaded with errors! {}</b>\n<i>Restart took {}s\nFailed to load {} modules</i>")
		msg = formatTrans(failureTpl, platform, took, strconv.Itoa(failedModules))
	}
	if !m.editRestartMessage(selfUpdateMsg, msg) {
		go func() {
			time.Sleep(2 * time.Second)
			m.handlePostRestart()
		}()
		return
	}
	if err := m.DB.Update(map[string]map[string]any{"Updater": {"selfupdatemsg": "", "restart_ts": int64(0), "secure_boot": false}}); err != nil {
		m.logBackgroundWrite("update", "selfupdatemsg,restart_ts", err)
		return
	}

}

func (m *Updater) editRestartMessage(reference, text string) bool {
	if strings.HasPrefix(reference, "inline:") {
		if m.Client.GorokuInline == nil || !m.Client.GorokuInline.IsComplete() {
			return false
		}
		edit := tgbotapi.EditMessageTextConfig{
			BaseEdit:  tgbotapi.BaseEdit{InlineMessageID: strings.TrimPrefix(reference, "inline:")},
			Text:      text,
			ParseMode: tgbotapi.ModeHTML,
		}
		_, err := m.Client.GorokuInline.GetBotAPI().Request(edit)
		return err == nil
	}
	if strings.HasPrefix(reference, "bot:") {
		parts := strings.SplitN(reference, ":", 3)
		if len(parts) != 3 || m.Client.GorokuInline == nil || !m.Client.GorokuInline.IsComplete() {
			return false
		}
		chatID, err1 := strconv.ParseInt(parts[1], 10, 64)
		msgID, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil {
			return false
		}
		edit := tgbotapi.EditMessageTextConfig{
			BaseEdit:  tgbotapi.BaseEdit{BaseChatMessage: tgbotapi.BaseChatMessage{ChatConfig: tgbotapi.ChatConfig{ChatID: chatID}, MessageID: msgID}},
			Text:      text,
			ParseMode: tgbotapi.ModeHTML,
		}
		_, err := m.Client.GorokuInline.GetBotAPI().Request(edit)
		return err == nil
	}
	if !strings.Contains(reference, ":") {
		return false
	}
	parts := strings.SplitN(reference, ":", 2)
	chatID, err1 := strconv.ParseInt(parts[0], 10, 64)
	msgID, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 == nil && err2 == nil {
		_, err := m.Client.EditMessage(goroku.ChatRefID(chatID), msgID, text)
		return err == nil
	}
	return false
}

func (m *Updater) UpdateCmd(msg *goroku.Message) error {
	if m.noGit() {
		return msg.Answer("<b>Git disabled via --no-git.</b>")
	}

	argsRaw := utils.GetArgsRaw(msg.Text)
	force := strings.Contains(argsRaw, "-f")

	changelog := m.getChangelog()
	if changelog == "" && !force {
		return msg.Answer(m.T("no_update", "🌟 <b>You are on the latest version!</b>"))
	}

	_ = msg.Answer(m.T("downloading", "<tg-emoji emoji-id=5208622108191506906>🕗</tg-emoji> <b>Downloading updates...</b>"))

	repoDir := m.getRepoDir()

	backupData := m.DB.GetAll()
	if backupBytes, err := json.MarshalIndent(backupData, "", "  "); err == nil {
		backupPath := filepath.Join(repoDir, fmt.Sprintf("db_backup_%d.json", time.Now().Unix()))
		if writeErr := os.WriteFile(backupPath, backupBytes, 0600); writeErr == nil {
			_ = msg.Answer(fmt.Sprintf("💾 <b>DB backup created:</b> <code>%s</code>", filepath.Base(backupPath)))
			time.Sleep(500 * time.Millisecond)
		}
	}

	output, err := m.gitCombinedOutput("pull")
	if err != nil {
		_ = msg.Answer(fmt.Sprintf("<tg-emoji emoji-id=5210952531676504517>❌</tg-emoji> <b>Update failed:</b>\n<pre>%s</pre>", output))
		return nil
	}

	if strings.Contains(output, "Already up to date") || strings.Contains(output, "Уже обновлено") {
		_ = msg.Answer(m.T("no_update", "<tg-emoji emoji-id=5465496001856950230>🌟</tg-emoji> <b>You are on the latest version!</b>"))
		return nil
	}

	_ = msg.Answer(m.T("installing", "<tg-emoji emoji-id=5208622108191506906>🕗</tg-emoji> <b>Installing updates...</b>"))

	if err := m.prepareRestart(fmt.Sprintf("%d:%d", msg.ChatID, msg.ID)); err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Update installed, but restart state could not be saved:</b> %v", err))
	}

	return nil
}

func (m *Updater) RestartCmd(msg *goroku.Message) error {
	force, secureBoot := restartOptions(utils.GetArgsRaw(msg.RawText))
	if force || m.Client.GorokuInline == nil || !m.Client.GorokuInline.IsComplete() {
		return m.restartNow(msg, secureBoot)
	}

	confirmText := m.T("restart_confirm", "<tg-emoji emoji-id=5382187118216879236>❓</tg-emoji> <b>Are you sure you want to restart?</b>")
	if secureBoot {
		confirmText = m.T("secure_boot_confirm", "<tg-emoji emoji-id=5382187118216879236>❓</tg-emoji> <b>Are you sure you want to restart in secure boot mode?</b>")
	}
	markup := [][]inline.Button{{
		{
			Text:  m.T("btn_restart", "🔄 Restart"),
			Style: "primary",
			Handler: func(c inline.CallbackQuery) error {
				if !requireOwnerCallback(m.Client, c, c.FromID) {
					return nil
				}
				text, err := m.restartCaption(secureBoot)
				if err != nil {
					return err
				}
				restartMessage := fmt.Sprintf("bot:%d:%d", c.ChatID, c.MessageID)
				if c.InlineMessage != nil && c.InlineMessage.InlineMessageID != "" {
					restartMessage = "inline:" + c.InlineMessage.InlineMessageID
				}
				if err := m.prepareRestart(restartMessage, secureBoot); err != nil {
					return err
				}
				_ = c.Answer("Restarting...", false)
				return c.Edit(text, tgbotapi.InlineKeyboardMarkup{})
			},
		},
		{
			Text:    m.T("cancel", "🚫 Cancel"),
			Style:   "danger",
			Handler: func(c inline.CallbackQuery) error { return closeForm(c) },
		},
	}}
	_, err := m.Client.GorokuInline.Form(confirmText, msg, markup,
		inline.WithForceMe(true),
		inline.WithStartText("🍃"),
	)
	return err
}

func restartOptions(args string) (force, secureBoot bool) {
	for _, arg := range strings.Fields(args) {
		switch arg {
		case "-f", "--force":
			force = true
		case "-sb", "--secure-boot":
			secureBoot = true
		}
	}
	return force, secureBoot
}

func (m *Updater) restartNow(msg *goroku.Message, secureBoot bool) error {
	text, err := m.restartCaption(secureBoot)
	if err != nil {
		return err
	}
	if err := m.prepareRestart(fmt.Sprintf("%d:%d", msg.ChatID, msg.ID), secureBoot); err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Restart state could not be saved:</b> %v", err))
	}
	return msg.Answer(text)
}

func (m *Updater) restartCaption(secureBoot bool) (string, error) {
	platform := "Goroku"
	me, err := m.Client.GetMe()
	if err == nil {
		if tgUser, ok := me.(*tg.User); ok {
			if tgUser.Premium {
				platform = utils.GetPlatformEmoji()
			}
		}
	}
	if secureBoot {
		return fmt.Sprintf("🛡 <b>Your %s is starting in Secure Boot...</b>\n<i>User modules will be skipped.</i>", platform), nil
	}

	template := m.T("restarting_caption", "<tg-emoji emoji-id=5208622108191506906>🕗</tg-emoji> <b>Your {} is restarting...</b>")
	return strings.ReplaceAll(template, "{}", platform), nil
}

func (m *Updater) ChangelogCmd(msg *goroku.Message) error {
	repoDir := m.getRepoDir()
	changelogPath := filepath.Join(repoDir, "CHANGELOG.md")

	content, err := os.ReadFile(changelogPath) //nolint:gosec
	if err != nil {
		output, gitErr := m.gitOutput("log", "--oneline", "-15", "--pretty=format:%h: %s")
		if gitErr != nil {
			_ = msg.Answer("⚠️ <b>No CHANGELOG.md found and git log failed</b>")
			return nil
		}
		_ = msg.Answer("📋 <b>Recent commits:</b>\n<pre>" + output + "</pre>")
		return nil
	}

	sections := strings.Split(string(content), "##")
	var changelog string
	if len(sections) > 1 {
		changelog = strings.TrimSpace(sections[1])
	} else {
		changelog = strings.TrimSpace(string(content))
	}

	if len(changelog) > 3500 {
		changelog = changelog[:3500] + "\n...<i>(truncated)</i>"
	}

	_ = msg.Answer(fmt.Sprintf("📋 <b>Changelog:</b>\n\n%s", changelog))
	return nil
}

func (m *Updater) AutoupdateCmd(msg *goroku.Message) error {
	current := m.autoupdate
	newState := !current

	if err := m.DB.SetBool("Updater", "autoupdate", newState); err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Failed to save auto-update setting:</b> %v", err))
	}
	m.autoupdate = newState

	if newState {
		_ = msg.Answer("✅ <b>Auto-update enabled.</b> Bot will update automatically when new versions are available.")
	} else {
		_ = msg.Answer("🚫 <b>Auto-update disabled.</b> You will be notified about updates but won't auto-install them.")
	}
	return nil
}

func (m *Updater) SourceCmd(msg *goroku.Message) error {
	url, err := m.gitOutput("remote", "get-url", "origin")
	if err != nil {
		_ = msg.Answer("⚠️ <b>Could not determine source URL</b>")
		return nil
	}
	sourceTpl := m.T("source", "📦 <b>Source:</b> <a href=\"{}\">{}</a>")
	text := formatTrans(sourceTpl, url, url)
	_ = msg.Answer(text)
	return nil
}

func (m *Updater) RollbackCmd(msg *goroku.Message) error {
	args := strings.TrimSpace(strings.Join(strings.Fields(msg.Text)[1:], " "))

	n := 1
	forceFlag := false
	for _, part := range strings.Fields(args) {
		if part == "-f" {
			forceFlag = true
			continue
		}
		if num, err := strconv.Atoi(part); err == nil {
			if num < 1 || num > 10 {
				return msg.Answer("⚠️ <b>Rollback range must be between 1 and 10</b>")
			}
			n = num
		}
	}

	if !forceFlag {
		return msg.Answer(fmt.Sprintf(
			"⚠️ <b>This will revert %d commit(s)!</b>\nTo confirm: <code>.rollback %d -f</code>",
			n, n,
		))
	}

	_ = msg.Answer(fmt.Sprintf("🔄 <b>Rolling back %d commit(s)...</b>", n))

	out, err := m.gitCombinedOutput("reset", "--hard", fmt.Sprintf("HEAD~%d", n))
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Rollback failed:</b>\n<pre>%s</pre>", out))
	}

	_ = msg.Answer("✅ <b>Rollback successful! Restarting...</b>")
	scheduleGorokuRestart()
	return nil
}

func (m *Updater) UbstopCmd(msg *goroku.Message) error {
	if err := m.DB.SetBool("Updater", "autoupdate", false); err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Failed to disable auto-update:</b> %v", err))
	}
	m.autoupdate = false
	platform := "userbot"
	me, err := m.Client.GetMe()
	if err == nil {
		if tgUser, ok := me.(*tg.User); ok {
			if tgUser.Premium {
				platform = utils.GetPlatformEmoji()
			}
		}
	}
	template := m.T("ub_stop", "Your {emoji} stopped!")
	text := strings.ReplaceAll(template, "{emoji}", platform)
	_ = msg.Answer(text)
	go func() {
		time.Sleep(1 * time.Second)
		goroku.Die()
	}()
	return nil
}
