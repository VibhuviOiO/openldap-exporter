// Package config loads and validates the exporter's YAML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level document.
type Config struct {
	Listen        string        `yaml:"listen"`
	MetricsPath   string        `yaml:"metrics_path"`
	ScrapeTimeout time.Duration `yaml:"scrape_timeout"`

	// Defaults applied to every target that leaves the field empty.
	Defaults TargetDefaults `yaml:"defaults"`

	Targets []Target `yaml:"targets"`

	// ConfigInterval is how often cn=config is re-read, independent of the
	// metrics scrape interval. cn=config is near-static, and the olcSyncRepl
	// values it returns carry a cleartext bind password, so re-reading it on
	// every scrape puts that password on the wire far more often than the
	// signal justifies. Nil means the default; an explicit 0 disables the
	// cache and reads cn=config on every scrape, as before.
	ConfigInterval *time.Duration `yaml:"config_interval"`

	// Resolved at load time. Not read from YAML.
	ResolvedConfigInterval time.Duration `yaml:"-"`

	// Suffixes whose contextCSN is read on every target. Empty means: discover
	// them from namingContexts on the root DSE.
	Suffixes []string `yaml:"suffixes"`

	// Entries are searches whose result count becomes a gauge.
	Entries []EntryCount `yaml:"entries"`

	Replication Replication `yaml:"replication"`
}

// TargetDefaults are inherited by targets. Useful when every node shares a
// bind DN and password.
type TargetDefaults struct {
	BindDN         string `yaml:"bind_dn"`
	Password       string `yaml:"password"`
	PasswordFile   string `yaml:"password_file"`
	ConfigBindDN   string `yaml:"config_bind_dn"`
	ConfigPassword string `yaml:"config_password"`
	ConfigPassFile string `yaml:"config_password_file"`
	StartTLS       bool   `yaml:"starttls"`
	TLSSkipVerify  bool   `yaml:"tls_skip_verify"`
	TLSCAFile      string `yaml:"tls_ca_file"`
}

// Target is one LDAP server to scrape.
type Target struct {
	Name string `yaml:"name"`
	URI  string `yaml:"uri"` // ldap://host:389 or ldaps://host:636

	// Group names a replication group. Lag is computed between members of the
	// same group only. A QA node that reads from prod belongs in the prod group
	// for lag purposes, since it should carry prod's sids.
	Group string `yaml:"group"`

	// Role is a free-form label (prod, qa, provider, consumer) copied onto
	// every metric for this target.
	Role string `yaml:"role"`

	BindDN         string `yaml:"bind_dn"`
	Password       string `yaml:"password"`
	PasswordFile   string `yaml:"password_file"`
	ConfigBindDN   string `yaml:"config_bind_dn"`
	ConfigPassword string `yaml:"config_password"`
	ConfigPassFile string `yaml:"config_password_file"`

	StartTLS      *bool  `yaml:"starttls"`
	TLSSkipVerify *bool  `yaml:"tls_skip_verify"`
	TLSCAFile     string `yaml:"tls_ca_file"`

	// Resolved at load time. Not read from YAML.
	ResolvedPassword       string `yaml:"-"`
	ResolvedConfigPassword string `yaml:"-"`
	ResolvedStartTLS       bool   `yaml:"-"`
	ResolvedTLSSkipVerify  bool   `yaml:"-"`
}

// EntryCount is a search whose entry count is exported.
type EntryCount struct {
	Name   string `yaml:"name"`
	Base   string `yaml:"base"`
	Filter string `yaml:"filter"`
	Scope  string `yaml:"scope"` // base | one | sub (default sub)
	// Targets restricts this count to named targets. Empty = all.
	Targets []string `yaml:"targets"`
}

// Replication tunes the derived sync metrics.
type Replication struct {
	// InSyncTolerance is how far behind a replica may be and still report
	// openldap_replication_in_sync = 1.
	InSyncTolerance time.Duration `yaml:"in_sync_tolerance"`

	// ExpectedSIDs lets you declare which sids each group should carry, so a
	// sid that has never written anything is still reported as missing.
	// Optional. Key is group name.
	ExpectedSIDs map[string][]string `yaml:"expected_sids"`
}

