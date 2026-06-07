package compat

import (
	"regexp"
	"strings"
)

func Compat(code string) string {
	lines := strings.Split(code, "\n")
	var result []string

	// Recreate the Python regex cascading translations
	r1 := regexp.MustCompile(`^( *)from \.\.inline import (.+), ?rand, ?(.+)$`)
	r2 := regexp.MustCompile(`^( *)from \.\.inline import (.+), ?rand[^,]*$`)
	r3 := regexp.MustCompile(`^( *)from \.\.inline import rand, ?(.+)$`)
	r4 := regexp.MustCompile(`^( *)from \.\.inline import rand[^,]*$`)
	r5 := regexp.MustCompile(`^( *)from \.\.inline import (.+)$`)

	for _, line := range lines {
		// Clean specific references
		line = strings.ReplaceAll(line, "GeekInlineQuery", "InlineQuery")
		line = strings.ReplaceAll(line, "self.inline._bot", "self.inline.bot")

		if r1.MatchString(line) {
			line = r1.ReplaceAllString(line, "${1}from ..inline.types import ${2}, ${3}\n${1}from ..utils import rand")
		} else if r2.MatchString(line) {
			line = r2.ReplaceAllString(line, "${1}from ..inline.types import ${2}\n${1}from ..utils import rand")
		} else if r3.MatchString(line) {
			line = r3.ReplaceAllString(line, "${1}from ..inline.types import ${2}\n${1}from ..utils import rand")
		} else if r4.MatchString(line) {
			line = r4.ReplaceAllString(line, "${1}from ..utils import rand")
		} else if r5.MatchString(line) {
			line = r5.ReplaceAllString(line, "${1}from ..inline.types import ${2}")
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}
