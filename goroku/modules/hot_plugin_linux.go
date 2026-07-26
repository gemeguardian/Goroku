//go:build linux || android

package modules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"goroku/goroku"
	"io"
	"os"
	"path/filepath"
	"plugin"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	hotPluginArtifactLimit = 32
	hotPluginTempMaxAge    = time.Hour
)

var ErrHotPluginRestartRequired = errors.New("hot plugin image limit reached; restart is required")

type HotPluginRestartRequiredError struct {
	Limit int
}

func (e *HotPluginRestartRequiredError) Error() string {
	return fmt.Sprintf("opened %d unique hot plugin images; restart Goroku before loading another revision", e.Limit)
}

func (e *HotPluginRestartRequiredError) Unwrap() error { return ErrHotPluginRestartRequired }

type hotArtifactBuildLock struct {
	mu   sync.Mutex
	refs int
}

var hotArtifactBuildLocks = struct {
	sync.Mutex
	locks map[string]*hotArtifactBuildLock
}{locks: make(map[string]*hotArtifactBuildLock)}

var hotPluginRegistry = struct {
	sync.Mutex
	plugins map[string]*plugin.Plugin
}{plugins: make(map[string]*plugin.Plugin)}

var hotPluginCurrent = struct {
	sync.Mutex
	refs map[string]int
}{refs: make(map[string]int)}

var hotPluginHostIdentityState struct {
	sync.Once
	value string
}

func RegisterModulesHot(msg *goroku.Message, structNames []string) error {
	if msg == nil || msg.Client == nil {
		return fmt.Errorf("message client is required for hot module loading")
	}

	loader := msg.Client.Loader
	if loader == nil {
		return fmt.Errorf("modules registry not found")
	}

	_ = msg.Answer("🛠 <b>Compiling modules for hot-load...</b>")
	if err := HotLoadStructs(loader, structNames); err != nil {
		return err
	}

	_ = msg.Answer("✅ <b>Modules loaded without restart.</b>")
	return nil
}

func HotLoadStructs(loader *goroku.Modules, structNames []string) error {
	mods, err := buildAndOpenPluginBundle(structNames)
	if err != nil && len(structNames) > 1 && !errors.Is(err, ErrHotPluginRestartRequired) {
		mods = make([]goroku.Module, 0, len(structNames))
		for _, structName := range structNames {
			mod, individualErr := buildAndOpenPlugin(structName)
			if individualErr != nil {
				return errors.Join(fmt.Errorf("module bundle build failed: %w", err), fmt.Errorf("individual build failed for %s: %w", structName, individualErr))
			}
			mods = append(mods, mod)
		}
	} else if err != nil {
		return err
	}
	for _, mod := range mods {
		if err := replaceOpenedPlugin(loader, mod); err != nil {
			return err
		}
	}
	return nil
}

func prepareHotModule(structName, sourcePath string) (goroku.Module, error) {
	return buildAndOpenPluginFromSource(structName, sourcePath)
}

func replacePreparedHotModule(loader *goroku.Modules, mod goroku.Module) error {
	return replaceOpenedPlugin(loader, mod)
}

func buildAndOpenPlugin(structName string) (goroku.Module, error) {
	sourcePath, err := findModuleSource(structName)
	if err != nil {
		return nil, err
	}
	return buildAndOpenPluginFromSource(structName, sourcePath)
}

type hotModuleSource struct {
	structName string
	source     []byte
}

