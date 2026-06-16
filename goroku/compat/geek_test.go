package compat

import (
	"testing"
)

func TestCompat(t *testing.T) {
	// Test python inline queries replacement
	input1 := "from ..inline import GeekInlineQuery, rand, other"
	got1 := Compat(input1)
	expected1 := "from ..inline.types import InlineQuery, other\nfrom ..utils import rand"
	if got1 != expected1 {
		t.Errorf("Expected:\n%q\nGot:\n%q", expected1, got1)
	}

	// Test case without rand
	input2 := "from ..inline import InlineQuery"
	got2 := Compat(input2)
	expected2 := "from ..inline.types import InlineQuery"
	if got2 != expected2 {
		t.Errorf("Expected:\n%q\nGot:\n%q", expected2, got2)
	}

	// Test self.inline._bot replacement
	input3 := "self.inline._bot.send_message()"
	got3 := Compat(input3)
	expected3 := "self.inline.bot.send_message()"
	if got3 != expected3 {
		t.Errorf("Expected:\n%q\nGot:\n%q", expected3, got3)
	}
}
