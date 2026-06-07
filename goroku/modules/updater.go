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
)

type Updater struct {
	client     *goroku.CustomTelegramClient
	db         *goroku.Database
	translator *goroku.Translator
	notified   string

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

	if loader, ok := m.client.Loader.(*goroku.Modules); ok {
		loader.RegisterLoop(pollLoop)
		loader.RegisterLoop(announcementLoop)
	}

	m.handlePostRestart()

	doNotCreate, _ := m.db.Get("Updater", "do_not_create", false).(bool)
	if !doNotCreate {
		go func() {
			_ = m.client.CreateGorokuFolder(m.client.TGID)
			m.db.Set("Updater", "do_not_create", true)
		}()
	}
	return nil
}

func (m *Updater) ConfigDefaults() map[string]interface{} {
	return map[string]interface{}{
		"GIT_ORIGIN_URL":        "https://github.com/gemeguardian/Goroku",
		"disable_notifications": false,
		"autoupdate":            false,
	}
}

func (m *Updater) ConfigReady(config map[string]interface{}) error {
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
			cmd := exec.Command("git", "remote", "set-url", "origin", val)
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

func (m *Updater) getLatestHash() string {
	if m.noGit() {
		return ""
	}
	repoDir := m.getRepoDir()
	cmd := exec.Command("git", "fetch", "--quiet")
	cmd.Dir = repoDir
	_ = cmd.Run()

	branch := goroku.GetVersionBranch()
	cmd = exec.Command("git", "rev-parse", "origin/"+branch)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = repoDir
		out, err = cmd.Output()
		if err != nil {
			return ""
		}
	}
	return strings.TrimSpace(string(out))
}

