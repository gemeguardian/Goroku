package modules

import (
	"bytes"
	"context"
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"goroku/goroku/utils"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func closeForm(call inline.CallbackQuery) error {
	if call.InlineMessage != nil {
		_, err := call.InlineMessage.Delete()
		return err
	}
	if call.BotMessage != nil {
		_, err := call.BotMessage.Delete()
		return err
	}
	return nil
}


func camelToSnake(s string) string {
	var res strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			res.WriteRune('_')
		}
		res.WriteRune(r)
	}
	return strings.ToLower(res.String())
}

// getTrans fetches a translated string from the translator or returns the default value.
func getTrans(t *goroku.Translator, modName, key, def string) string {
	if t == nil {
		return def
	}

	namesToTry := []string{modName, strings.ToLower(modName), camelToSnake(modName)}
	if strings.EqualFold(modName, "APILimiter") {
		namesToTry = append(namesToTry, "api_protection")
	}
	if strings.EqualFold(modName, "Tester") {
		namesToTry = append(namesToTry, "test")
	}

	for _, name := range namesToTry {
		searchKey := fmt.Sprintf("goroku.modules.%s.%s", name, key)
		if val := t.GetKey(searchKey); val != nil {
			return fmt.Sprintf("%v", val)
		}
	}
	return def
}

func RegisterModulesAndRebuild(msg *goroku.Message, structNames []string) error {
	if msg != nil && msg.Client != nil {
		return RegisterModulesHot(msg, structNames)
	}

	_ = msg.Answer("🛠 <b>Registering modules and compiling...</b>")

	mainPath := filepath.Join(goroku.BasePath, "main.go")
	mainBytes, err := os.ReadFile(mainPath)
	if err != nil {
		return fmt.Errorf("failed to read main.go: %v", err)
	}

	mainStr := string(mainBytes)
	modified := false

	for _, structName := range structNames {
		regStr := fmt.Sprintf("&modules.%s{}", structName)
		if strings.Contains(mainStr, regStr) {
			continue
		}

		anchor := "goroku.Main([]goroku.Module{"
		idx := strings.Index(mainStr, anchor)
		if idx == -1 {
			return fmt.Errorf("could not find module registration list in main.go")
		}

		insertIdx := idx + len(anchor)
		mainStr = mainStr[:insertIdx] + "\n\t\t" + regStr + "," + mainStr[insertIdx:]
		modified = true
	}

	if !modified {
		return RebuildAndRestart(msg)
	}

	backupPath := mainPath + ".bak"
	_ = os.WriteFile(backupPath, mainBytes, 0644)
	defer os.Remove(backupPath)

	err = os.WriteFile(mainPath, []byte(mainStr), 0644)
	if err != nil {
		return fmt.Errorf("failed to update main.go: %v", err)
	}

	err = RebuildAndRestart(msg)
	if err != nil {
		_ = os.WriteFile(mainPath, mainBytes, 0644)
		return err
	}

	return nil
}

func findModuleSource(structName string) (string, error) {
	modulesDir := filepath.Join(goroku.BasePath, "goroku", "modules")
	preferred := filepath.Join(modulesDir, structName+".go")
	if _, err := os.Stat(preferred); err == nil {
		return preferred, nil
	}

	files, err := filepath.Glob(filepath.Join(modulesDir, "*.go"))
	if err != nil {
		return "", err
	}

	typeRe := regexp.MustCompile(`(?m)^\s*type\s+` + regexp.QuoteMeta(structName) + `\s+struct\b`)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		if typeRe.Match(content) {
			return file, nil
		}
	}

	return "", fmt.Errorf("source for module struct %s not found", structName)
}

func RebuildAndRestart(msg *goroku.Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	goPath := utils.GetGoPath()
	cmd := exec.CommandContext(ctx, goPath, "build", "-o", "goroku_bin")
	cmd.Dir = goroku.BasePath
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("compilation failed:\n%s", out.String())
	}

	_ = msg.Answer("✅ <b>Rebuild successful! Restarting bot...</b>")
	time.Sleep(1 * time.Second)
	goroku.Restart()
	return nil
}

func formatTrans(trans string, args ...string) string {
	res := trans
	for i, arg := range args {
		res = strings.ReplaceAll(res, fmt.Sprintf("{%d}", i), arg)
	}
	for _, arg := range args {
		res = strings.Replace(res, "{}", arg, 1)
	}
	return res
}
