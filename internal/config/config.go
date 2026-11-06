// Package config loads and validates deployd's YAML configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Role is the authorization role of an API user.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

// User is an API user with a pre-hashed bearer token.
type User struct {
	Name      string `yaml:"name"`
	TokenHash string `yaml:"token_hash"` // hex-encoded SHA-256 of the bearer token
	Role      Role   `yaml:"role"`
}

// Site is a deployable site bound to one git repo, branch and script.
type Site struct {
	Name          string `yaml:"name"`
	Repo          string `yaml:"repo"`
	Branch        string `yaml:"branch"`
	Script        string `yaml:"script"`
	WebhookSecret string `yaml:"webhook_secret"`
	RunAs         string `yaml:"run_as"` // optional unix user for CI isolation; empty = current user
}

// Fetch holds the unified fetch policy. Attempt 1 is always forced direct;
// HTTPS remotes then retry through the proxy; SSH remotes stay direct.
type Fetch struct {
	Proxy string `yaml:"proxy"`
}

// Config is the top-level deployd configuration.
type Config struct {
	Listen             string `yaml:"listen"`
	DBPath             string `yaml:"db_path"`
	StateDir           string `yaml:"state_dir"`
	GlobalConcurrency  int    `yaml:"global_concurrency"`
	DeployTimeoutSec   int    `yaml:"deploy_timeout_sec"`
	LogDir             string `yaml:"log_dir"`
	WebDir             string `yaml:"web_dir"` // static frontend export; empty = API only
	Fetch              Fetch  `yaml:"fetch"`
	Users              []User `yaml:"users"`
	Sites              []Site `yaml:"sites"`
	listenHost         string
	listenPort         string
	resolvedDBPath     string
	resolvedStateDir   string
	resolvedLogDir     string
	resolvedWebDir     string
	resolvedScriptPath map[string]string
}

var (
	nameRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	branchRe    = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	shaRe       = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
	tokenHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	userRe      = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// Load reads, parses and validates the YAML config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := c.validate(path); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate(cfgPath string) error {
	base := cfgDir(cfgPath)

	if strings.TrimSpace(c.Listen) == "" {
		c.Listen = "127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("config: listen %q: %w", c.Listen, err)
	}
	if port == "" || !validPort(port) {
		return fmt.Errorf("config: listen %q: invalid port", c.Listen)
	}
	c.listenHost, c.listenPort = host, port

	if c.GlobalConcurrency <= 0 {
		c.GlobalConcurrency = 2
	}
	if c.GlobalConcurrency > 32 {
		return fmt.Errorf("config: global_concurrency %d exceeds cap 32", c.GlobalConcurrency)
	}
	if c.DeployTimeoutSec <= 0 {
		c.DeployTimeoutSec = 1800
	}

	c.resolvedDBPath = c.resolve(base, c.DBPath, filepath.Join(base, "deploy.db"))
	c.resolvedStateDir = c.resolve(base, c.StateDir, filepath.Join(base, "state"))
	c.resolvedLogDir = c.resolve(base, c.LogDir, filepath.Join(c.resolvedStateDir, "logs"))
	c.resolvedWebDir = c.resolve(base, c.WebDir, "")

	// fetch.proxy: empty defaults to the unified snirect endpoint; "none" disables proxying entirely.
	trimmedProxy := strings.TrimSpace(c.Fetch.Proxy)
	if trimmedProxy == "" {
		c.Fetch.Proxy = "http://127.0.0.1:7654"
	} else if strings.EqualFold(trimmedProxy, "none") {
		c.Fetch.Proxy = ""
	}

	if len(c.Users) == 0 {
		return fmt.Errorf("config: at least one user is required")
	}
	seenUsers := map[string]bool{}
	for i := range c.Users {
		u := &c.Users[i]
		u.Name = strings.TrimSpace(u.Name)
		u.TokenHash = strings.ToLower(strings.TrimSpace(u.TokenHash))
		u.Role = Role(strings.ToLower(strings.TrimSpace(string(u.Role))))
		if !nameRe.MatchString(u.Name) {
			return fmt.Errorf("config: users[%d] invalid name %q", i, u.Name)
		}
		if seenUsers[u.Name] {
			return fmt.Errorf("config: duplicate user %q", u.Name)
		}
		seenUsers[u.Name] = true
		if !tokenHashRe.MatchString(u.TokenHash) {
			return fmt.Errorf("config: user %q token_hash must be 64 lowercase hex chars (sha256)", u.Name)
		}
		switch u.Role {
		case RoleAdmin, RoleViewer:
		default:
			return fmt.Errorf("config: user %q role must be admin or viewer, got %q", u.Name, u.Role)
		}
	}

	if len(c.Sites) == 0 {
		return fmt.Errorf("config: at least one site is required")
	}
	c.resolvedScriptPath = map[string]string{}
	seenSites := map[string]bool{}
	for i := range c.Sites {
		s := &c.Sites[i]
		s.Name = strings.TrimSpace(s.Name)
		s.Repo = strings.TrimSpace(s.Repo)
		s.Branch = strings.TrimSpace(s.Branch)
		s.Script = strings.TrimSpace(s.Script)
		s.RunAs = strings.TrimSpace(s.RunAs)
		if !nameRe.MatchString(s.Name) {
			return fmt.Errorf("config: sites[%d] invalid name %q", i, s.Name)
		}
		if seenSites[s.Name] {
			return fmt.Errorf("config: duplicate site %q", s.Name)
		}
		seenSites[s.Name] = true
		if s.Repo == "" {
			return fmt.Errorf("config: site %q repo is required", s.Name)
		}
		if !ValidBranch(s.Branch) {
			return fmt.Errorf("config: site %q invalid branch %q", s.Name, s.Branch)
		}
		if !filepath.IsAbs(s.Script) {
			return fmt.Errorf("config: site %q script %q must be an absolute path", s.Name, s.Script)
		}
		if s.RunAs != "" && !userRe.MatchString(s.RunAs) {
			return fmt.Errorf("config: site %q invalid run_as user %q", s.Name, s.RunAs)
		}
		resolved := filepath.Clean(s.Script)
		if resolved != s.Script {
			return fmt.Errorf("config: site %q script %q must be a clean absolute path", s.Name, s.Script)
		}
		c.resolvedScriptPath[s.Name] = resolved
	}
	return nil
}

