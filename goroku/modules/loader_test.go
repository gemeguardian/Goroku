package modules

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goroku/goroku"
	"goroku/goroku/utils"
)

func TestValidateRemoteURLPolicy(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://example.com/module.go", true},
		{"http://example.com/module.go", false},
		{"https://localhost/module.go", false},
		{"https://127.0.0.1/module.go", false},
		{"https://10.0.0.1/module.go", false},
		{"https://169.254.1.1/module.go", false},
		{"https://[::1]/module.go", false},
		{"https://100.64.0.1/module.go", false},
		{"https://100.127.1.1/module.go", false},
		{"file:///tmp/module.go", false},
		{"https://user:pass@example.com/module.go", false},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			if got := validateRemoteURL(tc.url) == nil; got != tc.want {
				t.Fatalf("validateRemoteURL() allowed=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestPresetsDownloadUsesModuleURLPolicy(t *testing.T) {
	// downloadPresetModuleURL must reject the same SSRF targets as loader.
	_, err := downloadPresetModuleURL("http://127.0.0.1/module.go")
	if err == nil {
		t.Fatal("expected presets download to reject insecure private URL")
	}
	_, err = downloadPresetModuleURL("https://100.64.1.2/module.go")
	if err == nil {
		t.Fatal("expected presets download to reject CGNAT target")
	}
}

func TestUnsafeInstallRequiresConfirmOrTrustedDigest(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	body := []byte("package modules\n\ntype Demo struct{}\n")
	msg := &goroku.Message{RawText: ".dlmod https://example.com/x.go", Text: ".dlmod https://example.com/x.go"}
	if err := ensureUnsafeInstallAllowed(msg, db, body, false); err == nil {
		t.Fatal("expected untrusted install without confirm to fail")
	}
	msg.RawText = ".dlmod https://example.com/x.go -confirm"
	if err := ensureUnsafeInstallAllowed(msg, db, body, false); err != nil {
		t.Fatalf("confirm token should allow install: %v", err)
	}
	if err := ensureUnsafeInstallAllowed(&goroku.Message{RawText: ".dlmod x"}, db, body, true); err != nil {
		t.Fatalf("interactive confirmed=true should allow install: %v", err)
	}
	digest := contentSHA256(body)
	if err := trustContentDigest(db, digest); err != nil {
		t.Fatal(err)
	}
	if err := ensureUnsafeInstallAllowed(&goroku.Message{RawText: ".dlmod x"}, db, body, false); err != nil {
		t.Fatalf("trusted digest should allow install: %v", err)
	}
}

func TestParseInstallArgsStripsConfirmWithoutPoisoningPayload(t *testing.T) {
	payload, confirmed := parseInstallArgs("https://example.com/mod.go -confirm")
	if !confirmed || payload != "https://example.com/mod.go" {
		t.Fatalf("url path: payload=%q confirmed=%v", payload, confirmed)
	}
	payload, confirmed = parseInstallArgs("-confirm MyModule")
	if !confirmed || payload != "MyModule" {
		t.Fatalf("name path: payload=%q confirmed=%v", payload, confirmed)
	}
	src := "package modules\n\ntype Demo struct{}\n"
	payload, confirmed = parseInstallArgs(src + "\n-confirm")
	if !confirmed {
		t.Fatal("expected confirm flag after multiline body")
	}
	if payload != strings.TrimSpace(src) && !strings.Contains(payload, "type Demo struct{}") {
		t.Fatalf("multiline body poisoned: %q", payload)
	}
	// Bare confirm must not strip from multi-line source containing the word.
	bodyWithWord := "package modules\n// confirm this works\ntype Demo struct{}\n"
	payload, confirmed = parseInstallArgs(bodyWithWord)
	if confirmed {
		t.Fatal("bare confirm inside multiline source must not be treated as flag")
	}
	if !strings.Contains(payload, "confirm this works") {
		t.Fatalf("source body altered: %q", payload)
	}
}

func TestDlmodConfirmPathDoesNotPoisonURL(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	dataRoot := t.TempDir()
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = dataRoot
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })

	client := goroku.NewCustomTelegramClient(42)
	client.Loader = goroku.NewModules(client, db)
	loader := &LoaderModule{client: client, db: db}

	goodBody := []byte("package modules\n\ntype ConfirmMod struct{}\nfunc (m *ConfirmMod) Name() string { return \"ConfirmMod\" }\n")
	var capturedBody []byte
	loader.installHotModuleApply = func(_ *goroku.Message, _, dest string, body []byte) error {
		capturedBody = append([]byte(nil), body...)
		return os.WriteFile(dest, body, 0600)
	}
	msg := &goroku.Message{
		RawText:  ".dlmod https://example.com/ConfirmMod.go -confirm",
		Text:     ".dlmod https://example.com/ConfirmMod.go -confirm",
		Client:   client,
		SenderID: 42,
	}
	urlPayload, confirmed := parseInstallArgs(utils.GetArgsRaw(msg.RawText))
	if !confirmed || strings.Contains(urlPayload, "confirm") || !strings.HasPrefix(urlPayload, "https://") {
		t.Fatalf("confirm not stripped from URL payload: %q confirmed=%v", urlPayload, confirmed)
	}
	dest, err := runtimeModuleSourcePath("ConfirmMod")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	if err := loader.installPersistedHotModuleConfirmed(msg, "ConfirmMod", dest, urlPayload, goodBody, confirmed); err != nil {
		t.Fatalf("confirmed install failed: %v", err)
	}
	if string(capturedBody) != string(goodBody) {
		t.Fatalf("body mismatch")
	}
	if got := moduleContentDigests(db)["ConfirmMod"]; got != contentSHA256(goodBody) {
		t.Fatalf("expected pinned digest, got %q", got)
	}
	// Real ensure path rejects unconfirmed untrusted install without compiling.
	if err := ensureUnsafeInstallAllowed(&goroku.Message{RawText: ".dlmod https://example.com/Other.go"}, db, goodBody, false); err == nil {
		t.Fatal("expected unconfirmed install to fail")
	}
}