// buildAndOpenPluginBundle keeps restored modules in one Go plugin image.
// A plugin carries a large copy of package metadata, so one image per module
// makes idle RSS grow by roughly 10-20 MiB for every installed module.
func buildAndOpenPluginBundle(structNames []string) ([]goroku.Module, error) {
	if len(structNames) == 0 {
		return nil, nil
	}
	if len(structNames) == 1 {
		mod, err := buildAndOpenPlugin(structNames[0])
		if err != nil {
			return nil, err
		}
		return []goroku.Module{mod}, nil
	}
	structNames = append([]string(nil), structNames...)
	sort.Strings(structNames)

	sources := make([]hotModuleSource, 0, len(structNames))
	for i, structName := range structNames {
		if i > 0 && structName == structNames[i-1] {
			return nil, fmt.Errorf("module bundle contains duplicate struct name %q", structName)
		}
		sourcePath, err := findModuleSource(structName)
		if err != nil {
			return nil, err
		}
		source, err := os.ReadFile(sourcePath) //nolint:gosec
		if err != nil {
			return nil, err
		}
		sources = append(sources, hotModuleSource{structName: structName, source: source})
	}

	moduleRoot, err := currentGorokuModuleRoot()
	if err != nil {
		return nil, err
	}
	key := hotPluginArtifactKey("bundle", sources, moduleRoot)
	releaseCurrent := retainHotPluginArtifact(key)
	defer releaseCurrent()
	pluginFile, err := cachedHotPluginArtifact(key, func(workDir string) (string, error) {
		return prepareHotPluginBundleBuildAtRoot(sources, workDir, moduleRoot)
	})
	if err != nil {
		return nil, err
	}
	plug, err := openHotPlugin(key, pluginFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open module bundle: %w", err)
	}
	sym, err := plug.Lookup("NewModules")
	if err != nil {
		return nil, fmt.Errorf("module bundle does not export NewModules: %v", err)
	}
	factory, ok := sym.(func() []goroku.Module)
	if !ok {
		return nil, fmt.Errorf("module bundle has invalid NewModules signature")
	}
	mods, err := callBundleFactory(factory)
	if err != nil {
		return nil, err
	}
	if len(mods) != len(sources) {
		return nil, fmt.Errorf("module bundle returned %d modules, expected %d", len(mods), len(sources))
	}
	seen := make(map[string]struct{}, len(mods))
	for i, mod := range mods {
		if mod == nil {
			return nil, fmt.Errorf("module bundle factory returned nil module at index %d", i)
		}
		name := strings.ToLower(mod.Name())
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("module bundle factory returned duplicate module name %q", mod.Name())
		}
		seen[name] = struct{}{}
		index := i
		rememberHotModuleFactory(mod, func() goroku.Module {
			fresh, factoryErr := callBundleFactory(factory)
			if factoryErr != nil || index >= len(fresh) {
				return nil
			}
			return fresh[index]
		})
	}
	return mods, nil
}

func buildAndOpenPluginFromSource(structName, sourcePath string) (goroku.Module, error) {
	sourceBytes, err := os.ReadFile(sourcePath) //nolint:gosec
	if err != nil {
		return nil, err
	}

	moduleRoot, err := currentGorokuModuleRoot()
	if err != nil {
		return nil, err
	}
	sources := []hotModuleSource{{structName: structName, source: sourceBytes}}
	key := hotPluginArtifactKey("single", sources, moduleRoot)
	releaseCurrent := retainHotPluginArtifact(key)
	defer releaseCurrent()
	pluginFile, err := cachedHotPluginArtifact(key, func(workDir string) (string, error) {
		return prepareHotPluginBuildAtRoot(structName, sourceBytes, workDir, moduleRoot)
	})
	if err != nil {
		return nil, err
	}

	// plugin.Open maps native code into the process; it cannot be fully unloaded
	// from memory later even if the module is unregistered from the command table.
	plug, err := openHotPlugin(key, pluginFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open plugin %s: %w", structName, err)
	}
	sym, err := plug.Lookup("NewModule")
	if err != nil {
		return nil, fmt.Errorf("plugin %s does not export NewModule: %v", structName, err)
	}
	factory, ok := sym.(func() goroku.Module)
	if !ok {
		return nil, fmt.Errorf("plugin %s has invalid NewModule signature", structName)
	}

	mod, err := callModuleFactory(structName, factory)
	if err != nil {
		return nil, err
	}
	rememberHotModuleFactory(mod, func() goroku.Module {
		fresh, factoryErr := callModuleFactory(structName, factory)
		if factoryErr != nil {
			return nil
		}
		return fresh
	})
	return mod, nil
}

