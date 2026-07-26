package goroku

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateConfigBytes(t *testing.T) {
	t.Run("valid full config", func(t *testing.T) {
		raw := []byte(`{"api_id": 12345, "api_hash": "secret", "redis_uri": "redis://localhost", "web_setup_completed": true}`)
		findings, err := ValidateConfigBytes(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 4 {
			t.Fatalf("findings = %d, want 4 (%+v)", len(findings), findings)
		}
		for _, f := range findings {
			if f.Status == doctorStatusMiss || f.Status == doctorStatusFail {
				t.Errorf("field %s status %s, want ok/info", f.Field, f.Status)
			}
		}
	})

	t.Run("missing required fields", func(t *testing.T) {
		raw := []byte(`{}`)
		findings, err := ValidateConfigBytes(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 4 {
			t.Fatalf("findings = %d, want 4", len(findings))
		}
		if findField(t, findings, "api_id").Status != doctorStatusMiss {
			t.Error("api_id should be missing")
		}
		if findField(t, findings, "api_hash").Status != doctorStatusMiss {
			t.Error("api_hash should be missing")
		}
		if findField(t, findings, "redis_uri").Status != doctorStatusInfo {
			t.Error("redis_uri should be info (optional)")
		}
	})

	t.Run("api_id as numeric string", func(t *testing.T) {
		raw := []byte(`{"api_id": "67890", "api_hash": "h"}`)
		findings, err := ValidateConfigBytes(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if f := findField(t, findings, "api_id"); f.Status != doctorStatusOK {
			t.Errorf("api_id status = %s, want ok", f.Status)
		}
	})

	t.Run("api_id zero is invalid", func(t *testing.T) {
		raw := []byte(`{"api_id": 0, "api_hash": "h"}`)
		findings, err := ValidateConfigBytes(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if f := findField(t, findings, "api_id"); f.Status != doctorStatusFail {
			t.Errorf("api_id status = %s, want fail", f.Status)
		}
	})

	t.Run("api_id non-numeric string is invalid", func(t *testing.T) {
		raw := []byte(`{"api_id": "not-a-number", "api_hash": "h"}`)
		findings, err := ValidateConfigBytes(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if f := findField(t, findings, "api_id"); f.Status != doctorStatusFail {
			t.Errorf("api_id status = %s, want fail", f.Status)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		raw := []byte(`{not json`)
		if _, err := ValidateConfigBytes(raw); err == nil {
			t.Fatal("expected parse error, got nil")
		}
	})

	t.Run("null body is empty map", func(t *testing.T) {
		findings, err := ValidateConfigBytes([]byte(`null`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(findings) != 4 {
			t.Fatalf("findings = %d, want 4", len(findings))
		}
	})
}

func TestValidateConfigFile(t *testing.T) {
	t.Run("missing file returns ErrConfigMissing", func(t *testing.T) {
		_, err := ValidateConfigFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
		if !errors.Is(err, ErrConfigMissing) {
			t.Fatalf("err = %v, want ErrConfigMissing", err)
		}
	})

	t.Run("present valid file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(`{"api_id": 1, "api_hash": "x"}`), 0600); err != nil {
			t.Fatal(err)
		}
		findings, err := ValidateConfigFile(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if findField(t, findings, "api_id").Status != doctorStatusOK {
			t.Error("api_id should be ok")
		}
	})

	t.Run("corrupt file returns parse error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(`{bad`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateConfigFile(path); err == nil || errors.Is(err, ErrConfigMissing) {
			t.Fatalf("err = %v, want parse error", err)
		}
	})
}

func TestRunDoctorHealthy(t *testing.T) {
	withCleanDoctorEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"api_id": 12345, "api_hash": "secret", "web_setup_completed": true}`), 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := RunDoctor(DoctorOptions{Out: &buf, ConfigPath: filepath.Join(dir, "config.json"), DataRoot: dir})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", code, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"[version]", "[runtime]", "[config]", "[data-root]", "[redis]", "[web]", "[trust]", "Result: OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret") {
		t.Errorf("output leaked secret value:\n%s", out)
	}
}

func TestRunDoctorCorruptConfigFails(t *testing.T) {
	withCleanDoctorEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{bad json`), 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := RunDoctor(DoctorOptions{Out: &buf, ConfigPath: filepath.Join(dir, "config.json"), DataRoot: dir})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output:\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "Result: FAIL") {
		t.Errorf("output missing FAIL\n%s", buf.String())
	}
}

func TestRunDoctorMissingDataRootFails(t *testing.T) {
	withCleanDoctorEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"api_id": 1, "api_hash": "x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := RunDoctor(DoctorOptions{Out: &buf, ConfigPath: filepath.Join(dir, "config.json"), DataRoot: filepath.Join(dir, "no-such-root")})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output:\n%s", code, buf.String())
	}
}