func TestBootRestoreRefusesDigestMismatch(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	dataRoot := t.TempDir()
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = dataRoot
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })

	pinned := []byte("package modules\n\ntype Pinned struct{}\n")
	swapped := []byte("package modules\n\ntype Pinned struct{ Evil bool }\n")
	if err := setModuleContentDigest(db, "Pinned", contentSHA256(pinned)); err != nil {
		t.Fatal(err)
	}
	loader := &LoaderModule{db: db}
	// Simulate restoreLoadedModule body verification without network.
	if err := verifyPinnedOrTrustedContent(db, "Pinned", swapped, true); err == nil {
		t.Fatal("expected swapped content to be rejected against pin")
	}
	if err := verifyPinnedOrTrustedContent(db, "Pinned", pinned, true); err != nil {
		t.Fatalf("pinned content should verify: %v", err)
	}
	// No pin: re-download requires trust.
	if err := verifyPinnedOrTrustedContent(db, "Unpinned", swapped, true); err == nil {
		t.Fatal("unpinned re-download without trust must fail")
	}
	if err := trustContentDigest(db, contentSHA256(swapped)); err != nil {
		t.Fatal(err)
	}
	if err := verifyPinnedOrTrustedContent(db, "Unpinned", swapped, true); err != nil {
		t.Fatalf("trusted content should pass: %v", err)
	}
	// Local load with pin mismatch refuses.
	if err := verifyPinnedOrTrustedContent(db, "Pinned", swapped, false); err == nil {
		t.Fatal("local pin mismatch must refuse")
	}
	_ = loader
}

func TestUntrustRemovesContentDigest(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	body := []byte("package modules\n\ntype T struct{}\n")
	digest := contentSHA256(body)
	if err := trustContentDigest(db, digest); err != nil {
		t.Fatal(err)
	}
	if !isContentDigestTrusted(db, digest) {
		t.Fatal("expected trusted")
	}
	if err := untrustContentDigest(db, digest); err != nil {
		t.Fatal(err)
	}
	if isContentDigestTrusted(db, digest) {
		t.Fatal("digest remained trusted after untrust")
	}
}

