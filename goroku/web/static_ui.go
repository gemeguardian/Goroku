package web

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// staticContentTypes pins Content-Type for embedded static assets so they are
// served deterministically regardless of the host MIME registry.
var staticContentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "application/javascript; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".svg":   "image/svg+xml",
	".ttf":   "font/ttf",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// getPlatformEmoji returns a same-origin static path for the platform badge.
// R4.1: previously pointed at raw.githubusercontent.com; the PNGs are now
// vendored under assets/static and embedded into the binary.
func (w *Web) getPlatformEmoji() string {
	if os.Getenv("LAVHOST") != "" {
		return "static/platform-victory.png"
	} else if os.Getenv("DOCKER") != "" {
		return "static/platform-whale.png"
	}
	return "static/platform-moon.png"
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

// readResource returns a template/resource by name, preferring a disk override
// under the resolved web resource directory and falling back to the embedded
// assets tree so onboarding renders with no on-disk resources installed.
func (w *Web) readResource(name string) ([]byte, error) {
	if data, err := os.ReadFile(filepath.Join(webResourceDir(w.dataRoot), name)); err == nil {
		return data, nil
	}
	return embeddedAssets.ReadFile(path.Join("assets", name))
}

// staticHandler serves /static/* from disk when the operator ships an override
// directory, otherwise from the embedded assets. R4.1: keeps the onboarding
// panel offline (no CDN) while preserving the GOROKU_WEB_RESOURCES override.
func (w *Web) staticHandler() http.Handler {
	diskStaticDir := filepath.Join(webResourceDir(w.dataRoot), "static")
	return http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/static/")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" || strings.Contains(rel, "..") {
			http.NotFound(wr, r)
			return
		}
		if info, err := os.Stat(filepath.Join(diskStaticDir, rel)); err == nil && !info.IsDir() {
			http.ServeFile(wr, r, filepath.Join(diskStaticDir, rel))
			return
		}
		data, err := embeddedAssets.ReadFile(path.Join("assets/static", rel))
		if err != nil {
			http.NotFound(wr, r)
			return
		}
		ct, ok := staticContentTypes[strings.ToLower(filepath.Ext(rel))]
		if !ok {
			ct = http.DetectContentType(data)
		}
		wr.Header().Set("Content-Type", ct)
		wr.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeContent(wr, r, rel, time.Time{}, bytes.NewReader(data))
	})
}

func (w *Web) RootHandler(wr http.ResponseWriter, r *http.Request) {
	w.rememberSetupToken(wr, r)

	baseBytes, err := w.readResource("base.jinja2")
	if err != nil {
		http.Error(wr, "base template not found", http.StatusInternalServerError)
		return
	}

	rootBytes, err := w.readResource("root.jinja2")
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