func (m *Updater) getCurrentHash() string {
	if m.noGit() {
		return ""
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = m.getRepoDir()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (m *Updater) getChangelog() string {
	if m.noGit() {
		return ""
	}
	repoDir := m.getRepoDir()
	branch := goroku.GetVersionBranch()
	cmd := exec.Command("git", "log", "HEAD..origin/"+branch, "--oneline", "--format=<b>%h</b>: <i>%s</i>", "-10")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	return strings.TrimSpace(string(out))
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

	ignorePermanent, _ := m.db.Get("Updater", "ignore_permanent", "").(string)
	if ignorePermanent != "" && ignorePermanent == latest {
		return nil
	}

	if latest == m.notified {
		return nil
	}

	if m.autoupdate {
		m.notified = latest
		repoDir := m.getRepoDir()
		cmd := exec.Command("git", "pull")
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err == nil && !strings.Contains(string(out), "Already up to date") {
			_, _ = m.client.SendMessage(m.client.TGID,
				fmt.Sprintf("🔄 <b>Auto-updated to</b> <code>%s</code>\n\n%s", latest[:6], changelog))
			m.db.Set("Updater", "restart_ts", time.Now().Unix())
			go func() {
				time.Sleep(1 * time.Second)
				goroku.Restart()
			}()
		}
		return nil
	}

	_, _ = m.client.SendMessage(m.client.TGID,
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
	m.db.Set("Updater", "ignore_permanent", false)
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
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	announcement := strings.TrimSpace(string(body))
	previous, _ := m.db.Get("Updater", "announcement", "").(string)

	if announcement != "" && announcement != previous {
		_, _ = m.client.SendMessage(m.client.TGID,
			fmt.Sprintf("📢 <b>Goroku Announcement:</b>\n\n%s", announcement))
		m.db.Set("Updater", "announcement", announcement)
	}
	return nil
}

func (m *Updater) handlePostRestart() {
	selfUpdateMsg, _ := m.db.Get("Updater", "selfupdatemsg", "").(string)
	if selfUpdateMsg == "" {
		return
	}

	startTS := m.db.Get("Updater", "restart_ts", int64(0))
	var took string
	switch v := startTS.(type) {
	case float64:
		took = fmt.Sprintf("%d", int64(time.Now().Unix())-int64(v))
	case int64:
		took = fmt.Sprintf("%d", time.Now().Unix()-v)
	default:
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

	if strings.Contains(selfUpdateMsg, ":") {
		parts := strings.SplitN(selfUpdateMsg, ":", 2)
		chatID, err1 := strconv.ParseInt(parts[0], 10, 64)
		msgID, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 == nil && err2 == nil {
			_, _ = m.client.EditMessage(chatID, msgID, msg)
		}
	}

	m.db.Set("Updater", "selfupdatemsg", "")
	m.db.Set("Updater", "restart_ts", nil)
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
		if writeErr := os.WriteFile(backupPath, backupBytes, 0644); writeErr == nil {
			_ = msg.Answer(fmt.Sprintf("💾 <b>DB backup created:</b> <code>%s</code>", filepath.Base(backupPath)))
			time.Sleep(500 * time.Millisecond)
		}
	}

	cmd := exec.Command("git", "pull")
	cmd.Dir = repoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = msg.Answer(fmt.Sprintf("<tg-emoji emoji-id=5210952531676504517>❌</tg-emoji> <b>Update failed:</b>\n<pre>%s</pre>", string(output)))
		return nil
	}

	outStr := string(output)
	if strings.Contains(outStr, "Already up to date") || strings.Contains(outStr, "Уже обновлено") {
		_ = msg.Answer(m.getTrans("no_update", "<tg-emoji emoji-id=5465496001856950230>🌟</tg-emoji> <b>You are on the latest version!</b>"))
		return nil
	}

	_ = msg.Answer(m.getTrans("installing", "<tg-emoji emoji-id=5208622108191506906>🕗</tg-emoji> <b>Installing updates...</b>"))

	m.db.Set("Updater", "selfupdatemsg", fmt.Sprintf("%d:%d", msg.ChatID, msg.ID))
	m.db.Set("Updater", "restart_ts", time.Now().Unix())

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()

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

	_ = msg.Answer(text)

	m.db.Set("Updater", "selfupdatemsg", fmt.Sprintf("%d:%d", msg.ChatID, msg.ID))
	m.db.Set("Updater", "restart_ts", time.Now().Unix())

	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()
	return nil
}

func (m *Updater) ChangelogCmd(msg *goroku.Message) error {
	repoDir := m.getRepoDir()
	changelogPath := filepath.Join(repoDir, "CHANGELOG.md")

	content, err := os.ReadFile(changelogPath)
	if err != nil {
		cmd := exec.Command("git", "log", "--oneline", "-15", "--pretty=format:%h: %s")
		cmd.Dir = repoDir
		output, gitErr := cmd.Output()
		if gitErr != nil {
			_ = msg.Answer("⚠️ <b>No CHANGELOG.md found and git log failed</b>")
			return nil
		}
		_ = msg.Answer("📋 <b>Recent commits:</b>\n<pre>" + string(output) + "</pre>")
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

	m.db.Set("Updater", "autoupdate", newState)
	m.autoupdate = newState

	if newState {
		_ = msg.Answer("✅ <b>Auto-update enabled.</b> Bot will update automatically when new versions are available.")
	} else {
		_ = msg.Answer("🚫 <b>Auto-update disabled.</b> You will be notified about updates but won't auto-install them.")
	}
	return nil
}

func (m *Updater) SourceCmd(msg *goroku.Message) error {
	repoDir := m.getRepoDir()
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		_ = msg.Answer("⚠️ <b>Could not determine source URL</b>")
		return nil
	}
	url := strings.TrimSpace(string(output))
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

	repoDir := m.getRepoDir()
	cmd := exec.Command("git", "reset", "--hard", fmt.Sprintf("HEAD~%d", n))
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return msg.Answer(fmt.Sprintf("❌ <b>Rollback failed:</b>\n<pre>%s</pre>", string(out)))
	}

	_ = msg.Answer("✅ <b>Rollback successful! Restarting...</b>")
	go func() {
		time.Sleep(1 * time.Second)
		goroku.Restart()
	}()
	return nil
}

func (m *Updater) UbstopCmd(msg *goroku.Message) error {
	m.db.Set("Updater", "autoupdate", false)
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
