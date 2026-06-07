package goroku

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

type Validator interface {
	Validate(value interface{}) (interface{}, error)
}

type BooleanValidator struct{}

func NewBooleanValidator() *BooleanValidator {
	return &BooleanValidator{}
}

func (bv *BooleanValidator) Validate(value interface{}) (interface{}, error) {
	valStr := strings.TrimSpace(strings.ToLower(fmt.Sprintf("%v", value)))
	switch valStr {
	case "true", "1", "yes", "on", "y":
		return true, nil
	case "false", "0", "no", "off", "n":
		return false, nil
	}
	return nil, &ValidationError{Message: fmt.Sprintf("Passed value (%v) must be a boolean", value)}
}

type IntegerValidator struct {
	Digits  int
	Minimum int
	Maximum int
	HasMin  bool
	HasMax  bool
	HasDig  bool
}

func NewIntegerValidator(min, max int, hasMin, hasMax bool) *IntegerValidator {
	return &IntegerValidator{
		Minimum: min,
		Maximum: max,
		HasMin:  hasMin,
		HasMax:  hasMax,
	}
}

func (iv *IntegerValidator) Validate(value interface{}) (interface{}, error) {
	valStr := strings.TrimSpace(fmt.Sprintf("%v", value))
	intVal, err := strconv.Atoi(valStr)
	if err != nil {
		return nil, &ValidationError{Message: fmt.Sprintf("Passed value (%v) must be a number", value)}
	}

	if iv.HasMin && intVal < iv.Minimum {
		return nil, &ValidationError{Message: fmt.Sprintf("Passed value (%d) is lower than minimum (%d)", intVal, iv.Minimum)}
	}

	if iv.HasMax && intVal > iv.Maximum {
		return nil, &ValidationError{Message: fmt.Sprintf("Passed value (%d) is greater than maximum (%d)", intVal, iv.Maximum)}
	}

	if iv.HasDig && len(valStr) != iv.Digits {
		return nil, &ValidationError{Message: fmt.Sprintf("The length of passed value is incorrect (Must be exactly %d digits)", iv.Digits)}
	}

	return intVal, nil
}

type ChoiceValidator struct {
	PossibleValues []interface{}
}

func NewChoiceValidator(possible []interface{}) *ChoiceValidator {
	return &ChoiceValidator{PossibleValues: possible}
}

func (cv *ChoiceValidator) Validate(value interface{}) (interface{}, error) {
	for _, possible := range cv.PossibleValues {
		if fmt.Sprintf("%v", possible) == fmt.Sprintf("%v", value) {
			return possible, nil
		}
	}
	return nil, &ValidationError{Message: fmt.Sprintf("Passed value (%v) is not in allowed choices", value)}
}

type SeriesValidator struct {
	itemValidator Validator
	MinLen        int
	MaxLen        int
	HasMin        bool
	HasMax        bool
}

func NewSeriesValidator(itemVal Validator) *SeriesValidator {
	return &SeriesValidator{itemValidator: itemVal}
}

func (sv *SeriesValidator) Validate(value interface{}) (interface{}, error) {
	var list []string
	switch v := value.(type) {
	case []string:
		list = v
	case string:
		list = strings.Split(v, ",")
	default:
		list = strings.Split(fmt.Sprintf("%v", value), ",")
	}

	if sv.HasMin && len(list) < sv.MinLen {
		return nil, &ValidationError{Message: fmt.Sprintf("Passed value contains less than %d items", sv.MinLen)}
	}

	if sv.HasMax && len(list) > sv.MaxLen {
		return nil, &ValidationError{Message: fmt.Sprintf("Passed value contains more than %d items", sv.MaxLen)}
	}

	var res []interface{}
	for _, item := range list {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if sv.itemValidator != nil {
			validated, err := sv.itemValidator.Validate(trimmed)
			if err != nil {
				return nil, err
			}
			res = append(res, validated)
		} else {
			res = append(res, trimmed)
		}
	}

	return res, nil
}

type LinkValidator struct{}

