package goroku

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxFilesize  = 1024 * 1024 * 5   // 5 MB
	MaxTotalsize = 1024 * 1024 * 100 // 100 MB
)

type LocalStorage struct {
	path string
}

func NewLocalStorage() *LocalStorage {
	homeDir, err := os.UserHomeDir()
	var path string
	if err == nil {
		path = filepath.Join(homeDir, ".goroku", "modules_cache")
	} else {
		path = filepath.Join(".", "modules_cache")
	}

	ls := &LocalStorage{path: path}
	ls.ensureDirs()
	return ls
}

func (ls *LocalStorage) totalSize() int64 {
	var size int64
	err := filepath.Walk(ls.path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0
	}
	return size
}

func (ls *LocalStorage) ensureDirs() {
	if _, err := os.Stat(ls.path); os.IsNotExist(err) {
		os.MkdirAll(ls.path, 0755)
	}
}

func (ls *LocalStorage) getPath(repo, moduleName string) string {
	hasher := sha256.New()
	hasher.Write([]byte(fmt.Sprintf("%s_%s", repo, moduleName)))
	hashStr := hex.EncodeToString(hasher.Sum(nil))
	return filepath.Join(ls.path, hashStr+".py")
}

func (ls *LocalStorage) Save(repo, moduleName, moduleCode string) {
	size := int64(len(moduleCode))
	if size > MaxFilesize {
		log.Printf("Module %s from %s is too large (%d bytes) to save to local cache.\n", moduleName, repo, size)
		return
	}

	if ls.totalSize()+size > MaxTotalsize {
		log.Printf("Local storage is full, cannot save module %s from %s.\n", moduleName, repo)
		return
	}

	filePath := ls.getPath(repo, moduleName)
	err := os.WriteFile(filePath, []byte(moduleCode), 0644)
	if err != nil {
		log.Printf("Failed to write to local storage cache: %v\n", err)
		return
	}
	log.Printf("Saved module %s from %s to local cache.\n", moduleName, repo)
}

func (ls *LocalStorage) Fetch(repo, moduleName string) (string, error) {
	filePath := ls.getPath(repo, moduleName)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

type RemoteStorage struct {
	localStorage *LocalStorage
	client       *CustomTelegramClient
}

func NewRemoteStorage(client *CustomTelegramClient) *RemoteStorage {
	return &RemoteStorage{
		localStorage: NewLocalStorage(),
		client:       client,
	}
}

func (rs *RemoteStorage) parseURL(url string) (string, string, string) {
	// Simple domain URL parser mirroring python's _parse_url logic
	parts := strings.Split(url, "/")
	if len(parts) < 3 {
		return url, url, "unknown"
	}
	domainName := parts[2]

	var repo, moduleName string
	switch domainName {
	case "raw.githubusercontent.com":
		if len(parts) >= 6 {
			owner := parts[3]
			repoName := parts[4]
			branch := parts[5]
			moduleName = strings.Split(parts[len(parts)-1], ".")[0]
			repo = fmt.Sprintf("git+%s/%s:%s", owner, repoName, branch)
		} else {
			repo = url
			moduleName = "unknown"
		}
	case "github.com":
		if len(parts) >= 7 {
			owner := parts[3]
			repoName := parts[4]
			branch := parts[6]
			moduleName = strings.Split(parts[len(parts)-1], ".")[0]
			repo = fmt.Sprintf("git+%s/%s:%s", owner, repoName, branch)
		} else {
			repo = url
			moduleName = "unknown"
		}
	default:
		idx := strings.LastIndex(url, "/")
		if idx != -1 {
			repo = url[:idx]
			moduleName = strings.Split(url[idx+1:], ".")[0]
		} else {
			repo = url
			moduleName = "unknown"
		}
	}

	return url, repo, moduleName
}

func (rs *RemoteStorage) Fetch(url, auth string) (string, error) {
	parsedURL, repo, moduleName := rs.parseURL(url)

	req, err := http.NewRequest("GET", parsedURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Goroku Userbot")
	req.Header.Set("X-Goroku-Version", "2.0.0")
	req.Header.Set("X-Goroku-User", fmt.Sprintf("%d", rs.client.TGID))

	if auth != "" {
		parts := strings.SplitN(auth, ":", 2)
		if len(parts) == 2 {
			req.SetBasicAuth(parts[0], parts[1])
		}
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)

	if err != nil || resp.StatusCode != http.StatusOK {
		log.Printf("Can't load module from remote storage. Trying local storage: %v\n", err)
		if localCode, fetchErr := rs.localStorage.Fetch(repo, moduleName); fetchErr == nil {
			log.Println("Module source loaded from local storage.")
			return localCode, nil
		}
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("remote server returned status: %s", resp.Status)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	moduleCode := string(bodyBytes)
	rs.localStorage.Save(repo, moduleName, moduleCode)

	return moduleCode, nil
}
