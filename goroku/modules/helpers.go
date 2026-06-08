package modules

import (
	"fmt"
	"goroku/goroku"
	"goroku/goroku/inline"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	return fmt.Errorf("client is required for hot module loading")
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
