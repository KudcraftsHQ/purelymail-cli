// Package config resolves credentials.
//
// Precedence, highest first: explicit flags, then environment variables, then
// the selected profile in the config file, then the file's [default] profile.
// Env-only auth is enough for one Purelymail account; profiles exist for when
// there is more than one.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the resolved settings for one invocation.
type Config struct {
	APIToken string
	Profile  string
	// Source names where the token came from, for `config show`.
	Source string
}

// Profile is one named section of the config file.
type Profile struct {
	Name     string
	APIToken string
}

// File is a parsed config file.
type File struct {
	Path     string
	Profiles map[string]Profile
}

const (
	envToken      = "PURELYMAIL_API_TOKEN"
	envProfile    = "PURELYMAIL_PROFILE"
	envConfigPath = "PURELYMAIL_CONFIG"

	defaultProfile = "default"
)

// Path returns the config file location, honouring PURELYMAIL_CONFIG and
// XDG_CONFIG_HOME.
func Path() string {
	if p := os.Getenv(envConfigPath); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "purelymail.toml"
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "purelymail", "config.toml")
}

// Load reads and parses the config file. A missing file is not an error --
// environment-only usage is fully supported.
func Load() (*File, error) {
	path := Path()
	f := &File{Path: path, Profiles: map[string]Profile{}}

	fh, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer fh.Close()

	current := defaultProfile
	scanner := bufio.NewScanner(fh)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			current = strings.TrimSpace(text[1 : len(text)-1])
			if current == "" {
				return nil, fmt.Errorf("%s:%d: empty profile name", path, line)
			}
			if _, ok := f.Profiles[current]; !ok {
				f.Profiles[current] = Profile{Name: current}
			}
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key = value", path, line)
		}
		key = strings.TrimSpace(key)
		value = unquote(strings.TrimSpace(value))

		p := f.Profiles[current]
		p.Name = current
		switch key {
		case "api_token":
			p.APIToken = value
		default:
			return nil, fmt.Errorf("%s:%d: unknown key %q (the only key is api_token)", path, line, key)
		}
		f.Profiles[current] = p
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return f, nil
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// Names lists the profiles defined in the file, sorted.
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Profiles))
	for name := range f.Profiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Overrides are the values supplied on the command line.
type Overrides struct {
	Profile  string
	APIToken string
}

// Resolve merges flags, environment and file into the settings to use.
func Resolve(o Overrides) (Config, error) {
	file, err := Load()
	if err != nil {
		return Config{}, err
	}

	name := firstNonEmpty(o.Profile, os.Getenv(envProfile), defaultProfile)
	p, ok := file.Profiles[name]
	if !ok && name != defaultProfile {
		known := file.Names()
		if len(known) == 0 {
			return Config{}, fmt.Errorf("profile %q requested but %s defines none", name, file.Path)
		}
		return Config{}, fmt.Errorf("profile %q not found in %s (have: %s)",
			name, file.Path, strings.Join(known, ", "))
	}

	cfg := Config{Profile: name}
	switch {
	case o.APIToken != "":
		cfg.APIToken, cfg.Source = o.APIToken, "--token"
	case os.Getenv(envToken) != "":
		cfg.APIToken, cfg.Source = os.Getenv(envToken), envToken
	case p.APIToken != "":
		cfg.APIToken, cfg.Source = p.APIToken, file.Path+" ["+name+"]"
	}
	return cfg, nil
}

// RequireToken returns an actionable error when no token was found.
func (c Config) RequireToken() error {
	if c.APIToken != "" {
		return nil
	}
	return fmt.Errorf("no API token: set %s, pass --token, or add api_token to a profile in %s",
		envToken, Path())
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
