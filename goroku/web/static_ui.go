package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (w *Web) getPlatformEmoji() string {
	if os.Getenv("LAVHOST") != "" {
		return "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/victory-hand_270c-fe0f.png"
	} else if os.Getenv("DOCKER") != "" {
		return "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/spouting-whale_1f433.png"
	}
	return "https://raw.githubusercontent.com/gemeguardian/Goroku/master/goroku/assets/waning-crescent-moon_1f318.png"
}

func extractBlock(tpl, blockName string) string {
	startTag := fmt.Sprintf("{%% block %s %%}", blockName)
	endTag := "{% endblock %}"
	startIdx := strings.Index(tpl, startTag)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(startTag)
	endIdx := strings.Index(tpl[startIdx:], endTag)
	if endIdx == -1 {
		return ""
	}
	return tpl[startIdx : startIdx+endIdx]
}

func replaceBlock(tpl, blockName, content string) string {
	target := fmt.Sprintf("{%% block %s %%}{%% endblock %%}", blockName)
	return strings.ReplaceAll(tpl, target, content)
}

func replaceConditional(tpl, condition string, keepTrue bool) string {
	startTag := fmt.Sprintf("{%% if %s %%}", condition)
	elseTag := "{% else %}"
	endTag := "{% endif %}"

	for {
		startIdx := strings.Index(tpl, startTag)
		if startIdx == -1 {
			break
		}

		endIdx := strings.Index(tpl[startIdx:], endTag)
		if endIdx == -1 {
			break
		}
		endIdx += startIdx

		inner := tpl[startIdx+len(startTag) : endIdx]

		elseIdx := strings.Index(inner, elseTag)
		var truePart, falsePart string
		if elseIdx != -1 {
			truePart = inner[:elseIdx]
			falsePart = inner[elseIdx+len(elseTag):]
		} else {
			truePart = inner
			falsePart = ""
		}

		replacement := falsePart
		if keepTrue {
			replacement = truePart
		}

		tpl = tpl[:startIdx] + replacement + tpl[endIdx+len(endTag):]
	}
	return tpl
}
func webResourceDir(dataRoot string) string {
	var candidates []string
	if envDir := strings.TrimSpace(os.Getenv("GOROKU_WEB_RESOURCES")); envDir != "" {
		candidates = append(candidates, envDir)
	}
	if dataRoot != "" {
		candidates = append(candidates, filepath.Join(dataRoot, "web-resources"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "web-resources"))
	}
	if execPath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(execPath), "web-resources"))
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(candidate, "base.jinja2")); err == nil {
			return candidate
		}
	}
	if dataRoot != "" {
		return filepath.Join(dataRoot, "web-resources")
	}
	return "web-resources"
}

func (w *Web) RootHandler(wr http.ResponseWriter, r *http.Request) {
	w.rememberSetupToken(wr, r)
	resourceDir := webResourceDir(w.dataRoot)
	baseBytes, err := os.ReadFile(filepath.Join(resourceDir, "base.jinja2"))
	if err != nil {
		http.Error(wr, "base template not found", http.StatusInternalServerError)
		return
	}

	rootBytes, err := os.ReadFile(filepath.Join(resourceDir, "root.jinja2"))
	if err != nil {
		http.Error(wr, "root template not found", http.StatusInternalServerError)
		return
	}

	baseStr := string(baseBytes)
	rootStr := string(rootBytes)

	headBlock := extractBlock(rootStr, "head")
	contentBlock := extractBlock(rootStr, "content")
	afterBlock := extractBlock(rootStr, "after")

	htmlContent := baseStr
	htmlContent = replaceBlock(htmlContent, "head", headBlock)
	htmlContent = replaceBlock(htmlContent, "content", contentBlock)
	htmlContent = replaceBlock(htmlContent, "after", afterBlock)

	htmlContent = strings.ReplaceAll(htmlContent, `{{ static("base.css") }}`, "static/base.css")
	htmlContent = strings.ReplaceAll(htmlContent, `{{ static("root.js") }}`, "static/root.js")

	platformEmoji := w.getPlatformEmoji()
	htmlContent = strings.ReplaceAll(htmlContent, `{{ platform_emoji }}`, platformEmoji)

	w.mu.RLock()
	apiToken := w.apiToken
	w.mu.RUnlock()
	tgDone := w.clientCount() > 0
	skipCreds := hasAPIToken(apiToken)
	lavhost := os.Getenv("LAVHOST") != ""

	if skipCreds {
		htmlContent = strings.ReplaceAll(htmlContent, `{{ skip_creds }}`, "True")
	} else {
		htmlContent = strings.ReplaceAll(htmlContent, `{{ skip_creds }}`, "False")
	}

	if !tgDone {
		htmlContent = replaceConditional(htmlContent, "not tg_done", true)
	} else {
		htmlContent = replaceConditional(htmlContent, "not tg_done", false)
	}

	htmlContent = replaceConditional(htmlContent, "skip_creds and not lavhost", skipCreds && !lavhost)

	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeString(wr, htmlContent)
}
