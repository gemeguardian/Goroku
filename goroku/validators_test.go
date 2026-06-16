package goroku

import (
	"fmt"
	"regexp"
	"testing"
)

func TestBooleanValidator(t *testing.T) {
	v := NewBooleanValidator()

	for _, tc := range []struct {
		input    interface{}
		expected bool
		err      bool
	}{
		{"true", true, false},
		{"1", true, false},
		{"yes", true, false},
		{"on", true, false},
		{"y", true, false},
		{"false", false, false},
		{"0", false, false},
		{"no", false, false},
		{"off", false, false},
		{"n", false, false},
		{"invalid", false, true},
		{123, false, true},
	} {
		res, err := v.Validate(tc.input)
		if tc.err {
			if err == nil {
				t.Errorf("Expected error for input %v, got nil", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input %v: %v", tc.input, err)
			}
			if res != tc.expected {
				t.Errorf("Expected %v for input %v, got %v", tc.expected, tc.input, res)
			}
		}
	}
}

func TestIntegerValidator(t *testing.T) {
	v := NewIntegerValidator(10, 20, true, true)

	for _, tc := range []struct {
		input    interface{}
		expected int
		err      bool
	}{
		{"15", 15, false},
		{"10", 10, false},
		{"20", 20, false},
		{"9", 0, true},
		{"21", 0, true},
		{"abc", 0, true},
	} {
		res, err := v.Validate(tc.input)
		if tc.err {
			if err == nil {
				t.Errorf("Expected error for input %v, got nil", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input %v: %v", tc.input, err)
			}
			if res != tc.expected {
				t.Errorf("Expected %v for input %v, got %v", tc.expected, tc.input, res)
			}
		}
	}
}

func TestChoiceValidator(t *testing.T) {
	v := NewChoiceValidator([]interface{}{"apple", "banana", 42})

	for _, tc := range []struct {
		input    interface{}
		expected interface{}
		err      bool
	}{
		{"apple", "apple", false},
		{42, 42, false},
		{"orange", nil, true},
	} {
		res, err := v.Validate(tc.input)
		if tc.err {
			if err == nil {
				t.Errorf("Expected error for input %v, got nil", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input %v: %v", tc.input, err)
			}
			if fmt.Sprintf("%v", res) != fmt.Sprintf("%v", tc.expected) {
				t.Errorf("Expected %v for input %v, got %v", tc.expected, tc.input, res)
			}
		}
	}
}

func TestLinkValidator(t *testing.T) {
	v := NewLinkValidator()

	for _, tc := range []struct {
		input interface{}
		err   bool
	}{
		{"https://google.com", false},
		{"http://example.org/path?query=1", false},
		{"invalid-url", true},
		{"", true},
	} {
		_, err := v.Validate(tc.input)
		if tc.err && err == nil {
			t.Errorf("Expected error for link %v, got nil", tc.input)
		}
		if !tc.err && err != nil {
			t.Errorf("Unexpected error for link %v: %v", tc.input, err)
		}
	}
}

func TestSeriesValidator(t *testing.T) {
	itemVal := NewIntegerValidator(1, 10, true, true)
	v := NewSeriesValidator(itemVal)
	v.MinLen = 2
	v.HasMin = true

	res, err := v.Validate("3, 5, 7")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	slice, ok := res.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", res)
	}
	if len(slice) != 3 || slice[0] != 3 || slice[1] != 5 || slice[2] != 7 {
		t.Errorf("Unexpected result: %v", slice)
	}

	// Test short series error
	_, err = v.Validate("3")
	if err == nil {
		t.Error("Expected error for too short series, got nil")
	}
}

func TestStringValidator(t *testing.T) {
	v := &StringValidator{MinLen: 3, MaxLen: 6}

	for _, tc := range []struct {
		input interface{}
		err   bool
	}{
		{"abc", false},
		{"abcdef", false},
		{"ab", true},
		{"abcdefg", true},
	} {
		_, err := v.Validate(tc.input)
		if tc.err && err == nil {
			t.Errorf("Expected error for string %v, got nil", tc.input)
		}
		if !tc.err && err != nil {
			t.Errorf("Unexpected error for string %v: %v", tc.input, err)
		}
	}
}

func TestRegExpValidator(t *testing.T) {
	v := &RegExpValidator{Pattern: regexp.MustCompile(`^\d{3}$`)}

	for _, tc := range []struct {
		input interface{}
		err   bool
	}{
		{"123", false},
		{"12a", true},
		{"12", true},
	} {
		_, err := v.Validate(tc.input)
		if tc.err && err == nil {
			t.Errorf("Expected error for regex %v, got nil", tc.input)
		}
		if !tc.err && err != nil {
			t.Errorf("Unexpected error for regex %v: %v", tc.input, err)
		}
	}
}

func TestFloatValidator(t *testing.T) {
	v := &FloatValidator{Minimum: 1.5, Maximum: 5.5}

	for _, tc := range []struct {
		input    interface{}
		expected float64
		err      bool
	}{
		{"3.14", 3.14, false},
		{4, 4.0, false},
		{"1.4", 0, true},
		{"5.6", 0, true},
		{"abc", 0, true},
	} {
		res, err := v.Validate(tc.input)
		if tc.err {
			if err == nil {
				t.Errorf("Expected error for input %v, got nil", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error for input %v: %v", tc.input, err)
			}
			if res != tc.expected {
				t.Errorf("Expected %f, got %f", tc.expected, res)
			}
		}
	}
}

func TestTelegramIDValidator(t *testing.T) {
	v := &TelegramIDValidator{}

	for _, tc := range []struct {
		input interface{}
		err   bool
	}{
		{"12345", false},
		{"-100123456789", false},
		{"abc", true},
	} {
		_, err := v.Validate(tc.input)
		if tc.err && err == nil {
			t.Errorf("Expected error for telegram ID %v, got nil", tc.input)
		}
		if !tc.err && err != nil {
			t.Errorf("Unexpected error for telegram ID %v: %v", tc.input, err)
		}
	}
}

func TestUnionValidator(t *testing.T) {
	v := &UnionValidator{
		Validators: []Validator{
			&IntegerValidator{Minimum: 1, Maximum: 5, HasMin: true, HasMax: true},
			&BooleanValidator{},
		},
	}

	for _, tc := range []struct {
		input interface{}
		err   bool
	}{
		{"3", false},
		{"true", false},
		{"6", true},
		{"abc", true},
	} {
		_, err := v.Validate(tc.input)
		if tc.err && err == nil {
			t.Errorf("Expected error for union input %v, got nil", tc.input)
		}
		if !tc.err && err != nil {
			t.Errorf("Unexpected error for union input %v: %v", tc.input, err)
		}
	}
}

func TestNoneTypeValidator(t *testing.T) {
	v := &NoneTypeValidator{}
	res, err := v.Validate("anything")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if res != nil {
		t.Errorf("Expected nil output, got %v", res)
	}
}

func TestHiddenValidator(t *testing.T) {
	v := &HiddenValidator{Inner: &BooleanValidator{}}
	res, err := v.Validate("true")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res != true {
		t.Errorf("Expected true, got %v", res)
	}
}

func TestEmojiValidator(t *testing.T) {
	v := &EmojiValidator{}
	res, err := v.Validate("👍")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if res != "👍" {
		t.Errorf("Expected 👍, got %v", res)
	}

	_, err = v.Validate("")
	if err == nil {
		t.Error("Expected error for empty emoji, got nil")
	}
}

func TestEntityLikeValidator(t *testing.T) {
	v := &EntityLikeValidator{}

	for _, tc := range []struct {
		input interface{}
		err   bool
	}{
		{"@username", false},
		{"+12345678", false},
		{"98765432", false},
		{"justText", true},
	} {
		_, err := v.Validate(tc.input)
		if tc.err && err == nil {
			t.Errorf("Expected error for entity like input %v, got nil", tc.input)
		}
		if !tc.err && err != nil {
			t.Errorf("Unexpected error for entity like input %v: %v", tc.input, err)
		}
	}
}

func TestMultiChoiceValidator(t *testing.T) {
	v := &MultiChoiceValidator{}
	res, err := v.Validate("test")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if res != "test" {
		t.Errorf("Expected test, got %v", res)
	}
}

func TestRandomLinkValidators(t *testing.T) {
	v1 := &RandomLinkValidator{}
	res1, err := v1.Validate("link1")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if res1 != "link1" {
		t.Errorf("Expected link1, got %v", res1)
	}

	v2 := &RandomLinkListValidator{}
	res2, err := v2.Validate("linkList")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if res2 != "linkList" {
		t.Errorf("Expected linkList, got %v", res2)
	}
}
