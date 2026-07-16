package goroku

import (
	"encoding/hex"
	"testing"
)

func TestNormalizeProxySecretPrefersSecretOverPass(t *testing.T) {
	if got := NormalizeProxySecret("aabb", "ccdd"); got != "aabb" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeProxySecret("", "0xdead"); got != "dead" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeProxySecret("  ", "  ee01  "); got != "ee01" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateMTProxyConfigCompleteOrEmpty(t *testing.T) {
	if err := ValidateMTProxyConfig("", 0, ""); err != nil {
		t.Fatal(err)
	}
	// 16-byte simple secret
	secret := hex.EncodeToString(make([]byte, 16))
	if err := ValidateMTProxyConfig("proxy.example", 443, secret); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMTProxyConfig("proxy.example", 0, secret); err == nil {
		t.Fatal("expected port error")
	}
	if err := ValidateMTProxyConfig("", 443, secret); err == nil {
		t.Fatal("expected host error")
	}
	if err := ValidateMTProxyConfig("proxy.example", 443, ""); err == nil {
		t.Fatal("expected secret error")
	}
	if err := ValidateMTProxyConfig("proxy.example", 443, "not-hex"); err == nil {
		t.Fatal("expected hex error")
	}
}

func TestConfigureMTProxyRoundTrip(t *testing.T) {
	t.Cleanup(func() { _ = ConfigureMTProxy(MTProxyConfig{}) })

	secret := hex.EncodeToString(make([]byte, 16))
	if err := ConfigureMTProxy(MTProxyConfig{Host: "127.0.0.1", Port: 443, Secret: secret}); err != nil {
		t.Fatal(err)
	}
	cfg, ok := ActiveMTProxy()
	if !ok || cfg.Host != "127.0.0.1" || cfg.Port != 443 || cfg.Secret != secret {
		t.Fatalf("active proxy = %#v ok=%v", cfg, ok)
	}
	if err := ConfigureMTProxy(MTProxyConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := ActiveMTProxy(); ok {
		t.Fatal("proxy should be cleared")
	}
}
