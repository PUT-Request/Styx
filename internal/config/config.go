package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

type Mode string

const (
	ModeRead      Mode = "read"
	ModeReadWrite Mode = "read_write"
)

type Config struct {
	Prompt            string `yaml:"prompt"`
	Mode              Mode   `yaml:"mode"`
	APIEndpoint       string `yaml:"api_endpoint"`
	APIKey            string `yaml:"api_key"`
	Model             string `yaml:"model"`
	MaxContext        int    `yaml:"max_context"`
	VerificationRegex string `yaml:"verification_regex"`
	MaxWorkingTime    string `yaml:"max_working_time"`
	SaveLog           bool   `yaml:"save_log"`
	SendLog           bool   `yaml:"send_log"`
	WebhookURL        string `yaml:"webhook_url"`

	// Parsed fields
	CompiledRegex *regexp.Regexp
	MaxDuration   time.Duration
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Defaults
	if cfg.Mode == "" {
		cfg.Mode = ModeRead
	}
	if cfg.MaxContext == 0 {
		cfg.MaxContext = 128000
	}
	if cfg.MaxWorkingTime == "" {
		cfg.MaxWorkingTime = "30m"
	}

	// Expand env vars
	cfg.APIEndpoint = expandEnv(cfg.APIEndpoint)
	cfg.APIKey = expandEnv(cfg.APIKey)
	cfg.WebhookURL = expandEnv(cfg.WebhookURL)

	// Validate
	if cfg.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if cfg.APIEndpoint == "" {
		return nil, fmt.Errorf("api_endpoint is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	// Parse regex
	if cfg.VerificationRegex != "" {
		re, err := regexp.Compile(cfg.VerificationRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid verification_regex: %w", err)
		}
		cfg.CompiledRegex = re
	}

	// Parse duration
	dur, err := time.ParseDuration(cfg.MaxWorkingTime)
	if err != nil {
		return nil, fmt.Errorf("invalid max_working_time: %w", err)
	}
	cfg.MaxDuration = dur

	return &cfg, nil
}

func expandEnv(s string) string {
	if s == "" {
		return ""
	}
	return os.Expand(s, func(key string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		return "${" + key + "}"
	})
}
