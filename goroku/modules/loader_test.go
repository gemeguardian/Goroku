package modules

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"goroku/goroku"
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

func TestOwnerAuthorizedInstallNeedsNoConfirmation(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = t.TempDir()
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })

	client := goroku.NewCustomTelegramClient(42)
	client.Loader = goroku.NewModules(client, db)
	loader := &LoaderModule{client: client, db: db}
	body := []byte("package modules\n\ntype DirectInstall struct{}\n")
	loader.installHotModuleApply = func(_ *goroku.Message, _, dest string, source []byte) error {
		return os.WriteFile(dest, source, 0600)
	}
	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	dest, err := runtimeModuleSourcePath("DirectInstall")
	if err != nil {
		t.Fatal(err)
	}
	msg := &goroku.Message{RawText: ".loadmod package modules", SenderID: 42, Client: client}
	if _, err := loader.installPersistedHotModule(msg, "DirectInstall", dest, "https://example.com/DirectInstall.go", body); err != nil {
		t.Fatalf("install without confirmation failed: %v", err)
	}
	if got := moduleContentDigests(db)["DirectInstall"]; got != contentSHA256(body) {
		t.Fatalf("persisted digest = %q", got)
	}
}

func TestInstallReturnsActualRuntimeModuleWhenSourceNameDiffers(t *testing.T) {
	db := newSecurityModuleTestDatabase(t)
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = t.TempDir()
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })

	client := goroku.NewCustomTelegramClient(42)
	client.Loader = goroku.NewModules(client, db)
	loader := &LoaderModule{client: client, db: db}
	loader.installHotModuleApply = func(_ *goroku.Message, _, dest string, source []byte) error {
		if err := os.WriteFile(dest, source, 0600); err != nil {
			return err
		}
		return client.Loader.RegisterModule(&displayNameModule{name: "RuntimeName"})
	}
	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	dest, err := runtimeModuleSourcePath("StructKey")
	if err != nil {
		t.Fatal(err)
	}
	installed, err := loader.installPersistedHotModule(nil, "StructKey", dest, "local", []byte("package modules\n\ntype StructKey struct{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if installed == nil || installed.Name() != "RuntimeName" {
		t.Fatalf("installed module = %#v, want runtime name RuntimeName", installed)
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
	if err := verifyModuleContentDigest(db, "Pinned", swapped, true); err == nil {
		t.Fatal("expected swapped content to be rejected against recorded digest")
	}
	if err := verifyModuleContentDigest(db, "Pinned", pinned, true); err != nil {
		t.Fatalf("recorded content should verify: %v", err)
	}
	// No digest: remote re-download must fail without an allowlist fallback.
	if err := verifyModuleContentDigest(db, "Unpinned", swapped, true); err == nil {
		t.Fatal("re-download without a recorded digest must fail")
	}
	// Persisted local source with a digest mismatch refuses.
	if err := verifyModuleContentDigest(db, "Pinned", swapped, false); err == nil {
		t.Fatal("local digest mismatch must refuse")
	}
	_ = loader
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

type displayNameModule struct{ name string }

func (m *displayNameModule) Name() string                                            { return m.name }
func (*displayNameModule) Strings() map[string]string                                { return nil }
func (*displayNameModule) Init(*goroku.CustomTelegramClient, *goroku.Database) error { return nil }
func (*displayNameModule) ClientReady() error                                        { return nil }
func (*displayNameModule) OnUnload() error                                           { return nil }
func (*displayNameModule) OnDlmod() error                                            { return nil }
func (*displayNameModule) Commands() map[string]goroku.CommandHandler                { return nil }
func (*displayNameModule) Watchers() []goroku.WatcherHandler                         { return nil }

func TestRegisteredModuleSourceUsesStructNameRatherThanDisplayName(t *testing.T) {
	dataRoot := t.TempDir()
	oldBaseDir := goroku.BaseDir
	goroku.BaseDir = dataRoot
	t.Cleanup(func() { goroku.BaseDir = oldBaseDir })

	if err := ensureRuntimeModuleSourceDir(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runtimeModuleSourceDir(), "UtilsModule.go")
	if err := os.WriteFile(path, []byte("package modules\n\ntype displayNameModule struct{}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	found, err := findRegisteredModuleSource(&displayNameModule{name: "Utils"})
	if err != nil || found != path {
		t.Fatalf("registered module source = %q, %v; want %q", found, err, path)
	}
}

func TestModuleSourceForExportFindsBuiltinWithoutMakingItUnloadable(t *testing.T) {
	dataRoot := t.TempDir()
	sourceRoot := t.TempDir()
	builtinDir := filepath.Join(sourceRoot, "goroku", "modules")
	if err := os.MkdirAll(builtinDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(builtinDir, "display.go")
	if err := os.WriteFile(path, []byte("package modules\n\ntype displayNameModule struct{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	oldBaseDir, oldBasePath := goroku.BaseDir, goroku.BasePath
	goroku.BaseDir, goroku.BasePath = dataRoot, sourceRoot
	t.Cleanup(func() { goroku.BaseDir, goroku.BasePath = oldBaseDir, oldBasePath })

	module := &displayNameModule{name: "Display"}
	found, err := findModuleSourceForExport(module)
	if err != nil || found != path {
		t.Fatalf("export source = %q, %v; want %q", found, err, path)
	}
	if _, err := findRegisteredModuleSource(module); err == nil {
		t.Fatal("built-in source was classified as an unloadable runtime module")
	}
}
