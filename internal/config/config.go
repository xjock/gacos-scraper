package config

import (
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xjock/gacos-scraper/internal/utils"
	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration loaded from YAML.
type Config struct {
	Gacos    GacosConfig    `yaml:"gacos"`
	IMAP     IMAPConfig     `yaml:"imap"`
	Output   OutputConfig   `yaml:"output"`
	Polling  PollingConfig  `yaml:"polling"`
	State    StateConfig    `yaml:"state"`
}

// GacosConfig holds the parameters sent to the GACOS request form.
type GacosConfig struct {
	North   float64  `yaml:"north"`
	South   float64  `yaml:"south"`
	West    float64  `yaml:"west"`
	East    float64  `yaml:"east"`
	Hour    int      `yaml:"hour"`
	Minute  int      `yaml:"minute"`
	Dates   []string `yaml:"dates"`
	Type    int      `yaml:"type"`
	Email   string   `yaml:"email"`
}

// IMAPConfig holds mailbox access settings.
type IMAPConfig struct {
	Server         string `yaml:"server"`
	Username       string `yaml:"username"`
	Password       string `yaml:"password"`
	UseTLS         bool   `yaml:"use_tls"`
	SkipTLSVerify  bool   `yaml:"skip_tls_verify"`
	Mailbox        string `yaml:"mailbox"`
	SenderFilter   string `yaml:"sender_filter"`
	SubjectFilter  string `yaml:"subject_filter"`
}

// OutputConfig controls where files are stored.
type OutputConfig struct {
	Dir           string `yaml:"dir"`
	StagingSubdir string `yaml:"staging_subdir"`
	ExtractSubdir string `yaml:"extract_subdir"`
	Extract       bool   `yaml:"extract"`
}

// PollingConfig controls loop timing and retries.
type PollingConfig struct {
	Interval        time.Duration `yaml:"interval"`
	DownloadTimeout time.Duration `yaml:"download_timeout"`
	MaxRetries      int           `yaml:"max_retries"`
}

// StateConfig controls the persistent state database.
type StateConfig struct {
	DBFile string `yaml:"db_file"`
}

// Load reads and validates a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() error {
	if c.IMAP.Mailbox == "" {
		c.IMAP.Mailbox = "INBOX"
	}
	if c.Output.Dir == "" {
		c.Output.Dir = "./downloads"
	}
	if c.Output.StagingSubdir == "" {
		c.Output.StagingSubdir = "staging"
	}
	if c.Output.ExtractSubdir == "" {
		c.Output.ExtractSubdir = "extracted"
	}
	if c.Polling.Interval == 0 {
		c.Polling.Interval = 5 * time.Minute
	}
	if c.Polling.DownloadTimeout == 0 {
		c.Polling.DownloadTimeout = 30 * time.Minute
	}
	if c.Polling.MaxRetries <= 0 {
		c.Polling.MaxRetries = 3
	}
	if c.State.DBFile == "" {
		c.State.DBFile = "./state.db"
	}
	c.Gacos.Dates = utils.NormalizeDates(c.Gacos.Dates)
	return nil
}

// StateFile returns the configured state DB path.
func (c *Config) StateFile() string {
	return c.State.DBFile
}

func (c *Config) validate() error {
	g := c.Gacos
	if g.North < -90 || g.North > 90 {
		return fmt.Errorf("gacos.north must be between -90 and 90")
	}
	if g.South < -90 || g.South > 90 {
		return fmt.Errorf("gacos.south must be between -90 and 90")
	}
	if g.West < -180 || g.West > 180 {
		return fmt.Errorf("gacos.west must be between -180 and 180")
	}
	if g.East < -180 || g.East > 180 {
		return fmt.Errorf("gacos.east must be between -180 and 180")
	}
	if g.South >= g.North {
		return fmt.Errorf("gacos.south must be less than gacos.north")
	}
	if g.West >= g.East {
		return fmt.Errorf("gacos.west must be less than gacos.east")
	}
	if g.Hour < 0 || g.Hour > 23 {
		return fmt.Errorf("gacos.hour must be between 0 and 23")
	}
	if g.Minute < 0 || g.Minute > 59 {
		return fmt.Errorf("gacos.minute must be between 0 and 59")
	}
	if g.Type != 1 && g.Type != 2 {
		return fmt.Errorf("gacos.type must be 1 (binary) or 2 (geotiff)")
	}
	if len(g.Dates) == 0 {
		return fmt.Errorf("gacos.dates is empty")
	}
	seen := make(map[string]struct{}, len(g.Dates))
	for _, d := range g.Dates {
		if err := utils.ValidateYYYYMMDD(d); err != nil {
			return err
		}
		if _, ok := seen[d]; ok {
			return fmt.Errorf("duplicate date in gacos.dates: %s", d)
		}
		seen[d] = struct{}{}
	}
	if err := utils.ValidateEmail(g.Email); err != nil {
		return fmt.Errorf("gacos.email is invalid: %w", err)
	}

	if c.IMAP.Server == "" {
		return fmt.Errorf("imap.server is required")
	}
	if c.IMAP.Username == "" {
		return fmt.Errorf("imap.username is required")
	}
	if c.IMAP.Password == "" {
		return fmt.Errorf("imap.password is required")
	}

	if err := os.MkdirAll(c.Output.Dir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", c.Output.Dir, err)
	}

	return nil
}

// StagingDir returns the absolute path to the staging directory.
func (c *Config) StagingDir() string {
	return filepath.Join(c.Output.Dir, c.Output.StagingSubdir)
}

// ExtractDir returns the absolute path to the extraction directory.
func (c *Config) ExtractDir() string {
	return filepath.Join(c.Output.Dir, c.Output.ExtractSubdir)
}

// ParseDatesFile reads a text file with one YYYYMMDD date per line and appends them to cfg.Gacos.Dates.
func (c *Config) ParseDatesFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read dates file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		c.Gacos.Dates = append(c.Gacos.Dates, line)
	}
	c.Gacos.Dates = utils.NormalizeDates(c.Gacos.Dates)
	return nil
}

// String helpers for form encoding.
func (g *GacosConfig) N() string { return strconv.FormatFloat(g.North, 'f', 6, 64) }
func (g *GacosConfig) W() string { return strconv.FormatFloat(g.West, 'f', 6, 64) }
func (g *GacosConfig) E() string { return strconv.FormatFloat(g.East, 'f', 6, 64) }
func (g *GacosConfig) S() string { return strconv.FormatFloat(g.South, 'f', 6, 64) }
func (g *GacosConfig) H() string { return strconv.Itoa(g.Hour) }
func (g *GacosConfig) M() string { return strconv.Itoa(g.Minute) }
func (g *GacosConfig) TypeStr() string { return strconv.Itoa(g.Type) }
func (g *GacosConfig) DatesBlock() string { return strings.Join(g.Dates, "\n") }

// SubmissionKey returns the idempotency key for a single submission chunk.
func (g *GacosConfig) SubmissionKey(dates []string) string {
	return utils.SubmissionKey(g.North, g.West, g.East, g.South, g.Hour, g.Minute, g.Type, g.Email, dates)
}

// EmailAddress satisfies small external callers that need a net/mail address.
func (g *GacosConfig) EmailAddress() *mail.Address {
	a, _ := mail.ParseAddress(g.Email)
	return a
}
