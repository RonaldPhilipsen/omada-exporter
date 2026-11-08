package config

import (
	"strings"
	"testing"
	"time"
)

const secret = "s3cr3t-value"

func baseEnv() map[string]string {
	return map[string]string{
		"OMADA_BASE_URL":      "https://omada.example",
		"OMADA_OMADAC_ID":     "abc123",
		"OMADA_CLIENT_ID":     "client",
		"OMADA_CLIENT_SECRET": secret,
	}
}

func getter(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFromEnvDefaults(t *testing.T) {
	cfg, err := FromEnv(getter(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BaseURL != "https://omada.example" || cfg.OmadacID != "abc123" ||
		cfg.ClientID != "client" || cfg.ClientSecret != secret {
		t.Errorf("required values not copied: %+v", cfg)
	}
	if cfg.PollInterval != 60*time.Second {
		t.Errorf("PollInterval = %v", cfg.PollInterval)
	}
	if cfg.FirmwarePollInterval != time.Hour {
		t.Errorf("FirmwarePollInterval = %v", cfg.FirmwarePollInterval)
	}
	if !cfg.ClientMetrics {
		t.Error("ClientMetrics should default to true")
	}
	if cfg.InsecureTLS {
		t.Error("InsecureTLS should default to false")
	}
}

func TestFromEnvMissing(t *testing.T) {
	for _, name := range []string{"OMADA_BASE_URL", "OMADA_OMADAC_ID", "OMADA_CLIENT_ID", "OMADA_CLIENT_SECRET"} {
		t.Run(name, func(t *testing.T) {
			env := baseEnv()
			delete(env, name)
			_, err := FromEnv(getter(env))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not name %s", err, name)
			}
		})
	}
}

func TestFromEnvInvalid(t *testing.T) {
	cases := []struct{ name, value string }{
		{"OMADA_POLL_INTERVAL", "abc"},
		{"OMADA_POLL_INTERVAL", "0s"},
		{"OMADA_POLL_INTERVAL", "-1s"},
		{"OMADA_FIRMWARE_POLL_INTERVAL", "0s"},
		{"OMADA_CLIENT_METRICS", "maybe"},
		{"OMADA_BASE_URL", "omada.local:8043"},
		{"OMADA_BASE_URL", "not a url"},
		{"OMADA_BASE_URL", "ftp://x"},
		{"OMADA_BASE_URL", "   "},
		{"OMADA_CLIENT_ID", "  \t"},
	}
	for _, c := range cases {
		t.Run(c.name+"="+c.value, func(t *testing.T) {
			env := baseEnv()
			env[c.name] = c.value
			_, err := FromEnv(getter(env))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.name) {
				t.Errorf("error %q does not name %s", err, c.name)
			}
		})
	}
}

func TestFromEnvErrorHidesSecret(t *testing.T) {
	env := baseEnv()
	env["OMADA_POLL_INTERVAL"] = "abc"
	_, err := FromEnv(getter(env))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the secret: %q", err)
	}
	delete(env, "OMADA_CLIENT_ID")
	env["OMADA_POLL_INTERVAL"] = "60s"
	_, err = FromEnv(getter(env))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("error missing or leaks the secret: %v", err)
	}
}

func TestFromEnvTrimsRequired(t *testing.T) {
	env := baseEnv()
	env["OMADA_BASE_URL"] = " https://omada.example:8043 \n"
	env["OMADA_CLIENT_ID"] = " client "
	cfg, err := FromEnv(getter(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://omada.example:8043" || cfg.ClientID != "client" {
		t.Errorf("values not trimmed: %+v", cfg)
	}
}

func TestFromEnvBooleansHonored(t *testing.T) {
	env := baseEnv()
	env["OMADA_CLIENT_METRICS"] = "false"
	env["OMADA_INSECURE_TLS"] = "true"
	cfg, err := FromEnv(getter(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientMetrics || !cfg.InsecureTLS {
		t.Errorf("ClientMetrics=%v InsecureTLS=%v", cfg.ClientMetrics, cfg.InsecureTLS)
	}
}
