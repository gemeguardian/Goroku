//go:build linux || android

package modules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"goroku/goroku"
	"os"
	"os/exec"
	"path/filepath"
	"plugin"
	"regexp"
	"strings"
)

func RegisterModulesHot(msg *goroku.Message, structNames []string) error {
	if msg == nil || msg.Client == nil {
		return fmt.Errorf("message client is required for hot module loading")
	}

	loader, ok := msg.Client.Loader.(*goroku.Modules)
	if !ok || loader == nil {
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

		if old := loader.LookupByName(mod.Name()); old != nil {
			if err := loader.UnloadModule(old.Name()); err != nil {
				return fmt.Errorf("failed to replace module %s: %v", mod.Name(), err)
			}
		}

		if err := loader.RegisterModule(mod); err != nil {
			return err
		}
		if err := mod.OnDlmod(); err != nil {
			return fmt.Errorf("on_dlmod hook failed for %s: %v", mod.Name(), err)
		}
		if err := mod.ClientReady(); err != nil {
			return fmt.Errorf("client_ready hook failed for %s: %v", mod.Name(), err)
		}
	}
	return nil
}

func buildAndOpenPlugin(structName string) (goroku.Module, error) {
	sourcePath, err := findModuleSource(structName)
	if err != nil {
		return nil, err
	}

	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	if err := validateHotPluginSource(structName, string(sourceBytes)); err != nil {
		return nil, err
	}

	packageRe := regexp.MustCompile(`(?m)^\s*package\s+\w+`)
	pluginSource := packageRe.ReplaceAllString(string(sourceBytes), "package main")
	if pluginSource == string(sourceBytes) {
		return nil, fmt.Errorf("module %s has no package declaration", structName)
	}

	hash := sha256.Sum256(sourceBytes)
	shortHash := hex.EncodeToString(hash[:])[:16]
	workDir := filepath.Join(goroku.BasePath, ".goroku_plugins", strings.ToLower(structName)+"_"+shortHash)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, err
	}

	moduleFile := filepath.Join(workDir, filepath.Base(sourcePath))
	wrapperFile := filepath.Join(workDir, "plugin_export.go")
	pluginFile := filepath.Join(workDir, strings.ToLower(structName)+".so")

	if err := os.WriteFile(moduleFile, []byte(pluginSource), 0644); err != nil {
		return nil, err
	}
	wrapper := fmt.Sprintf("package main\n\nimport \"goroku/goroku\"\n\nfunc NewModule() goroku.Module {\n\treturn &%s{}\n}\n", structName)
	if err := os.WriteFile(wrapperFile, []byte(wrapper), 0644); err != nil {
		return nil, err
	}

	cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", pluginFile, ".")
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("plugin build failed for %s:\n%s", structName, string(output))
	}

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

func validateHotPluginSource(structName, source string) error {
	if strings.Contains(source, "goroku: allow-unsafe") {
		return nil
	}
	dangerousImports := []string{"os/exec", "syscall", "unsafe", "plugin"}
	for _, imp := range dangerousImports {
		if regexp.MustCompile(`(?m)^\s*import\s+"`+regexp.QuoteMeta(imp)+`"`).MatchString(source) ||
			regexp.MustCompile(`(?m)^\s*"`+regexp.QuoteMeta(imp)+`"`).MatchString(source) {
			return fmt.Errorf("module %s imports %s; add '// goroku: allow-unsafe' if you trust this module", structName, imp)
		}
	}
	return nil
}
