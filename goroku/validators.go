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
	Validate(value any) (any, error)
}

type BooleanValidator struct{}

func (v *BooleanValidator) Validate(value any) (any, error) {
	switch val := value.(type) {
	case bool:
		return val, nil
	case string:
		if strings.EqualFold(val, "true") {
			return true, nil
		} else if strings.EqualFold(val, "false") {
			return false, nil
		}
	case int:
		return val != 0, nil
	case int64:
		return val != 0, nil
	case float64:
		return val != 0, nil
	}
	return false, &ValidationError{Message: "Value must be a boolean"}
}

type IntegerValidator struct {
	Minimum int64
	Maximum int64
	HasMin  bool
	HasMax  bool
}

func (v *IntegerValidator) Validate(value any) (any, error) {
	var i int64
	switch val := value.(type) {
	case int:
		i = int64(val)
	case int64:
		i = val
	case float64:
		i = int64(val)
	case string:
		var err error
		i, err = strconv.ParseInt(val, 10, 64)
		if err != nil {
			return 0, &ValidationError{Message: "Value must be an integer"}
		}
	default:
		return 0, &ValidationError{Message: "Value must be an integer"}
	}
	if v.HasMin && i < v.Minimum {
		return 0, &ValidationError{Message: fmt.Sprintf("Value must be >= %d", v.Minimum)}
	}
	if v.HasMax && i > v.Maximum {
		return 0, &ValidationError{Message: fmt.Sprintf("Value must be <= %d", v.Maximum)}
	}
	return i, nil
}

type StringValidator struct {
	MinLen int
	MaxLen int
}

func (v *StringValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	runes := []rune(str)
	if v.MinLen > 0 && len(runes) < v.MinLen {
		return "", &ValidationError{Message: fmt.Sprintf("Value must be at least %d characters", v.MinLen)}
	}
	if v.MaxLen > 0 && len(runes) > v.MaxLen {
		return "", &ValidationError{Message: fmt.Sprintf("Value must be at most %d characters", v.MaxLen)}
	}
	return str, nil
}

type FloatValidator struct {
	Minimum float64
	Maximum float64
	HasMin  bool
	HasMax  bool
}

func (v *FloatValidator) Validate(value any) (any, error) {
	var f float64
	switch val := value.(type) {
	case float64:
		f = val
	case int:
		f = float64(val)
	case int64:
		f = float64(val)
	case string:
		var err error
		f, err = strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, &ValidationError{Message: "Value must be a float"}
		}
	default:
		return 0, &ValidationError{Message: "Value must be a float"}
	}
	if v.HasMin && f < v.Minimum {
		return 0, &ValidationError{Message: fmt.Sprintf("Value must be >= %f", v.Minimum)}
	}
	if v.HasMax && f > v.Maximum {
		return 0, &ValidationError{Message: fmt.Sprintf("Value must be <= %f", v.Maximum)}
	}
	return f, nil
}

type URLValidator struct{}

func (v *URLValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	if str == "" {
		return "", &ValidationError{Message: "URL cannot be empty"}
	}
	if _, err := url.ParseRequestURI(str); err != nil {
		return "", &ValidationError{Message: "Invalid URL"}
	}
	return str, nil
}

type LinkValidator struct{}

func (v *LinkValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	if str == "" {
		return "", &ValidationError{Message: "Link cannot be empty"}
	}
	u, err := url.ParseRequestURI(str)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", &ValidationError{Message: "Invalid link"}
	}
	return str, nil
}

type RegExpValidator struct {
	Pattern *regexp.Regexp
}

func (v *RegExpValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	if v.Pattern == nil {
		return str, nil
	}
	if !v.Pattern.MatchString(str) {
		return "", &ValidationError{Message: fmt.Sprintf("Value does not match pattern %s", v.Pattern.String())}
	}
	return str, nil
}

type HiddenValidator struct {
	Inner Validator
}

func (v *HiddenValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	if v.Inner != nil {
		if _, err := v.Inner.Validate(value); err != nil {
			return "", err
		}
	}
	return str, nil
}