func TestRunDoctorFreshInstallOK(t *testing.T) {
	withCleanDoctorEnv(t)
	dir := t.TempDir()
	var buf bytes.Buffer
	code := RunDoctor(DoctorOptions{Out: &buf, ConfigPath: filepath.Join(dir, "config.json"), DataRoot: dir})
	if code != 0 {
		t.Fatalf("fresh install exit code = %d, want 0; output:\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "config not found") {
		t.Errorf("expected missing-config warning\n%s", buf.String())
	}
}

func TestRunDoctorConfigValidateOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	t.Run("valid", func(t *testing.T) {
		withCleanDoctorEnv(t)
		if err := os.WriteFile(path, []byte(`{"api_id": 1, "api_hash": "x"}`), 0600); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		code := RunDoctor(DoctorOptions{Out: &buf, ConfigPath: path, DataRoot: dir, ConfigValidateOnly: true})
		if code != 0 {
			t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
		}
		out := buf.String()
		if strings.Contains(out, "[data-root]") {
			t.Errorf("validate-only leaked data-root section\n%s", out)
		}
		if !strings.Contains(out, "config validate") {
			t.Errorf("expected config-validate header\n%s", out)
		}
	})

	t.Run("missing is critical", func(t *testing.T) {
		withCleanDoctorEnv(t)
		var buf bytes.Buffer
		code := RunDoctor(DoctorOptions{Out: &buf, ConfigPath: filepath.Join(dir, "missing.json"), DataRoot: dir, ConfigValidateOnly: true})
		if code != 1 {
			t.Fatalf("exit = %d, want 1; output:\n%s", code, buf.String())
		}
	})
}

func TestRunDoctorRedactsAndCountsPerAccount(t *testing.T) {
	withCleanDoctorEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"api_id": 1, "api_hash": "h"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "config-7770001.json"),
		[]byte(`{"Loader":{"loaded_modules":{"A":"https://example.test/a.go","B":"local","C":"https://example.test/c.go"}},"goroku.inline":{"bot_token":"777:abcdef"}}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := RunDoctor(DoctorOptions{Out: &buf, ConfigPath: filepath.Join(dir, "config.json"), DataRoot: dir})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "tg=7770001 modules=3 bot_token=ok") {
		t.Errorf("per-account line missing or wrong\n%s", out)
	}
	if strings.Contains(out, "abcdef") || strings.Contains(out, "777:abcdef") {
		t.Errorf("bot_token value leaked\n%s", out)
	}
}

func TestCountCSVAndLoopback(t *testing.T) {
	if countCSV("") != 0 {
		t.Error("empty csv should be 0")
	}
	if countCSV("10.0.0.0/8") != 1 {
		t.Error("single cidr should be 1")
	}
	if countCSV("10.0.0.0/8, fd00::/8 , ") != 2 {
		t.Error("two cidrs + empty should be 2")
	}
	if !isLoopbackBindHost("127.0.0.1") {
		t.Error("127.0.0.1 should be loopback")
	}
	if isLoopbackBindHost("0.0.0.0") {
		t.Error("0.0.0.0 should not be loopback")
	}
	if !isLoopbackBindHost("localhost") {
		t.Error("localhost should be loopback")
	}
}

func findField(t *testing.T, findings []ConfigFinding, field string) ConfigFinding {
	t.Helper()
	for _, f := range findings {
		if f.Field == field {
			return f
		}
	}
	t.Fatalf("finding %q not found in %+v", field, findings)
	return ConfigFinding{}
}

func withCleanDoctorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"REDIS_URL", "GOROKU_WEB_BIND", "GOROKU_IP", "DOCKER", "GOROKU_TRUSTED_PROXIES", "GOROKU_TRUST_PROXY_HEADERS"} {
		t.Setenv(k, "")
	}
}