func NewLinkValidator() *LinkValidator {
	return &LinkValidator{}
}

func (lv *LinkValidator) Validate(value interface{}) (interface{}, error) {
	valStr := strings.TrimSpace(fmt.Sprintf("%v", value))
	parsed, err := url.ParseRequestURI(valStr)
	if err != nil || parsed.Host == "" {
		return nil, &ValidationError{Message: fmt.Sprintf("Passed value (%s) is not a valid URL link", valStr)}
	}
	return valStr, nil
}

type MultiChoiceValidator struct {
	Choices []interface{}
}

func (m *MultiChoiceValidator) Validate(value interface{}) (interface{}, error) {
	return value, nil
}

type StringValidator struct {
	MinLen int
	MaxLen int
}

func (s *StringValidator) Validate(value interface{}) (interface{}, error) {
	str := fmt.Sprintf("%v", value)
	if s.MinLen > 0 && len(str) < s.MinLen {
		return nil, &ValidationError{Message: "String is too short"}
	}
	if s.MaxLen > 0 && len(str) > s.MaxLen {
		return nil, &ValidationError{Message: "String is too long"}
	}
	return str, nil
}

type RegExpValidator struct {
	Pattern *regexp.Regexp
}

func (r *RegExpValidator) Validate(value interface{}) (interface{}, error) {
	str := fmt.Sprintf("%v", value)
	if !r.Pattern.MatchString(str) {
		return nil, &ValidationError{Message: "Pattern mismatch"}
	}
	return str, nil
}

type FloatValidator struct {
	Minimum float64
	Maximum float64
}

func (f *FloatValidator) Validate(value interface{}) (interface{}, error) {
	str := fmt.Sprintf("%v", value)
	val, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return nil, &ValidationError{Message: "Invalid float"}
	}
	if val < f.Minimum || val > f.Maximum {
		return nil, &ValidationError{Message: "Float out of bounds"}
	}
	return val, nil
}

type TelegramIDValidator struct{}

func (t *TelegramIDValidator) Validate(value interface{}) (interface{}, error) {
	str := fmt.Sprintf("%v", value)
	if strings.HasPrefix(str, "-100") {
		str = str[4:]
	}
	_, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return nil, &ValidationError{Message: "Invalid Telegram ID"}
	}
	return value, nil
}

type UnionValidator struct {
	Validators []Validator
}

func (u *UnionValidator) Validate(value interface{}) (interface{}, error) {
	for _, val := range u.Validators {
		if res, err := val.Validate(value); err == nil {
			return res, nil
		}
	}
	return nil, &ValidationError{Message: "Value does not match any union type"}
}

type NoneTypeValidator struct{}

func (n *NoneTypeValidator) Validate(value interface{}) (interface{}, error) {
	return nil, nil
}

type HiddenValidator struct {
	Inner Validator
}

func (h *HiddenValidator) Validate(value interface{}) (interface{}, error) {
	return h.Inner.Validate(value)
}

type EmojiValidator struct{}

func (e *EmojiValidator) Validate(value interface{}) (interface{}, error) {
	if fmt.Sprintf("%v", value) == "" {
		return nil, &ValidationError{Message: "Emoji cannot be empty"}
	}
	return value, nil
}

type EntityLikeValidator struct{}

func (e *EntityLikeValidator) Validate(value interface{}) (interface{}, error) {
	str := fmt.Sprintf("%v", value)
	if strings.HasPrefix(str, "@") || strings.HasPrefix(str, "+") {
		return str, nil
	}
	_, err := strconv.ParseInt(str, 10, 64)
	if err == nil {
		return str, nil
	}
	return nil, &ValidationError{Message: "Invalid entity identifier"}
}

type RandomLinkValidator struct {
	Links []string
}

func (r *RandomLinkValidator) Validate(value interface{}) (interface{}, error) {
	return value, nil
}

type RandomLinkListValidator struct{}

func (r *RandomLinkListValidator) Validate(value interface{}) (interface{}, error) {
	return value, nil
}

