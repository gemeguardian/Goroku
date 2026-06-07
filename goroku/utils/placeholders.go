package utils

import (
	"fmt"
	"strings"
	"sync"
)

type PlaceholderData struct {
	ModuleName      string
	Callback        func() string
	Description     string
	PlaceholderName string
}

var (
	mu                 sync.RWMutex
	CustomPlaceholders = make(map[string]PlaceholderData)
)

func RegisterPlaceholder(placeholder string, callback func() string, moduleName, description string) bool {
	mu.Lock()
	defer mu.Unlock()

	CustomPlaceholders[placeholder] = PlaceholderData{
		ModuleName:      moduleName,
		Callback:        callback,
		Description:     description,
		PlaceholderName: placeholder,
	}
	return true
}

func UnregisterPlaceholders(moduleName string) bool {
	mu.Lock()
	defer mu.Unlock()

	for k, v := range CustomPlaceholders {
		if v.ModuleName == moduleName {
			delete(CustomPlaceholders, k)
		}
	}
	return true
}

func GetPlaceholder(placeholder string) string {
	mu.RLock()
	defer mu.RUnlock()

	if data, exists := CustomPlaceholders[placeholder]; exists && data.Callback != nil {
		return data.Callback()
	}
	return ""
}

func FormatPlaceholders(message string) string {
	mu.RLock()
	defer mu.RUnlock()

	for k, v := range CustomPlaceholders {
		target := fmt.Sprintf("{%s}", k)
		if strings.Contains(message, target) && v.Callback != nil {
			message = strings.ReplaceAll(message, target, v.Callback())
		}
	}
	return message
}

func HelpPlaceholders(moduleName string, commandEmoji string) []string {
	mu.RLock()
	defer mu.RUnlock()

	var result []string
	for name, data := range CustomPlaceholders {
		if data.ModuleName == moduleName {
			desc := "No docs"
			if data.Description != "" {
				desc = data.Description
			}
			result = append(result, fmt.Sprintf("%s {%s} - %s", commandEmoji, name, desc))
		}
	}
	return result
}