func validateHotModuleCompilation(structName string, source []byte) error {
	workDir, err := os.MkdirTemp("", ".goroku-plugin-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workDir) }()
	_, err = prepareHotPluginBuild(structName, source, workDir)
	return err
}

// prepareHotPluginBuild performs the exact source rewrite and compilation used
// by hot loading. It deliberately stops before plugin.Open, which runs init code.
func prepareHotPluginBuild(structName string, sourceBytes []byte, workDir string) (string, error) {
	moduleRoot, err := currentGorokuModuleRoot()
	if err != nil {
		return "", err
	}
	return prepareHotPluginBuildAtRoot(structName, sourceBytes, workDir, moduleRoot)
}

func prepareHotPluginBuildAtRoot(structName string, sourceBytes []byte, workDir, moduleRoot string) (string, error) {
	pluginSource, err := rewriteModulePackage(sourceBytes, "main")
	if err != nil {
		return "", fmt.Errorf("module %s: %w", structName, err)
	}

	moduleFile := filepath.Join(workDir, "module.go")
	wrapperFile := filepath.Join(workDir, "plugin_export.go")
	pluginFile := filepath.Join(workDir, "plugin.so")
	if err := clearStalePluginSources(workDir); err != nil {
		return "", err
	}

	if err := os.WriteFile(moduleFile, pluginSource, 0600); err != nil {
		return "", err
	}
	wrapper := fmt.Sprintf("package main\n\nimport \"goroku/goroku\"\n\nfunc NewModule() goroku.Module {\n\treturn &%s{}\n}\n", structName)
	if err := os.WriteFile(wrapperFile, []byte(wrapper), 0600); err != nil {
		return "", err
	}
	identity := hotPluginArtifactKey("single", []hotModuleSource{{structName: structName, source: sourceBytes}}, moduleRoot)
	goMod := fmt.Sprintf("module goroku.hotplugin/%s/h%s\n\ngo 1.25.0\n\nrequire goroku v0.0.0\n\nreplace goroku => %s\n", strings.ToLower(structName), identity, strconv.Quote(filepath.ToSlash(moduleRoot)))
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(goMod), 0600); err != nil {
		return "", err
	}
	return compileHotPlugin(workDir, pluginFile, structName)
}

func prepareHotPluginBundleBuild(sources []hotModuleSource, workDir string) (string, error) {
	moduleRoot, err := currentGorokuModuleRoot()
	if err != nil {
		return "", err
	}
	return prepareHotPluginBundleBuildAtRoot(sources, workDir, moduleRoot)
}

func prepareHotPluginBundleBuildAtRoot(sources []hotModuleSource, workDir, moduleRoot string) (string, error) {
	if err := clearHotPluginGoSources(workDir); err != nil {
		return "", err
	}
	constructors := make([]string, 0, len(sources))
	for i, source := range sources {
		pluginSource, err := rewriteModulePackage(source.source, "main")
		if err != nil {
			return "", fmt.Errorf("module %s: %w", source.structName, err)
		}
		moduleFile := filepath.Join(workDir, fmt.Sprintf("module_%03d.go", i))
		if err := os.WriteFile(moduleFile, pluginSource, 0600); err != nil {
			return "", err
		}
		constructors = append(constructors, fmt.Sprintf("&%s{}", source.structName))
	}

	wrapper := "package main\n\nimport \"goroku/goroku\"\n\nfunc NewModules() []goroku.Module {\n\treturn []goroku.Module{" + strings.Join(constructors, ", ") + "}\n}\n"
	if err := os.WriteFile(filepath.Join(workDir, "plugin_export.go"), []byte(wrapper), 0600); err != nil {
		return "", err
	}
	identity := hotPluginArtifactKey("bundle", sources, moduleRoot)
	goMod := fmt.Sprintf("module goroku.hotplugin/bundle/h%s\n\ngo 1.25.0\n\nrequire goroku v0.0.0\n\nreplace goroku => %s\n", identity, strconv.Quote(filepath.ToSlash(moduleRoot)))
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(goMod), 0600); err != nil {
		return "", err
	}
	pluginFile := filepath.Join(workDir, "plugin.so")
	return compileHotPlugin(workDir, pluginFile, "module bundle")
}

func currentGorokuModuleRoot() (string, error) {
	moduleRoot, err := gorokuModuleRoot(goroku.BasePath)
	if err == nil {
		return moduleRoot, nil
	}
	return gorokuModuleRoot(".")
}

func hotPluginBuildProfile() string {
	settings := []string(nil)
	goVersion := runtime.Version()
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.GoVersion != "" {
			goVersion = info.GoVersion
		}
		settings = make([]string, 0, len(info.Settings))
		for _, setting := range info.Settings {
			settings = append(settings, setting.Key+"="+setting.Value)
		}
		sort.Strings(settings)
	}
	flags := []string{"-mod=mod", "-buildmode=plugin"}
	if hotPluginTrimpathEnabled() {
		flags = append(flags, "-trimpath")
	}
	if raceEnabled {
		flags = append(flags, "-race")
	}
	return strings.Join([]string{
		"host=" + hotPluginHostIdentity(),
		"go=" + goVersion,
		"runtime=" + runtime.Version(),
		"target=" + runtime.GOOS + "/" + runtime.GOARCH,
		"flags=" + strings.Join(flags, " "),
		"settings=" + strings.Join(settings, "\x00"),
	}, "\n")
}