// resolve returns an absolute path: v if already absolute, else v joined onto
// base (the config file's directory). Empty v falls back to def.
func (c *Config) resolve(base, v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	if filepath.IsAbs(v) {
		return filepath.Clean(v)
	}
	return filepath.Clean(filepath.Join(base, v))
}

func cfgDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "."
	}
	return filepath.Dir(abs)
}

func validPort(p string) bool {
	n := 0
	for _, r := range p {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	if len(p) > 5 || n < 1 || n > 65535 {
		return false
	}
	return true
}

// ListenAddr returns the host:port to bind.
func (c *Config) ListenAddr() string { return net.JoinHostPort(c.listenHost, c.listenPort) }

// DBFile returns the resolved SQLite database path.
func (c *Config) DBFile() string { return c.resolvedDBPath }

// WorkRoot returns the root directory holding per-site worktrees.
func (c *Config) WorkRoot() string { return filepath.Join(c.resolvedStateDir, "work") }

// LogDirectory returns the resolved job-log directory.
func (c *Config) LogDirectory() string { return c.resolvedLogDir }

// WebRoot returns the resolved directory of the static frontend export, or
// "" when the service should serve the API only.
func (c *Config) WebRoot() string { return c.resolvedWebDir }

// ScriptPath returns the validated absolute script path for site.
func (c *Config) ScriptPath(site string) (string, bool) {
	p, ok := c.resolvedScriptPath[site]
	return p, ok
}

// SiteByName returns the site with the given name.
func (c *Config) SiteByName(name string) (Site, bool) {
	for _, s := range c.Sites {
		if s.Name == name {
			return s, true
		}
	}
	return Site{}, false
}

// ValidSHA reports whether s is a plausible git object name (40/64-hex).
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// ValidBranch reports whether s is a safe branch name for CLI use.
func ValidBranch(s string) bool {
	return branchRe.MatchString(s) && !strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "/") && !strings.HasSuffix(s, "/") && !strings.HasSuffix(s, ".") && !strings.Contains(s, "..") && !strings.Contains(s, "//") && !strings.Contains(s, "@{")
}
