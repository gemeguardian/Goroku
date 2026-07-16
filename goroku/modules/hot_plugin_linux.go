//go:build linux || android

package modules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"goroku/goroku"
	"os"
	"path/filepath"
	"plugin"
	"regexp"
	"strconv"
	"strings"
	"time"
)

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
	for _, structName := range structNames {
		mod, err := buildAndOpenPlugin(structName)
		if err != nil {
			return err
		}

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

func buildAndOpenPluginFromSource(structName, sourcePath string) (goroku.Module, error) {
	sourceBytes, err := os.ReadFile(sourcePath) //nolint:gosec
	if err != nil {
		return nil, err
	}

	hash := sha256.Sum256(sourceBytes)
	shortHash := hex.EncodeToString(hash[:])[:16]
	workDir := filepath.Join(goroku.BaseDir, ".goroku_plugins", strings.ToLower(structName)+"_"+shortHash)
	if err := os.MkdirAll(workDir, 0750); err != nil {
		return nil, err
	}
	pluginFile, err := prepareHotPluginBuild(structName, sourceBytes, workDir)
	if err != nil {
		return nil, err
	}

	// plugin.Open maps native code into the process; it cannot be fully unloaded
	// from memory later even if the module is unregistered from the command table.
	plug, err := plugin.Open(pluginFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open plugin %s: %v", structName, err)
	}
	sym, err := plug.Lookup("NewModule")
	if err != nil {
		return nil, fmt.Errorf("plugin %s does not export NewModule: %v", structName, err)
	}
	factory, ok := sym.(func() goroku.Module)
	if !ok {
		return nil, fmt.Errorf("plugin %s has invalid NewModule signature", structName)
	}

	return factory(), nil
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
	packageRe := regexp.MustCompile(`(?m)^\s*package\s+\w+`)
	if !packageRe.Match(sourceBytes) {
		return "", fmt.Errorf("module %s has no package declaration", structName)
	}
	pluginSource := packageRe.ReplaceAllString(string(sourceBytes), "package main")

	moduleFile := filepath.Join(workDir, "module.go")
	wrapperFile := filepath.Join(workDir, "plugin_export.go")
	pluginFile := filepath.Join(workDir, strings.ToLower(structName)+".so")
	moduleRoot, err := gorokuModuleRoot(goroku.BasePath)
	if err != nil {
		moduleRoot, err = gorokuModuleRoot(".")
		if err != nil {
			return "", err
		}
	}

	if err := os.WriteFile(moduleFile, []byte(pluginSource), 0600); err != nil {
		return "", err
	}
	wrapper := fmt.Sprintf("package main\n\nimport \"goroku/goroku\"\n\nfunc NewModule() goroku.Module {\n\treturn &%s{}\n}\n", structName)
	if err := os.WriteFile(wrapperFile, []byte(wrapper), 0600); err != nil {
		return "", err
	}
	goMod := fmt.Sprintf("module goroku.hotplugin/%s\n\ngo 1.24.4\n\nrequire goroku v0.0.0\n\nreplace goroku => %s\n", strings.ToLower(structName), strconv.Quote(filepath.ToSlash(moduleRoot)))
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(goMod), 0600); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	buildArgs := []string{"build", "-mod=mod"}
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
	output := newBoundedBuffer(externalOutputLimit)
	cmd, err := defaultProcessExecutor.Command(ctx, ProcessSpec{
		Name:     "go",
		Args:     buildArgs,
		Dir:      workDir,
		ExtraEnv: extraEnv,
		Stdout:   output,
		Stderr:   output,
	})
	if err != nil {
		return "", err
	}
	defer defaultProcessExecutor.Release()
	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("plugin build failed for %s: %v\n%s", structName, err, output.String())
	}
	return pluginFile, nil
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
			if err := loader.RegisterModule(old); err != nil {
				return errors.Join(cause, fmt.Errorf("failed to restore previous module: %w", err))
			}
		}
		return cause
	}
	if err := loader.RegisterModule(mod); err != nil {
		return restore(err)
	}
	if err := mod.OnDlmod(); err != nil {
		_, unloadErr := unloadModuleForTransaction(loader, mod.Name())
		if unloadErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to unload rejected module: %w", unloadErr))
		}
		return restore(fmt.Errorf("on_dlmod hook failed for %s: %w", mod.Name(), err))
	}
	if err := mod.ClientReady(); err != nil {
		_, unloadErr := unloadModuleForTransaction(loader, mod.Name())
		if unloadErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to unload rejected module: %w", unloadErr))
		}
		return restore(fmt.Errorf("client_ready hook failed for %s: %w", mod.Name(), err))
	}
	return nil
}
