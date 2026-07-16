package modules

import (
	"encoding/json"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/utils"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type Updater struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
	notified   string
	restart    func()

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

func (m *Updater) Init(client *goroku.CustomTelegramClient, db *goroku.Database) error {
	m.client = client
	m.db = db
	m.translator = goroku.NewTranslator(client, db)
	m.translator.Init()
	return nil
}

func (m *Updater) ClientReady() error {
	pollLoop := goroku.NewInfiniteLoop(m.pollerTick, 60*time.Second, m.Name(), true)
	announcementLoop := goroku.NewInfiniteLoop(m.announcementTick, 60*time.Second, m.Name(), true)

	if loader := m.client.Loader; loader != nil {
		loader.RegisterLoop(pollLoop)
		loader.RegisterLoop(announcementLoop)
	}

	m.handlePostRestart()

	if !m.db.GetBool("Updater", "do_not_create", false) {
		go func() {
			if err := m.client.CreateGorokuFolder(m.client.TGID); err != nil {
				return
			}
			if err := m.db.SetBool("Updater", "do_not_create", true); err != nil {
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

func (m *Updater) OnUnload() error { return nil }
func (m *Updater) OnDlmod() error  { return nil }

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

func (m *Updater) Watchers() []goroku.WatcherHandler {
	return []goroku.WatcherHandler{}
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

func (m *Updater) persistRestartMetadata(message string, timestamp int64) error {
	return m.db.Update(map[string]map[string]any{
		"Updater": {"selfupdatemsg": message, "restart_ts": timestamp},
	})
}

func (m *Updater) prepareRestart(message string) error {
	if err := m.persistRestartMetadata(message, time.Now().Unix()); err != nil {
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

func (m *Updater) getTrans(key, def string) string {
	if m.translator == nil {
		return def
	}
	searchKey := fmt.Sprintf("goroku.modules.%s.%s", m.Name(), key)
	if val := m.translator.GetKey(searchKey); val != nil {
		return fmt.Sprintf("%v", val)
	}
	searchKeyLower := fmt.Sprintf("goroku.modules.%s.%s", strings.ToLower(m.Name()), key)
	if val := m.translator.GetKey(searchKeyLower); val != nil {
		return fmt.Sprintf("%v", val)
	}
	return def
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

	ignorePermanent := m.db.GetString("Updater", "ignore_permanent", "")
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
			_, _ = m.client.SendMessage(goroku.ChatRefID(m.client.TGID),
				fmt.Sprintf("🔄 <b>Auto-updated to</b> <code>%s</code>\n\n%s", latest[:6], changelog))
			m.notified = latest
		}
		return nil
	}

	if err := m.db.SetString("Updater", "ignore_permanent", ""); err != nil {
		m.logBackgroundWrite("set", "ignore_permanent", err)
		return err
	}
	_, _ = m.client.SendMessage(goroku.ChatRefID(m.client.TGID),
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
	previous := m.db.GetString("Updater", "announcement", "")

	if announcement != "" && announcement != previous {
		_, _ = m.client.SendMessage(goroku.ChatRefID(m.client.TGID),
			fmt.Sprintf("📢 <b>Goroku Announcement:</b>\n\n%s", announcement))
		if err := m.db.SetString("Updater", "announcement", announcement); err != nil {
			m.logBackgroundWrite("set", "announcement", err)
			return err
		}
	}
	return nil
}

func (m *Updater) handlePostRestart() {
	selfUpdateMsg := m.db.GetString("Updater", "selfupdatemsg", "")
	if selfUpdateMsg == "" {
		return
	}

	startTS := m.db.GetInt64("Updater", "restart_ts", 0)
	var took string
	if startTS != 0 {
		took = fmt.Sprintf("%d", time.Now().Unix()-startTS)
	} else {
		took = "n/a"
	}

	platform := "Goroku"
	me, err := m.client.GetMe()
	if err == nil {
		if tgUser, ok := me.(*tg.User); ok {
			if tgUser.Premium {
				platform = utils.GetPlatformEmoji()
			}
		}
	}
	successTpl := m.getTrans("success", "✅ <b>Restart complete! {}</b> Took <b>{}</b>s")
	msg := formatTrans(successTpl, platform, took)
	if err := m.persistRestartMetadata("", 0); err != nil {
		m.logBackgroundWrite("update", "selfupdatemsg,restart_ts", err)
		return
	}

	if strings.Contains(selfUpdateMsg, ":") {
		parts := strings.SplitN(selfUpdateMsg, ":", 2)
		chatID, err1 := strconv.ParseInt(parts[0], 10, 64)
		msgID, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 == nil && err2 == nil {
			_, _ = m.client.EditMessage(goroku.ChatRefID(chatID), msgID, msg)
		}
	}

}

func (m *Updater) UpdateCmd(msg *goroku.Message) error {
	if m.noGit() {
		return msg.Answer("<b>Git disabled via --no-git.</b>")
	}

	argsRaw := utils.GetArgsRaw(msg.Text)
	force := strings.Contains(argsRaw, "-f")

	changelog := m.getChangelog()
	if changelog == "" && !force {
		return msg.Answer(m.getTrans("no_update", "🌟 <b>You are on the latest version!</b>"))
	}

	_ = msg.Answer(m.getTrans("downloading", "<tg-emoji emoji-id=5208622108191506906>🕗</tg-emoji> <b>Downloading updates...</b>"))

	repoDir := m.getRepoDir()

	backupData := m.db.GetAll()
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
		_ = msg.Answer(m.getTrans("no_update", "<tg-emoji emoji-id=5465496001856950230>🌟</tg-emoji> <b>You are on the latest version!</b>"))
		return nil
	}

	_ = msg.Answer(m.getTrans("installing", "<tg-emoji emoji-id=5208622108191506906>🕗</tg-emoji> <b>Installing updates...</b>"))

	if err := m.prepareRestart(fmt.Sprintf("%d:%d", msg.ChatID, msg.ID)); err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Update installed, but restart state could not be saved:</b> %v", err))
	}

	return nil
}

func (m *Updater) RestartCmd(msg *goroku.Message) error {
	platform := "Goroku"
	me, err := m.client.GetMe()
	if err == nil {
		if tgUser, ok := me.(*tg.User); ok {
			if tgUser.Premium {
				platform = utils.GetPlatformEmoji()
			}
		}
	}

	template := m.getTrans("restarting_caption", "<tg-emoji emoji-id=5208622108191506906>🕗</tg-emoji> <b>Your {} is restarting...</b>")
	text := strings.ReplaceAll(template, "{}", platform)

	if err := m.prepareRestart(fmt.Sprintf("%d:%d", msg.ChatID, msg.ID)); err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Restart state could not be saved:</b> %v", err))
	}
	return msg.Answer(text)
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

	if err := m.db.SetBool("Updater", "autoupdate", newState); err != nil {
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
	sourceTpl := m.getTrans("source", "📦 <b>Source:</b> <a href=\"{}\">{}</a>")
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
	if err := m.db.SetBool("Updater", "autoupdate", false); err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Failed to disable auto-update:</b> %v", err))
	}
	m.autoupdate = false
	platform := "userbot"
	me, err := m.client.GetMe()
	if err == nil {
		if tgUser, ok := me.(*tg.User); ok {
			if tgUser.Premium {
				platform = utils.GetPlatformEmoji()
			}
		}
	}
	template := m.getTrans("ub_stop", "Your {emoji} stopped!")
	text := strings.ReplaceAll(template, "{emoji}", platform)
	_ = msg.Answer(text)
	go func() {
		time.Sleep(1 * time.Second)
		goroku.Die()
	}()
	return nil
}