func TestCallbackOwnerRecheck(t *testing.T) {
	client := goroku.NewCustomTelegramClient(99)
	type ans struct {
		text string
	}
	var got ans
	fake := fakeAnswer{fn: func(text string, _ bool) error {
		got.text = text
		return nil
	}}
	if requireOwnerCallback(client, fake, 99) != true {
		t.Fatal("owner TGID should pass")
	}
	if requireOwnerCallback(client, fake, 1) {
		t.Fatal("non-owner must fail")
	}
	if got.text == "" {
		t.Fatal("expected owner-only answer")
	}
}

type fakeAnswer struct {
	fn func(string, bool) error
}

func (f fakeAnswer) Answer(text string, showAlert bool) error { return f.fn(text, showAlert) }

func TestDangerousCommandMetasAreOwnerOnly(t *testing.T) {
	for name, meta := range (&Eval{}).CommandMetas() {
		if !meta.OnlyOwner {
			t.Fatalf("eval %s missing OnlyOwner", name)
		}
	}
	for name, meta := range (&TerminalMod{}).CommandMetas() {
		if !meta.OnlyOwner {
			t.Fatalf("terminal %s missing OnlyOwner", name)
		}
	}
	for name, meta := range (&LoaderModule{}).CommandMetas() {
		if !meta.OnlyOwner {
			t.Fatalf("loader %s missing OnlyOwner", name)
		}
	}
	for name, meta := range (&Presets{}).CommandMetas() {
		if !meta.OnlyOwner {
			t.Fatalf("presets %s missing OnlyOwner", name)
		}
	}
	for name, meta := range (&GorokuPluginSecurity{}).CommandMetas() {
		if !meta.OnlyOwner {
			t.Fatalf("plugin security %s missing OnlyOwner", name)
		}
	}
}

func TestModuleHTTPClientBlocksUnsafeRedirect(t *testing.T) {
	client := newModuleHTTPClient(time.Second)
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/module.go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("expected redirect to insecure private URL to be rejected")
	}
}

func TestRuntimeModuleSourceDoesNotAlterSourcePackage(t *testing.T) {
	dataRoot := t.TempDir()
	sourceRoot := t.TempDir()
	legacyDir := filepath.Join(sourceRoot, "goroku", "modules")
	if err := os.MkdirAll(legacyDir, 0750); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "Example.go")
	legacyBody := []byte("package modules\n\ntype Example struct{}\n")
	if err := os.WriteFile(legacyPath, legacyBody, 0600); err != nil {
		t.Fatal(err)
	}

	oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
	goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
	t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })

	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	runtimePath, err := runtimeModuleSourcePath("Example")
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(runtimePath, []byte("package modules\n\ntype Example struct{ Runtime bool }\n")); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(runtimePath) != filepath.Join(dataRoot, "modules") {
		t.Fatalf("runtime source path = %q", runtimePath)
	}
	if body, err := os.ReadFile(legacyPath); err != nil || string(body) != string(legacyBody) {
		t.Fatalf("source package changed: body=%q err=%v", body, err)
	}
	if found, err := findInstalledModuleSource("Example"); err != nil || found != runtimePath {
		t.Fatalf("installed source = %q, %v; want runtime source", found, err)
	}
}

func TestInstalledModuleSourceDoesNotAutomaticallyUseLegacySource(t *testing.T) {
	dataRoot := t.TempDir()
	sourceRoot := t.TempDir()
	legacyDir := filepath.Join(sourceRoot, "goroku", "modules")
	if err := os.MkdirAll(legacyDir, 0750); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "Legacy.go")
	if err := os.WriteFile(legacyPath, []byte("package modules\n"), 0600); err != nil {
		t.Fatal(err)
	}

	oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
	goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
	t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })

	if found, err := findInstalledModuleSource("Legacy"); err == nil {
		t.Fatalf("legacy source was selected automatically: %q", found)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "modules")); !os.IsNotExist(err) {
		t.Fatalf("legacy lookup created runtime storage: %v", err)
	}
}
