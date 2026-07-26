package goroku

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrConfigMissing is returned by ValidateConfigFile when the config file does
// not exist. It is distinct from a corrupt/unreadable config so doctor can
// treat a fresh install (no config yet) as a warning rather than a hard fail.
var ErrConfigMissing = errors.New("config file not found")

// ConfigFinding is a single redacted validation result for one config field.
type ConfigFinding struct {
	Field  string
	Status string
	Detail string
}

// DoctorOptions configures a doctor run. Zero-value Out falls back to os.Stdout.
// Empty ConfigPath/DataRoot fall back to the package defaults (ConfigPath/BaseDir).
type DoctorOptions struct {
	Out                io.Writer
	ConfigPath         string
	DataRoot           string
	ConfigValidateOnly bool
}

const (
	doctorStatusOK   = "ok"
	doctorStatusWarn = "warn"
	doctorStatusFail = "fail"
	doctorStatusInfo = "info"
	doctorStatusMiss = "missing"
)

// ValidateConfigBytes parses and validates raw config JSON, returning redacted
// findings. It is pure: no filesystem, no network, no env. Use it in tests.
func ValidateConfigBytes(raw []byte) ([]ConfigFinding, error) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if data == nil {
		data = map[string]any{}
	}
	return validateConfig(data), nil
}

// ValidateConfigFile reads and validates the config at path. A missing file
// returns ErrConfigMissing; a present-but-unparseable file returns the parse
// error; otherwise redacted findings are returned.
func ValidateConfigFile(path string) ([]ConfigFinding, error) {
	data, err := parseConfigFile(path)
	if err != nil {
		return nil, err
	}
	return validateConfig(data), nil
}

func parseConfigFile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrConfigMissing
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if data == nil {
		data = map[string]any{}
	}
	return data, nil
}

func validateConfig(data map[string]any) []ConfigFinding {
	findings := make([]ConfigFinding, 0, 5)

	switch v := data["api_id"].(type) {
	case nil:
		findings = append(findings, ConfigFinding{"api_id", doctorStatusMiss, "not set (web onboarding will prompt)"})
	case float64:
		if int64(v) <= 0 {
			findings = append(findings, ConfigFinding{"api_id", doctorStatusFail, "must be a positive integer"})
		} else {
			findings = append(findings, ConfigFinding{"api_id", doctorStatusOK, "present (redacted)"})
		}
	case string:
		id, perr := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if perr != nil {
			findings = append(findings, ConfigFinding{"api_id", doctorStatusFail, "not a valid integer"})
		} else if id <= 0 {
			findings = append(findings, ConfigFinding{"api_id", doctorStatusFail, "must be a positive integer"})
		} else {
			findings = append(findings, ConfigFinding{"api_id", doctorStatusOK, "present (redacted)"})
		}
	default:
		findings = append(findings, ConfigFinding{"api_id", doctorStatusFail, fmt.Sprintf("unexpected type %T", v)})
	}

	if h, ok := data["api_hash"].(string); ok && strings.TrimSpace(h) != "" {
		findings = append(findings, ConfigFinding{"api_hash", doctorStatusOK, "present (redacted)"})
	} else {
		findings = append(findings, ConfigFinding{"api_hash", doctorStatusMiss, "not set (web onboarding will prompt)"})
	}

	if r, ok := data["redis_uri"].(string); ok && strings.TrimSpace(r) != "" {
		findings = append(findings, ConfigFinding{"redis_uri", doctorStatusOK, "configured (redacted)"})
	} else {
		findings = append(findings, ConfigFinding{"redis_uri", doctorStatusInfo, "not set (local JSON only)"})
	}

	if v, ok := data["web_setup_completed"]; ok {
		findings = append(findings, ConfigFinding{"web_setup_completed", doctorStatusOK, fmt.Sprintf("set (%v)", v)})
	} else {
		findings = append(findings, ConfigFinding{"web_setup_completed", doctorStatusInfo, "not set (onboarding not completed)"})
	}

	return findings
}