type ChoiceValidator struct {
	PossibleValues []string
}

func (v *ChoiceValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	for _, pv := range v.PossibleValues {
		if pv == str {
			return str, nil
		}
	}
	return "", &ValidationError{Message: fmt.Sprintf("Value must be one of: %s", strings.Join(v.PossibleValues, ", "))}
}

type SeriesValidator struct {
	MinLen int
	HasMin bool
	Item   Validator
}

func (v *SeriesValidator) Validate(value any) (any, error) {
	var items []string
	switch val := value.(type) {
	case []string:
		items = val
	case []any:
		items = make([]string, len(val))
		for i, item := range val {
			items[i] = fmt.Sprintf("%v", item)
		}
	case string:
		parts := strings.Split(val, ",")
		items = make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				items = append(items, trimmed)
			}
		}
	default:
		return nil, &ValidationError{Message: "Value must be a series (list of strings)"}
	}
	if v.HasMin && len(items) < v.MinLen {
		return nil, &ValidationError{Message: fmt.Sprintf("Series must have at least %d items", v.MinLen)}
	}
	if v.Item != nil {
		validated := make([]any, len(items))
		for i, item := range items {
			res, err := v.Item.Validate(item)
			if err != nil {
				return nil, err
			}
			validated[i] = res
		}
		return validated, nil
	}
	return items, nil
}

type TelegramIDValidator struct{}

func (v *TelegramIDValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	if str == "" {
		return "", &ValidationError{Message: "Telegram ID cannot be empty"}
	}
	if _, err := strconv.ParseInt(str, 10, 64); err != nil {
		return "", &ValidationError{Message: "Invalid Telegram ID"}
	}
	return str, nil
}

type UnionValidator struct {
	Validators []Validator
}

func (v *UnionValidator) Validate(value any) (any, error) {
	var lastErr error
	for _, validator := range v.Validators {
		res, err := validator.Validate(value)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, &ValidationError{Message: "Value does not match any validator"}
}

type NoneTypeValidator struct{}

func (v *NoneTypeValidator) Validate(value any) (any, error) {
	if value != nil {
		return nil, &ValidationError{Message: "Value must be nil"}
	}
	return nil, nil
}

type EmojiValidator struct{}

func (e *EmojiValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	if str == "" {
		return "", &ValidationError{Message: "Emoji cannot be empty"}
	}
	return str, nil
}

type EntityLikeValidator struct{}

func (v *EntityLikeValidator) Validate(value any) (any, error) {
	str := fmt.Sprintf("%v", value)
	if str == "" {
		return "", &ValidationError{Message: "Entity-like value cannot be empty"}
	}
	if str[0] == '@' || str[0] == '+' || (str[0] >= '0' && str[0] <= '9') {
		return str, nil
	}
	return "", &ValidationError{Message: "Value must be an entity-like identifier (username, phone, or numeric ID)"}
}

func unwrapValidator(v Validator) Validator {
	if hv, ok := v.(*HiddenValidator); ok && hv.Inner != nil {
		return hv.Inner
	}
	return v
}

func NewBooleanValidator() *BooleanValidator {
	return &BooleanValidator{}
}

func NewIntegerValidator(min, max int64, hasMin, hasMax bool) *IntegerValidator {
	return &IntegerValidator{Minimum: min, Maximum: max, HasMin: hasMin, HasMax: hasMax}
}

func NewChoiceValidator(values any) *ChoiceValidator {
	var strs []string
	switch v := values.(type) {
	case []string:
		strs = v
	case []any:
		strs = make([]string, len(v))
		for i, val := range v {
			strs[i] = fmt.Sprintf("%v", val)
		}
	}
	return &ChoiceValidator{PossibleValues: strs}
}

func NewLinkValidator() *LinkValidator {
	return &LinkValidator{}
}

func NewSeriesValidator(item Validator) *SeriesValidator {
	return &SeriesValidator{Item: item}
}
