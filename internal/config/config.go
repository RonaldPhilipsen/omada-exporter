// Package config reads the exporter settings from environment variables.
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config holds the settings of the exporter.
type Config struct {
	BaseURL              string
	OmadacID             string
	ClientID             string
	ClientSecret         string
	InsecureTLS          bool
	PollInterval         time.Duration
	FirmwarePollInterval time.Duration
	ClientMetrics        bool
}

// FromEnv builds a Config with the given getenv function.
// Error messages name the variable but never include secret values.
func FromEnv(getenv func(string) string) (Config, error) {
	var cfg Config

	required := []struct {
		name string
		dst  *string
	}{
		{"OMADA_BASE_URL", &cfg.BaseURL},
		{"OMADA_OMADAC_ID", &cfg.OmadacID},
		{"OMADA_CLIENT_ID", &cfg.ClientID},
		{"OMADA_CLIENT_SECRET", &cfg.ClientSecret},
	}
	for _, r := range required {
		v := strings.TrimSpace(getenv(r.name))
		if v == "" {
			return Config{}, fmt.Errorf("%s is required", r.name)
		}
		*r.dst = v
	}

	if err := checkBaseURL(cfg.BaseURL); err != nil {
		return Config{}, err
	}

	var err error
	if cfg.PollInterval, err = duration(getenv, "OMADA_POLL_INTERVAL", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.FirmwarePollInterval, err = duration(getenv, "OMADA_FIRMWARE_POLL_INTERVAL", time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.InsecureTLS, err = boolean(getenv, "OMADA_INSECURE_TLS", false); err != nil {
		return Config{}, err
	}
	if cfg.ClientMetrics, err = boolean(getenv, "OMADA_CLIENT_METRICS", true); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func duration(getenv func(string) string, name string, def time.Duration) (time.Duration, error) {
	v := getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid duration", name)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return d, nil
}

func boolean(getenv func(string) string, name string, def bool) (bool, error) {
	v := getenv(name)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s is not a valid boolean", name)
	}
	return b, nil
}

func checkBaseURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("OMADA_BASE_URL must be an http or https URL with a host")
	}
	return nil
}