// RunDoctor executes diagnostics and returns a process exit code (0 = healthy,
// 1 = at least one critical check failed). It never starts Telegram clients,
// the web server, or the inline bot. The only network touch is an optional
// Redis ping with a short timeout that fails gracefully.
func RunDoctor(opts DoctorOptions) int {
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	if strings.TrimSpace(opts.ConfigPath) == "" {
		opts.ConfigPath = ConfigPath
	}
	if strings.TrimSpace(opts.DataRoot) == "" {
		opts.DataRoot = BaseDir
	}

	if opts.ConfigValidateOnly {
		return runConfigValidate(out, opts.ConfigPath)
	}

	fmt.Fprintln(out, "Goroku doctor")
	fmt.Fprintln(out, strings.Repeat("=", 40))
	fmt.Fprintln(out)

	critical, warnings := 0, 0

	critical += printVersionSection(out)
	fmt.Fprintln(out)

	critical += printRuntimeSection(out)
	fmt.Fprintln(out)

	cfgData, cfgCrit, cfgWarn := printConfigSection(out, opts.ConfigPath)
	critical += cfgCrit
	warnings += cfgWarn
	fmt.Fprintln(out)

	dc, dw := printDataRootSection(out, opts.DataRoot)
	critical += dc
	warnings += dw
	fmt.Fprintln(out)

	critical += printRedisSection(out, cfgData)
	fmt.Fprintln(out)

	warnings += printWebSection(out)
	fmt.Fprintln(out)

	printTrustSection(out)
	fmt.Fprintln(out)

	fmt.Fprintln(out, strings.Repeat("=", 40))
	fmt.Fprintf(out, "Summary: %d critical, %d warning(s)\n", critical, warnings)
	if critical > 0 {
		fmt.Fprintln(out, "Result: FAIL")
		return 1
	}
	fmt.Fprintln(out, "Result: OK")
	return 0
}

func runConfigValidate(out io.Writer, configPath string) int {
	fmt.Fprintln(out, "Goroku config validate")
	fmt.Fprintln(out, strings.Repeat("=", 40))
	fmt.Fprintf(out, "  path: %s\n", configPath)
	fmt.Fprintln(out)
	data, err := parseConfigFile(configPath)
	if err != nil {
		if errors.Is(err, ErrConfigMissing) {
			fmt.Fprintf(out, "  status: %s — config not found at %s\n", doctorStatusFail, configPath)
		} else {
			fmt.Fprintf(out, "  status: %s — %v\n", doctorStatusFail, err)
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, strings.Repeat("=", 40))
		fmt.Fprintln(out, "Result: FAIL")
		return 1
	}
	for _, f := range validateConfig(data) {
		fmt.Fprintf(out, "  %-22s %s — %s\n", f.Field, f.Status, f.Detail)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, strings.Repeat("=", 40))
	fmt.Fprintln(out, "Result: OK")
	return 0
}

func printVersionSection(out io.Writer) int {
	fmt.Fprintln(out, "[version]")
	fmt.Fprintf(out, "  VersionInfo : %s\n", GetVersionString())
	fmt.Fprintf(out, "  Commit      : %s\n", nonEmptyOr(Commit, "(none)"))
	fmt.Fprintf(out, "  Release     : %v\n", IsReleaseBuild())
	return 0
}

