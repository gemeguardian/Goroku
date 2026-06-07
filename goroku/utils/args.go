package utils

import (
	"strconv"
	"strings"
)

func GetArgsRaw(text string) string {
	parts := strings.SplitN(text, " ", 2)
	if len(parts) > 1 {
		return parts[1]
	}
	return ""
}

func GetArgs(text string) []string {
	raw := GetArgsRaw(text)
	if raw == "" {
		return []string{}
	}
	// Simplified whitespace split representing shell splitting logic
	return strings.Fields(raw)
}

func GetArgsSplitBy(text, separator string) []string {
	raw := GetArgsRaw(text)
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, separator)
	var res []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

func GetArgsInt(text string) []int {
	args := GetArgs(text)
	var res []int
	for _, arg := range args {
		if val, err := strconv.Atoi(arg); err == nil {
			res = append(res, val)
		}
	}
	return res
}

func GetArgsBool(text string) []bool {
	args := GetArgs(text)
	var res []bool
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "true" || lower == "yes" || lower == "1" || lower == "on" {
			res = append(res, true)
		} else if lower == "false" || lower == "no" || lower == "0" || lower == "off" {
			res = append(res, false)
		}
	}
	return res
}