func hotPluginHostIdentity() string {
	hotPluginHostIdentityState.Do(func() {
		file, err := os.Open("/proc/self/exe") //nolint:gosec
		if err != nil {
			executable, executableErr := os.Executable()
			if executableErr == nil {
				file, err = os.Open(executable) //nolint:gosec
			}
		}
		if err != nil {
			hotPluginHostIdentityState.value = "unknown"
			return
		}
		defer func() { _ = file.Close() }()
		hasher := sha256.New()
		if _, err := io.Copy(hasher, file); err != nil {
			hotPluginHostIdentityState.value = "unknown"
			return
		}
		hotPluginHostIdentityState.value = hex.EncodeToString(hasher.Sum(nil))
	})
	return hotPluginHostIdentityState.value
}

func hotPluginTrimpathEnabled() bool {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-trimpath" && setting.Value == "true" {
				return true
			}
		}
	}
	return false
}

func hotPluginArtifactKey(kind string, sources []hotModuleSource, moduleRoot string) string {
	hasher := sha256.New()
	writeHashField := func(value string) {
		_, _ = fmt.Fprintf(hasher, "%d:", len(value))
		_, _ = hasher.Write([]byte(value))
	}
	writeHashField(kind)
	writeHashField(filepath.ToSlash(moduleRoot))
	writeHashField(hotPluginBuildProfile())
	for _, source := range sources {
		writeHashField(source.structName)
		_, _ = fmt.Fprintf(hasher, "%d:", len(source.source))
		_, _ = hasher.Write(source.source)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func hotPluginCacheRoot() string {
	return filepath.Join(goroku.BaseDir, ".goroku_plugins")
}

func validHotPluginArtifact(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func acquireHotArtifactBuildLock(key string) func() {
	hotArtifactBuildLocks.Lock()
	lock := hotArtifactBuildLocks.locks[key]
	if lock == nil {
		lock = &hotArtifactBuildLock{}
		hotArtifactBuildLocks.locks[key] = lock
	}
	lock.refs++
	hotArtifactBuildLocks.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		hotArtifactBuildLocks.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(hotArtifactBuildLocks.locks, key)
		}
		hotArtifactBuildLocks.Unlock()
	}
}

func retainHotPluginArtifact(key string) func() {
	hotPluginCurrent.Lock()
	hotPluginCurrent.refs[key]++
	hotPluginCurrent.Unlock()
	return func() {
		hotPluginCurrent.Lock()
		hotPluginCurrent.refs[key]--
		if hotPluginCurrent.refs[key] == 0 {
			delete(hotPluginCurrent.refs, key)
		}
		hotPluginCurrent.Unlock()
	}
}

func cachedHotPluginArtifact(key string, build func(string) (string, error)) (string, error) {
	release := acquireHotArtifactBuildLock(key)
	defer release()

	cacheRoot := hotPluginCacheRoot()
	artifactsRoot := filepath.Join(cacheRoot, "artifacts")
	finalDir := filepath.Join(artifactsRoot, key)
	finalPlugin := filepath.Join(finalDir, "plugin.so")
	if validHotPluginArtifact(finalPlugin) {
		return finalPlugin, nil
	}
	if err := os.MkdirAll(artifactsRoot, 0750); err != nil {
		return "", err
	}
	tempDir, err := os.MkdirTemp(cacheRoot, ".tmp-"+key+"-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	builtPlugin, err := build(tempDir)
	if err != nil {
		return "", err
	}
	if !validHotPluginArtifact(builtPlugin) {
		return "", fmt.Errorf("plugin build produced an empty artifact")
	}
	if err := os.RemoveAll(finalDir); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(tempDir, finalDir); err != nil {
		if validHotPluginArtifact(finalPlugin) {
			return finalPlugin, nil
		}
		return "", fmt.Errorf("publish plugin artifact: %w", err)
	}
	return finalPlugin, nil
}

func openHotPlugin(key, pluginFile string) (*plugin.Plugin, error) {
	hotPluginRegistry.Lock()
	defer hotPluginRegistry.Unlock()
	if opened := hotPluginRegistry.plugins[key]; opened != nil {
		return opened, nil
	}
	if len(hotPluginRegistry.plugins) >= hotPluginArtifactLimit {
		return nil, &HotPluginRestartRequiredError{Limit: hotPluginArtifactLimit}
	}
	opened, err := plugin.Open(pluginFile)
	if err != nil {
		return nil, err
	}
	hotPluginRegistry.plugins[key] = opened
	cacheRoot := hotPluginCacheRoot()
	go func() { _ = cleanupHotPluginCache(cacheRoot, time.Now()) }()
	return opened, nil
}

func callModuleFactory(name string, factory func() goroku.Module) (mod goroku.Module, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			mod = nil
			err = fmt.Errorf("plugin %s NewModule panicked: %v", name, recovered)
		}
	}()
	mod = factory()
	if mod == nil {
		return nil, fmt.Errorf("plugin %s factory returned nil module", name)
	}
	return mod, nil
}

