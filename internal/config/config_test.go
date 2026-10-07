package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envConfigPath, path)
}

func TestPrecedence(t *testing.T) {
	writeConfig(t, "# comment\napi_token = 'file-default'\n\n[work]\napi_token = \"file-work\"\n")
	t.Setenv(envProfile, "")
	t.Setenv(envToken, "")

	cfg, err := Resolve(Overrides{})
	if err != nil || cfg.APIToken != "file-default" || cfg.Profile != "default" {
		t.Errorf("file default: %+v %v", cfg, err)
	}
	cfg, _ = Resolve(Overrides{Profile: "work"})
	if cfg.APIToken != "file-work" {
		t.Errorf("profile: %+v", cfg)
	}
	t.Setenv(envToken, "env")
	cfg, _ = Resolve(Overrides{Profile: "work"})
	if cfg.APIToken != "env" || cfg.Source != envToken {
		t.Errorf("env should beat file: %+v", cfg)
	}
	cfg, _ = Resolve(Overrides{Profile: "work", APIToken: "flag"})
	if cfg.APIToken != "flag" || cfg.Source != "--token" {
		t.Errorf("flag should beat env: %+v", cfg)
	}
	t.Setenv(envProfile, "work")
	t.Setenv(envToken, "")
	cfg, _ = Resolve(Overrides{})
	if cfg.APIToken != "file-work" {
		t.Errorf("PURELYMAIL_PROFILE: %+v", cfg)
	}
}

func TestMissingFileAndToken(t *testing.T) {
	t.Setenv(envConfigPath, filepath.Join(t.TempDir(), "absent.toml"))
	t.Setenv(envProfile, "")
	t.Setenv(envToken, "")
	cfg, err := Resolve(Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.RequireToken(); err == nil || !strings.Contains(err.Error(), envToken) {
		t.Errorf("RequireToken = %v", err)
	}
	if _, err := Resolve(Overrides{Profile: "nope"}); err == nil {
		t.Error("unknown profile should fail")
	}
}

func TestUnknownKeyIsAnError(t *testing.T) {
	writeConfig(t, "access_token = x\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "api_token") {
		t.Errorf("err = %v", err)
	}
}
