package goroku

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ConfigField describes one module configuration option (M7 typed schema).
// Modules may implement ModuleWithConfigSchema; ConfigDefaults/ConfigValidators
// remain supported for backward compatibility.
type ConfigField struct {
	Key       string
	Type      string // "bool", "int", "string", "float", "choice", "series", "url", "link", "hidden"
	Default   any
	Validator Validator
	Secret    bool // true when the value must be redacted in logs/backups
}

// ModuleWithConfigSchema is the preferred config surface: type, default, validator, secret.
type ModuleWithConfigSchema interface {
	ConfigSchema() []ConfigField
}

// ModuleHasConfig reports whether mod exposes configuration via schema and/or defaults.
func ModuleHasConfig(mod Module) bool {
	if mod == nil {
		return false
	}
	if _, ok := mod.(ModuleWithConfigSchema); ok {
		return true
	}
	if _, ok := mod.(ModuleWithConfig); ok {
		return true
	}
	if _, ok := mod.(ModuleWithConfigValidators); ok {
		return true
	}
	return false
}

// ModuleConfigKeys returns canonical option keys (lowercased map key → original key).
func ModuleConfigKeys(mod Module) map[string]string {
	out := make(map[string]string)
	if mod == nil {
		return out
	}
	if withSchema, ok := mod.(ModuleWithConfigSchema); ok {
		for _, field := range withSchema.ConfigSchema() {
			if field.Key == "" {
				continue
			}
			out[strings.ToLower(field.Key)] = field.Key
		}
	}
	if withConfig, ok := mod.(ModuleWithConfig); ok {
		for k := range withConfig.ConfigDefaults() {
			if k == "" {
				continue
			}
			if _, exists := out[strings.ToLower(k)]; !exists {
				out[strings.ToLower(k)] = k
			}
		}
	}
	if withValidators, ok := mod.(ModuleWithConfigValidators); ok {
		for k := range withValidators.ConfigValidators() {
			if k == "" {
				continue
			}
			if _, exists := out[strings.ToLower(k)]; !exists {
				out[strings.ToLower(k)] = k
			}
		}
	}
	return out
}

// ConfigError carries module/key context for configuration failures.
type ConfigError struct {
	Module string
	Key    string
	Err    error
}

func (e *ConfigError) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Module != "" && e.Key != "":
		return fmt.Sprintf("config %s.%s: %v", e.Module, e.Key, e.Err)
	case e.Module != "":
		return fmt.Sprintf("config %s: %v", e.Module, e.Err)
	default:
		return fmt.Sprintf("config: %v", e.Err)
	}
}

func (e *ConfigError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewConfigError builds a ConfigError with module/key context.
func NewConfigError(module, key string, err error) error {
	if err == nil {
		return nil
	}
	return &ConfigError{Module: module, Key: key, Err: err}
}

// IsSecretValidator reports whether v marks a secret-bearing option.
func IsSecretValidator(v Validator) bool {
	_, ok := v.(*HiddenValidator)
	return ok
}

// SchemaDefaults converts a schema into the ConfigDefaults map shape.
func SchemaDefaults(fields []ConfigField) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if f.Key == "" {
			continue
		}
		out[f.Key] = f.Default
	}
	return out
}

// SchemaValidators converts a schema into the ConfigValidators map shape.
// Secret fields without an explicit validator wrap a StringValidator in HiddenValidator.
func SchemaValidators(fields []ConfigField) map[string]Validator {
	out := make(map[string]Validator, len(fields))
	for _, f := range fields {
		if f.Key == "" {
			continue
		}
		v := f.Validator
		if f.Secret {
			if v == nil {
				v = &StringValidator{}
			}
			if _, ok := v.(*HiddenValidator); !ok {
				v = &HiddenValidator{Inner: v}
			}
		}
		if v != nil {
			out[f.Key] = v
		}
	}
	return out
}

// SchemaSecretKeys returns keys marked secret in the schema (or via HiddenValidator).
func SchemaSecretKeys(fields []ConfigField) []string {
	var keys []string
	for _, f := range fields {
		if f.Key == "" {
			continue
		}
		if f.Secret || IsSecretValidator(f.Validator) {
			keys = append(keys, f.Key)
		}
	}
	return keys
}

// NormalizeConfigValue centralizes JSON-decoded config value normalization.
// float64 / json.Number integers become int64; other values pass through.
func NormalizeConfigValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return string(v)
	case float64:
		if v == float64(int64(v)) {
			return int64(v)
		}
		return v
	case float32:
		f := float64(v)
		if f == float64(int64(f)) {
			return int64(f)
		}
		return f
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = NormalizeConfigValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = NormalizeConfigValue(item)
		}
		return out
	default:
		return value
	}
}

// NormalizeConfigMap normalizes every value in a module config map.
func NormalizeConfigMap(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	out := make(map[string]any, len(config))
	for k, v := range config {
		out[k] = NormalizeConfigValue(v)
	}
	return out
}