func printRuntimeSection(out io.Writer) int {
	fmt.Fprintln(out, "[runtime]")
	fmt.Fprintf(out, "  Go version  : %s\n", runtime.Version())
	fmt.Fprintf(out, "  GOOS/GOARCH : %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(out, "  GOMAXPROCS  : %d\n", runtime.GOMAXPROCS(0))
	return 0
}

func printConfigSection(out io.Writer, configPath string) (map[string]any, int, int) {
	fmt.Fprintln(out, "[config]")
	fmt.Fprintf(out, "  path        : %s\n", configPath)
	data, err := parseConfigFile(configPath)
	critical, warnings := 0, 0
	if err != nil {
		if errors.Is(err, ErrConfigMissing) {
			fmt.Fprintf(out, "  status      : %s — config not found (web onboarding will create it)\n", doctorStatusWarn)
			warnings++
		} else {
			fmt.Fprintf(out, "  status      : %s — %v\n", doctorStatusFail, err)
			critical++
		}
		fmt.Fprintln(out)
		return nil, critical, warnings
	}
	for _, f := range validateConfig(data) {
		fmt.Fprintf(out, "  %-22s %s — %s\n", f.Field, f.Status, f.Detail)
	}
	return data, critical, warnings
}

// printDataRootSection reports data-root existence, writability, disk space,
// session files, and per-account DB summaries (loaded_modules + bot_token).
func printDataRootSection(out io.Writer, dataRoot string) (int, int) {
	fmt.Fprintln(out, "[data-root]")
	fmt.Fprintf(out, "  path        : %s\n", dataRoot)
	info, err := os.Stat(dataRoot)
	if err != nil {
		fmt.Fprintf(out, "  status      : %s — %v\n", doctorStatusFail, err)
		return 1, 0
	}
	if !info.IsDir() {
		fmt.Fprintf(out, "  status      : %s — not a directory\n", doctorStatusFail)
		return 1, 0
	}
	fmt.Fprintf(out, "  exists      : ok (dir)\n")

	probe := filepath.Join(dataRoot, ".goroku-doctor-probe")
	if werr := os.WriteFile(probe, []byte("ok"), 0600); werr != nil {
		fmt.Fprintf(out, "  writable    : %s — %v\n", doctorStatusFail, werr)
		return 1, 0
	}
	_ = os.Remove(probe)
	fmt.Fprintf(out, "  writable    : ok\n")

	fmt.Fprintf(out, "  disk        : %s\n", doctorDiskUsage(dataRoot))

	sessions := listSessionFiles(dataRoot)
	fmt.Fprintf(out, "  sessions    : %d\n", len(sessions))
	for _, s := range sessions {
		fmt.Fprintf(out, "    - %s\n", filepath.Base(s))
	}

	accounts := listAccounts(dataRoot)
	fmt.Fprintf(out, "  accounts    : %d\n", len(accounts))
	totalMods := 0
	for _, a := range accounts {
		totalMods += a.Modules
		tokenState := doctorStatusMiss
		if a.BotToken {
			tokenState = doctorStatusOK
		}
		fmt.Fprintf(out, "    - tg=%s modules=%d bot_token=%s\n", a.TGID, a.Modules, tokenState)
	}
	fmt.Fprintf(out, "  modules     : %d (loaded_modules across accounts)\n", totalMods)
	return 0, 0
}

func printRedisSection(out io.Writer, cfgData map[string]any) int {
	fmt.Fprintln(out, "[redis]")
	uri := strings.TrimSpace(os.Getenv("REDIS_URL"))
	if uri == "" && cfgData != nil {
		if v, ok := cfgData["redis_uri"].(string); ok {
			uri = strings.TrimSpace(v)
		}
	}
	if uri == "" {
		fmt.Fprintf(out, "  status      : not configured (local JSON only)\n")
		return 0
	}
	opt, perr := redis.ParseURL(uri)
	if perr != nil {
		fmt.Fprintf(out, "  status      : %s — parse: %v\n", doctorStatusFail, perr)
		return 0
	}
	opt.ContextTimeoutEnabled = true
	client := redis.NewClient(opt)
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if perr := client.Ping(ctx).Err(); perr != nil {
		fmt.Fprintf(out, "  status      : %s — ping: %v\n", doctorStatusWarn, perr)
		return 0
	}
	fmt.Fprintf(out, "  status      : ok (ping)\n")
	return 0
}

func printWebSection(out io.Writer) int {
	fmt.Fprintln(out, "[web]")
	bind, source := resolvedWebBind()
	fmt.Fprintf(out, "  bind        : %s (%s)\n", nonEmptyOr(bind, "(none)"), source)
	if bind != "" && !isLoopbackBindHost(bind) {
		if strings.TrimSpace(os.Getenv("GOROKU_TRUSTED_PROXIES")) == "" {
			fmt.Fprintf(out, "  warning     : non-loopback bind without GOROKU_TRUSTED_PROXIES; forwarding headers will not be trusted\n")
			return 1
		}
		fmt.Fprintf(out, "  trusted     : GOROKU_TRUSTED_PROXIES set\n")
	}
	return 0
}

func printTrustSection(out io.Writer) {
	fmt.Fprintln(out, "[trust]")
	fmt.Fprintf(out, "  trusted_proxies : %d CIDR(s)\n", countCSV(os.Getenv("GOROKU_TRUSTED_PROXIES")))
}

type accountSummary struct {
	TGID     string
	Modules  int
	BotToken bool
}

func listAccounts(dataRoot string) []accountSummary {
	files, _ := filepath.Glob(filepath.Join(dataRoot, "config-*.json"))
	out := make([]accountSummary, 0, len(files))
	for _, f := range files {
		base := filepath.Base(f)
		idStr := strings.TrimSuffix(strings.TrimPrefix(base, "config-"), ".json")
		if _, perr := strconv.ParseInt(idStr, 10, 64); perr != nil {
			continue
		}
		raw, rerr := os.ReadFile(f) //nolint:gosec
		if rerr != nil {
			out = append(out, accountSummary{TGID: idStr})
			continue
		}
		var data map[string]any
		if jerr := json.Unmarshal(raw, &data); jerr != nil || data == nil {
			out = append(out, accountSummary{TGID: idStr})
			continue
		}
		out = append(out, accountSummary{
			TGID:     idStr,
			Modules:  countLoadedModules(data),
			BotToken: hasBotToken(data),
		})
	}
	return out
}

func countLoadedModules(data map[string]any) int {
	loader, ok := data["Loader"].(map[string]any)
	if !ok {
		return 0
	}
	mods, ok := loader["loaded_modules"].(map[string]any)
	if !ok {
		return 0
	}
	return len(mods)
}

func hasBotToken(data map[string]any) bool {
	inline, ok := data["goroku.inline"].(map[string]any)
	if !ok {
		return false
	}
	if v, ok := inline["bot_token"].(string); ok && strings.TrimSpace(v) != "" {
		return true
	}
	return false
}

func listSessionFiles(dataRoot string) []string {
	patterns := []string{
		filepath.Join(dataRoot, "goroku-*.session"),
		filepath.Join(dataRoot, "heroku-*.session"),
		filepath.Join(dataRoot, "hikka-*.session"),
	}
	var out []string
	for _, p := range patterns {
		files, _ := filepath.Glob(p)
		for _, f := range files {
			base := filepath.Base(f)
			if base == "goroku-0.session" || base == "hikka-0.session" {
				continue
			}
			out = append(out, f)
		}
	}
	return out
}

func resolvedWebBind() (string, string) {
	if v := strings.TrimSpace(os.Getenv("GOROKU_WEB_BIND")); v != "" {
		return v, "GOROKU_WEB_BIND"
	}
	if v := strings.TrimSpace(os.Getenv("GOROKU_IP")); v != "" {
		return v, "GOROKU_IP"
	}
	if os.Getenv("DOCKER") != "" {
		return "0.0.0.0", "DOCKER default"
	}
	return "127.0.0.1", "default"
}

func isLoopbackBindHost(host string) bool {
	h := strings.Trim(strings.TrimSpace(host), "[]")
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(h, "localhost")
}

func countCSV(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n := 0
	for _, part := range strings.Split(v, ",") {
		if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n
}

func nonEmptyOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
