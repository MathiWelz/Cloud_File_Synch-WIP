// Package config handles loading and validating the YAML configuration file.
// Environment variables are expanded using ${VAR} or $VAR syntax.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure.
type Config struct {
	Providers    []ProviderConfig   `yaml:"providers"`
	Sync         SyncConfig         `yaml:"sync"`
	Notification NotificationConfig `yaml:"notification"`
	// StateDir is where per-provider sync state files are stored.
	StateDir string `yaml:"state_dir"`
}

// ProviderConfig holds the configuration for one cloud backend.
type ProviderConfig struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`    // "gdrive" | "azure" | "s3"
	Enabled  bool   `yaml:"enabled"` // default false, must be explicitly set to true

	// RemoteFolder is the cloud path to sync (bucket/prefix, container, Drive folder, etc.)
	RemoteFolder string `yaml:"remote_folder"`
	// LocalDest is the absolute (or ~-prefixed) local directory.
	LocalDest string `yaml:"local_destination"`
	// SyncDirection controls which direction changes are propagated.
	// Valid values: "both" (default), "cloud-to-local", "local-to-cloud"
	SyncDirection string `yaml:"sync_direction"`

	// Credentials is a free-form map; keys depend on the provider type.
	// Values may reference env vars with ${VAR} — they are expanded at load time.
	Credentials map[string]string `yaml:"credentials"`
}

// SyncConfig holds settings that apply to all providers.
type SyncConfig struct {
	// DiskThreshold (0–1): warn if the file would consume more than this
	// fraction of remaining free disk space. Default: 0.75
	DiskThreshold float64 `yaml:"disk_threshold"`
	// ExcludePatterns is a list of shell-style glob patterns matched against
	// the base file name (e.g., "*.tmp", ".DS_Store").
	ExcludePatterns []string `yaml:"exclude_patterns"`
	// MaxFileSizeMB skips individual files larger than this. 0 = no limit.
	MaxFileSizeMB int64 `yaml:"max_file_size_mb"`
}

// NotificationConfig controls email notifications.
type NotificationConfig struct {
	Enabled bool       `yaml:"enabled"`
	Email   string     `yaml:"email"` // recipient address
	SMTP    SMTPConfig `yaml:"smtp"`
}

// SMTPConfig holds SMTP server details.
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	// UseTLS: true = implicit TLS (port 465); false = STARTTLS (port 587)
	UseTLS bool `yaml:"use_tls"`
}

// Load reads, validates, and returns the config at path.
// Tilde expansion and environment variable substitution are applied.
func Load(path string) (*Config, error) {
	path = expandHome(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}

	// Security: warn if config is world-readable (it contains secrets).
	if info, err := os.Stat(path); err == nil {
		if info.Mode()&0o044 != 0 {
			fmt.Fprintf(os.Stderr,
				"⚠️  SECURITY: %s is group/world readable. Run: chmod 600 %s\n", path, path)
		}
	}

	// Expand ${VAR} / $VAR before YAML parsing so secrets live only in env.
	expanded := expandEnvVars(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Sync.DiskThreshold == 0 {
		cfg.Sync.DiskThreshold = 0.75
	}
	if cfg.StateDir == "" {
		home, _ := os.UserHomeDir()
		cfg.StateDir = filepath.Join(home, ".cloudsync", "state")
	} else {
		cfg.StateDir = expandHome(cfg.StateDir)
	}

	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		p.LocalDest = expandHome(p.LocalDest)
		if p.SyncDirection == "" {
			p.SyncDirection = "both"
		}
		if p.Credentials == nil {
			p.Credentials = make(map[string]string)
		}
		// Expand home in credential paths (e.g. token_file)
		for k, v := range p.Credentials {
			p.Credentials[k] = expandHome(v)
		}
	}
	if cfg.Notification.SMTP.Port == 0 {
		cfg.Notification.SMTP.Port = 587
	}
}

// expandEnvVars replaces ${VAR_NAME} and $VAR_NAME with the corresponding
// environment variable value. Unexpanded variables are left in place.
func expandEnvVars(s string) string {
	re := regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	return re.ReplaceAllStringFunc(s, func(match string) string {
		var name string
		if strings.HasPrefix(match, "${") {
			name = match[2 : len(match)-1]
		} else {
			name = match[1:]
		}
		if val, ok := os.LookupEnv(name); ok {
			return val
		}
		return match // leave unchanged if env var is not set
	})
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
