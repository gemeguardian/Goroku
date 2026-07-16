package goroku

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNormalizeConfigValue(t *testing.T) {
	if got := NormalizeConfigValue(float64(42)); got != int64(42) {
		t.Fatalf("float64 int normalize = %v (%T)", got, got)
	}
	if got := NormalizeConfigValue(json.Number("7")); got != int64(7) {
		t.Fatalf("json.Number normalize = %v (%T)", got, got)
	}
	if got := NormalizeConfigValue(3.5); got != 3.5 {
		t.Fatalf("non-int float must pass through, got %v", got)
	}
	in := map[string]any{"n": float64(1), "s": "x"}
	out := NormalizeConfigMap(in)
	if out["n"] != int64(1) || out["s"] != "x" {
		t.Fatalf("NormalizeConfigMap = %#v", out)
	}
}

func TestConfigErrorContext(t *testing.T) {
	err := NewConfigError("Mod", "key", errors.New("bad"))
	if err == nil || err.Error() != "config Mod.key: bad" {
		t.Fatalf("ConfigError = %v", err)
	}
	var ce *ConfigError
	if !errors.As(err, &ce) || ce.Module != "Mod" || ce.Key != "key" {
		t.Fatalf("errors.As failed: %#v", ce)
	}
}

func TestSchemaHelpers(t *testing.T) {
	fields := []ConfigField{
		{Key: "enabled", Type: "bool", Default: true, Validator: &BooleanValidator{}},
		{Key: "token", Type: "string", Default: "", Secret: true},
		{Key: "plain", Type: "string", Default: "x"},
	}
	defs := SchemaDefaults(fields)
	if defs["enabled"] != true || defs["token"] != "" || defs["plain"] != "x" {
		t.Fatalf("SchemaDefaults = %#v", defs)
	}
	vals := SchemaValidators(fields)
	if _, ok := vals["enabled"].(*BooleanValidator); !ok {
		t.Fatalf("enabled validator = %T", vals["enabled"])
	}
	if !IsSecretValidator(vals["token"]) {
		t.Fatal("token must be secret-wrapped")
	}
	secrets := SchemaSecretKeys(fields)
	if len(secrets) != 1 || secrets[0] != "token" {
		t.Fatalf("SchemaSecretKeys = %v", secrets)
	}
}

type schemaOnlyMod struct{}

func (schemaOnlyMod) Name() string                                { return "SchemaOnly" }
func (schemaOnlyMod) Strings() map[string]string                  { return nil }
func (schemaOnlyMod) Init(*CustomTelegramClient, *Database) error { return nil }
func (schemaOnlyMod) ClientReady() error                          { return nil }
func (schemaOnlyMod) OnUnload() error                             { return nil }
func (schemaOnlyMod) OnDlmod() error                              { return nil }
func (schemaOnlyMod) Commands() map[string]CommandHandler         { return nil }
func (schemaOnlyMod) Watchers() []WatcherHandler                  { return nil }
func (schemaOnlyMod) ConfigSchema() []ConfigField {
	return []ConfigField{{Key: "flag", Type: "bool", Default: true}}
}

func TestModuleHasConfigAndKeys(t *testing.T) {
	mod := schemaOnlyMod{}
	if !ModuleHasConfig(mod) {
		t.Fatal("schema-only module must report config")
	}
	keys := ModuleConfigKeys(mod)
	if keys["flag"] != "flag" {
		t.Fatalf("ModuleConfigKeys = %#v", keys)
	}
	if ModuleHasConfig(nil) {
		t.Fatal("nil module must not report config")
	}
}
