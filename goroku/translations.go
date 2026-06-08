package goroku

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	SupportedLanguages = map[string]string{
		"en": "🇬🇧 English",
		"ru": "🇷🇺 Русский",
		"ua": "🇺🇦 Український",
		"de": "🇩🇪 Deutsch",
		"jp": "🇯🇵 日本語",
	}
	MemeLanguages = map[string]string{
		"leet":   "🏴‍☠️ 1337",
		"uwu":    "🏴‍☠️ UwU",
		"uwu_ru": "🏴‍☠️ UwU(Ru)",
		"tiktok": "🏴‍☠️ TikTokKid",
		"neofit": "🏴‍☠️ Neofit",
	}
)

func FormatString(text string, kwargs map[string]interface{}) string {
	for k, v := range kwargs {
		placeholder := fmt.Sprintf("{%s}", k)
		text = strings.ReplaceAll(text, placeholder, fmt.Sprintf("%v", v))
	}
	return text
}

type BaseTranslator struct{}

func (bt *BaseTranslator) getPackRaw(content string, suffix string, prefix string) (map[string]interface{}, error) {
	parsed := make(map[string]interface{})
	var err error

	if suffix == ".json" {
		err = json.Unmarshal([]byte(content), &parsed)
	} else {
		content = strings.ReplaceAll(content, `<\/`, `</`)
		err = yaml.Unmarshal([]byte(content), &parsed)
	}

	if err != nil {
		return nil, err
	}

	res := make(map[string]interface{})
	for module, stringsMap := range parsed {
		if stringsMapVal, ok := stringsMap.(map[string]interface{}); ok {
			for key, val := range stringsMapVal {
				if key != "name" {
					resolvedKey := fmt.Sprintf("%s%s.%s", prefix, strings.TrimPrefix(module, "$"), key)
					res[resolvedKey] = val
				}
			}
		}
	}

	return res, nil
}

type Translator struct {
	mu      sync.RWMutex
	client  *CustomTelegramClient
	db      *Database
	data    map[string]interface{}
	rawData map[string]map[string]interface{}
	packs   string
}

var (
	globalTranslator   *Translator
	globalTranslatorMu sync.Mutex
)

func NewTranslator(client *CustomTelegramClient, db *Database) *Translator {
	globalTranslatorMu.Lock()
	defer globalTranslatorMu.Unlock()

	if globalTranslator == nil {
		execPath, err := os.Executable()
		var baseDir string
		if err == nil {
			baseDir = filepath.Dir(execPath)
		} else {
			baseDir = "."
		}

		packsDir := filepath.Join(baseDir, "langpacks")
		if _, err := os.Stat(filepath.Join(baseDir, "goroku", "langpacks")); err == nil {
			packsDir = filepath.Join(baseDir, "goroku", "langpacks")
		} else if _, err := os.Stat(filepath.Join(baseDir, "langpacks")); err != nil {
			packsDir = filepath.Join("goroku", "langpacks")
		}

		globalTranslator = &Translator{
			client:  client,
			db:      db,
			data:    make(map[string]interface{}),
			rawData: make(map[string]map[string]interface{}),
			packs:   packsDir,
		}
	}
	return globalTranslator
}

func (t *Translator) Init() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	bt := &BaseTranslator{}

	// Load English as base lang
	enPackPath := filepath.Join(t.packs, "en.yml")
	contentBytes, err := os.ReadFile(enPackPath)
	if err != nil {
		contentBytes, err = os.ReadFile(filepath.Join("goroku", "langpacks", "en.yml"))
	}
	if err != nil {
		contentBytes, err = os.ReadFile(filepath.Join("langpacks", "en.yml"))
	}

	if err == nil {
		parsed, err := bt.getPackRaw(string(contentBytes), ".yml", "goroku.modules.")
		if err == nil {
			t.data = parsed
			t.rawData["en"] = parsed
		}
	}

	// Try custom config lang
	langVal := t.db.Get("goroku.translations", "lang", "en")
	if langStr, ok := langVal.(string); ok {
		for _, language := range strings.Fields(langStr) {
			if strings.HasPrefix(language, "http://") || strings.HasPrefix(language, "https://") {
				// Fetch remote YAML / JSON
				httpClient := &http.Client{Timeout: 10 * time.Second}
				resp, err := httpClient.Get(language)
				if err == nil && resp.StatusCode == http.StatusOK {
					bodyBytes, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err == nil {
						suffix := ".yml"
						if strings.HasSuffix(language, ".json") {
							suffix = ".json"
						}
						parsed, err := bt.getPackRaw(string(bodyBytes), suffix, "goroku.modules.")
						if err == nil {
							for k, v := range parsed {
								t.data[k] = v
							}
							t.rawData[language] = parsed
						}
					}
				}
			} else {
				// Search local path
				for _, ext := range []string{".json", ".yml"} {
					localPath := filepath.Join(t.packs, language+ext)
					contentBytes, err := os.ReadFile(localPath)
					if err != nil {
						contentBytes, err = os.ReadFile(filepath.Join("goroku", "langpacks", language+ext))
					}
					if err != nil {
						contentBytes, err = os.ReadFile(filepath.Join("langpacks", language+ext))
					}
					if err == nil {
						parsed, err := bt.getPackRaw(string(contentBytes), ext, "goroku.modules.")
						if err == nil {
							for k, v := range parsed {
								t.data[k] = v
							}
							t.rawData[language] = parsed
						}
					}
				}
			}
		}
	}

	return len(t.data) > 0
}

func (t *Translator) GetKey(key string) interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.data[key]
}

func (t *Translator) HasRawData(lang string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.rawData[lang]
	return ok
}


type Strings struct {
	mod        Module
	translator *Translator
}

func NewStrings(mod Module, translator *Translator) *Strings {
	return &Strings{
		mod:        mod,
		translator: translator,
	}
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

func (s *Strings) Get(key string) string {
	if s.translator == nil {
		if val, exists := s.mod.Strings()[key]; exists {
			return val
		}
		return "Unknown string"
	}

	namesToTry := []string{s.mod.Name(), strings.ToLower(s.mod.Name()), camelToSnake(s.mod.Name())}
	if strings.EqualFold(s.mod.Name(), "APILimiter") {
		namesToTry = append(namesToTry, "api_protection")
	}
	if strings.EqualFold(s.mod.Name(), "Tester") {
		namesToTry = append(namesToTry, "test")
	}

	for _, name := range namesToTry {
		searchKey := fmt.Sprintf("goroku.modules.%s.%s", name, key)
		if val := s.translator.GetKey(searchKey); val != nil {
			return fmt.Sprintf("%v", val)
		}
	}

	if val, exists := s.mod.Strings()[key]; exists {
		return val
	}

	return "Unknown string"
}