func callBundleFactory(factory func() []goroku.Module) (mods []goroku.Module, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			mods = nil
			err = fmt.Errorf("module bundle NewModules panicked: %v", recovered)
		}
	}()
	return factory(), nil
}

type hotPluginArtifactEntry struct {
	path    string
	modTime time.Time
}

func cleanupHotPluginCache(cacheRoot string, now time.Time) error {
	hotArtifactBuildLocks.Lock()
	current := make(map[string]struct{}, len(hotArtifactBuildLocks.locks))
	for key := range hotArtifactBuildLocks.locks {
		current[key] = struct{}{}
	}
	hotArtifactBuildLocks.Unlock()
	hotPluginCurrent.Lock()
	for key := range hotPluginCurrent.refs {
		current[key] = struct{}{}
	}
	hotPluginCurrent.Unlock()
	hotPluginRegistry.Lock()
	opened := make(map[string]struct{}, len(hotPluginRegistry.plugins))
	for key := range hotPluginRegistry.plugins {
		opened[key] = struct{}{}
	}
	hotPluginRegistry.Unlock()

	entries, err := os.ReadDir(cacheRoot)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	legacyArtifactName := regexp.MustCompile(`^(?:bundle_|.+_)[0-9a-f]{16}$`)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() != "artifacts" && !strings.HasPrefix(entry.Name(), ".tmp-") && legacyArtifactName.MatchString(entry.Name()) {
			_ = os.RemoveAll(filepath.Join(cacheRoot, entry.Name()))
			continue
		}
		if !strings.HasPrefix(entry.Name(), ".tmp-") {
			continue
		}
		protected := false
		for key := range current {
			if strings.HasPrefix(entry.Name(), ".tmp-"+key+"-") {
				protected = true
				break
			}
		}
		info, infoErr := entry.Info()
		if !protected && infoErr == nil && now.Sub(info.ModTime()) > hotPluginTempMaxAge {
			_ = os.RemoveAll(filepath.Join(cacheRoot, entry.Name()))
		}
	}

	artifactsRoot := filepath.Join(cacheRoot, "artifacts")
	artifactDirs, err := os.ReadDir(artifactsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	artifacts := make([]hotPluginArtifactEntry, 0, len(artifactDirs))
	for _, entry := range artifactDirs {
		if !entry.IsDir() {
			continue
		}
		key := entry.Name()
		if _, keep := opened[key]; keep {
			continue
		}
		if _, keep := current[key]; keep {
			continue
		}
		path := filepath.Join(artifactsRoot, key)
		if !validHotPluginArtifact(filepath.Join(path, "plugin.so")) {
			_ = os.RemoveAll(path)
			continue
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			artifacts = append(artifacts, hotPluginArtifactEntry{path: path, modTime: info.ModTime()})
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].modTime.After(artifacts[j].modTime) })
	if len(artifacts) > hotPluginArtifactLimit {
		for _, artifact := range artifacts[hotPluginArtifactLimit:] {
			_ = os.RemoveAll(artifact.path)
		}
	}
	return nil
}