// Load reads, expands ${ENV} references, applies defaults and validates.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := os.Expand(string(raw), func(k string) string {
		if v, ok := os.LookupEnv(k); ok {
			return v
		}
		return "${" + k + "}" // leave unknown refs visible rather than blank
	})
	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.finish(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) finish() error {
	if c.Listen == "" {
		c.Listen = ":9330"
	}
	if c.MetricsPath == "" {
		c.MetricsPath = "/metrics"
	}
	if c.ScrapeTimeout == 0 {
		c.ScrapeTimeout = 10 * time.Second
	}
	c.ResolvedConfigInterval = 10 * time.Minute
	if c.ConfigInterval != nil {
		if *c.ConfigInterval < 0 {
			return errors.New("config: config_interval must not be negative")
		}
		c.ResolvedConfigInterval = *c.ConfigInterval
	}
	if c.Replication.InSyncTolerance == 0 {
		c.Replication.InSyncTolerance = 5 * time.Second
	}
	if len(c.Targets) == 0 {
		return errors.New("config: at least one target is required")
	}

	seen := map[string]bool{}
	var errs []string
	for i := range c.Targets {
		t := &c.Targets[i]
		if t.Name == "" {
			errs = append(errs, fmt.Sprintf("targets[%d]: name is required", i))
			continue
		}
		if seen[t.Name] {
			errs = append(errs, fmt.Sprintf("targets[%d]: duplicate name %q", i, t.Name))
		}
		seen[t.Name] = true
		if !strings.HasPrefix(t.URI, "ldap://") && !strings.HasPrefix(t.URI, "ldaps://") && !strings.HasPrefix(t.URI, "ldapi://") {
			errs = append(errs, fmt.Sprintf("target %s: uri must start with ldap://, ldaps:// or ldapi://", t.Name))
		}
		if t.Group == "" {
			t.Group = "default"
		}

		// inherit defaults
		if t.BindDN == "" {
			t.BindDN = c.Defaults.BindDN
		}
		if t.Password == "" {
			t.Password = c.Defaults.Password
		}
		if t.PasswordFile == "" {
			t.PasswordFile = c.Defaults.PasswordFile
		}
		if t.ConfigBindDN == "" {
			t.ConfigBindDN = c.Defaults.ConfigBindDN
		}
		if t.ConfigPassword == "" {
			t.ConfigPassword = c.Defaults.ConfigPassword
		}
		if t.ConfigPassFile == "" {
			t.ConfigPassFile = c.Defaults.ConfigPassFile
		}
		if t.TLSCAFile == "" {
			t.TLSCAFile = c.Defaults.TLSCAFile
		}
		t.ResolvedStartTLS = c.Defaults.StartTLS
		if t.StartTLS != nil {
			t.ResolvedStartTLS = *t.StartTLS
		}
		t.ResolvedTLSSkipVerify = c.Defaults.TLSSkipVerify
		if t.TLSSkipVerify != nil {
			t.ResolvedTLSSkipVerify = *t.TLSSkipVerify
		}

		pw, err := resolveSecret(t.Password, t.PasswordFile)
		if err != nil {
			errs = append(errs, fmt.Sprintf("target %s: password: %v", t.Name, err))
		}
		t.ResolvedPassword = pw
		if t.BindDN != "" && pw == "" {
			errs = append(errs, fmt.Sprintf("target %s: bind_dn set but no password/password_file", t.Name))
		}

		cpw, err := resolveSecret(t.ConfigPassword, t.ConfigPassFile)
		if err != nil {
			errs = append(errs, fmt.Sprintf("target %s: config_password: %v", t.Name, err))
		}
		t.ResolvedConfigPassword = cpw
		if t.ConfigBindDN != "" && cpw == "" {
			errs = append(errs, fmt.Sprintf("target %s: config_bind_dn set but no config_password", t.Name))
		}
	}

	for i, e := range c.Entries {
		if e.Name == "" || e.Base == "" {
			errs = append(errs, fmt.Sprintf("entries[%d]: name and base are required", i))
		}
		if e.Filter == "" {
			c.Entries[i].Filter = "(objectClass=*)"
		}
		switch strings.ToLower(e.Scope) {
		case "", "sub":
			c.Entries[i].Scope = "sub"
		case "one", "base":
			c.Entries[i].Scope = strings.ToLower(e.Scope)
		default:
			errs = append(errs, fmt.Sprintf("entries[%d]: scope must be base, one or sub", i))
		}
		for _, tn := range e.Targets {
			if !seen[tn] {
				errs = append(errs, fmt.Sprintf("entries[%d]: unknown target %q", i, tn))
			}
		}
	}

	if len(errs) > 0 {
		return errors.New("config:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

func resolveSecret(inline, file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return inline, nil
}

// TargetByName returns the target with that name, or nil.
func (c *Config) TargetByName(name string) *Target {
	for i := range c.Targets {
		if c.Targets[i].Name == name {
			return &c.Targets[i]
		}
	}
	return nil
}

// Groups returns the distinct group names in config order.
func (c *Config) Groups() []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range c.Targets {
		if !seen[t.Group] {
			seen[t.Group] = true
			out = append(out, t.Group)
		}
	}
	return out
}