func compileHotPlugin(workDir, pluginFile, label string) (string, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	buildArgs := []string{"build", "-mod=mod"}
	if hotPluginTrimpathEnabled() {
		buildArgs = append(buildArgs, "-trimpath")
	}
	if raceEnabled {
		buildArgs = append(buildArgs, "-race")
	}
	buildArgs = append(buildArgs, "-buildmode=plugin", "-o", pluginFile, ".")
	goPath := os.Getenv("GOPATH")
	if goPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			goPath = filepath.Join(home, "go")
		} else {
			goPath = filepath.Join(goroku.BaseDir, ".goroku_go", "gopath")
		}
	}
	goModCache := filepath.Join(goPath, "pkg", "mod")
	goCache := os.Getenv("GOCACHE")
	if goCache == "" {
		goCache = filepath.Join(goroku.BaseDir, ".goroku_go", "cache")
	}
	if err := os.MkdirAll(goPath, 0750); err != nil {
		return "", err
	}
	if err := os.MkdirAll(goCache, 0750); err != nil {
		return "", err
	}
	// Cleared env + required Go toolchain vars rather than full ambient env.
	extraEnv := []string{
		"GOPATH=" + goPath,
		"GOMODCACHE=" + goModCache,
		"GOCACHE=" + goCache,
	}
	if goproxy := os.Getenv("GOPROXY"); goproxy != "" {
		extraEnv = append(extraEnv, "GOPROXY="+goproxy)
	}
	if gosumdb := os.Getenv("GOSUMDB"); gosumdb != "" {
		extraEnv = append(extraEnv, "GOSUMDB="+gosumdb)
	}
	if goroot := os.Getenv("GOROOT"); goroot != "" {
		extraEnv = append(extraEnv, "GOROOT="+goroot)
	}
	if goTmpDir := os.Getenv("GOTMPDIR"); goTmpDir != "" {
		extraEnv = append(extraEnv, "GOTMPDIR="+goTmpDir)
	}
	if goFlags := os.Getenv("GOFLAGS"); goFlags != "" {
		extraEnv = append(extraEnv, "GOFLAGS="+goFlags)
	}
	output := newBoundedBuffer(externalOutputLimit)
	cmd, err := buildExecutor.CommandNoWait(ctx, ProcessSpec{
		Name:     "go",
		Args:     buildArgs,
		Dir:      workDir,
		ExtraEnv: extraEnv,
		Stdout:   output,
		Stderr:   output,
	})
	if errors.Is(err, ErrExecutorBusy) {
		// Builds are serialized, but the caller has already been told
		// "Compiling…" — report the queue instead of hanging on it.
		return "", fmt.Errorf("another plugin build is already running; try again once it finishes")
	}
	if err != nil {
		return "", err
	}
	defer buildExecutor.Release()
	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("plugin build failed for %s: %v\n%s", label, err, output.String())
	}
	return pluginFile, nil
}

func clearHotPluginGoSources(workDir string) error {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		if err := os.Remove(filepath.Join(workDir, entry.Name())); err != nil {
			return fmt.Errorf("remove stale plugin source %q: %w", entry.Name(), err)
		}
	}
	return nil
}

// clearStalePluginSources removes source copies left by older plugin builders.
// Only the generated module and factory files may participate in a build.
func clearStalePluginSources(workDir string) error {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		switch entry.Name() {
		case "module.go", "plugin_export.go":
			continue
		}
		if err := os.Remove(filepath.Join(workDir, entry.Name())); err != nil {
			return fmt.Errorf("remove stale plugin source %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func gorokuModuleRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		modPath := filepath.Join(dir, "go.mod")
		contents, readErr := os.ReadFile(modPath) //nolint:gosec
		if readErr == nil && regexp.MustCompile(`(?m)^module\s+goroku\s*$`).Match(contents) {
			resolved, err := filepath.EvalSymlinks(dir)
			if err != nil {
				return "", err
			}
			return resolved, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("cannot locate the goroku module root from %s", start)
		}
		dir = parent
	}
}

func replaceOpenedPlugin(loader *goroku.Modules, mod goroku.Module) error {
	old := loader.LookupByName(mod.Name())
	if old != nil {
		detached, err := unloadModuleForTransaction(loader, old.Name())
		if err != nil {
			cause := fmt.Errorf("failed to replace module %s: %w", mod.Name(), err)
			if detached {
				return restoreDetachedModule(loader, old, cause)
			}
			return cause
		}
	}
	restore := func(cause error) error {
		if old != nil {
			if err := registerRestoredModule(loader, old); err != nil {
				return errors.Join(cause, fmt.Errorf("failed to restore previous module: %w", err))
			}
		}
		return cause
	}
	if err := loader.RegisterModuleReady(mod, func() error {
		if err := mod.OnDlmod(); err != nil {
			return fmt.Errorf("on_dlmod hook failed for %s: %w", mod.Name(), err)
		}
		if err := mod.ClientReady(); err != nil {
			return fmt.Errorf("client_ready hook failed for %s: %w", mod.Name(), err)
		}
		return nil
	}); err != nil {
		return restore(err)
	}
	return nil
}
