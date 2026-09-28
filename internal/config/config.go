// Package config loads connection settings from ~/.config/caldav-tui/config.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Config struct {
	Server      string `json:"server"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	PasswordCmd string `json:"password_cmd,omitempty"` // e.g. "pass show fastmail/caldav"
}

func Dir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "caldav-tui"), nil
}

func Path() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

func CachePath() (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	d = filepath.Join(d, "caldav-tui")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(d, "tasks.db"), nil
}

func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no config at %s\n\nCreate it with:\n%s", p, example)
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if c.Server == "" {
		c.Server = "https://caldav.fastmail.com"
	}
	if c.Username == "" {
		return nil, fmt.Errorf("%s: username is required", p)
	}
	return &c, nil
}

// ResolvePassword returns the literal password, or the output of password_cmd.
func (c *Config) ResolvePassword() (string, error) {
	if c.PasswordCmd != "" {
		out, err := exec.Command("sh", "-c", c.PasswordCmd).Output()
		if err != nil {
			return "", fmt.Errorf("password_cmd: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	if c.Password == "" {
		return "", errors.New("config needs password or password_cmd")
	}
	return c.Password, nil
}

const example = `mkdir -p ~/.config/caldav-tui && cat > ~/.config/caldav-tui/config.json <<'JSON'
{
  "server": "https://caldav.fastmail.com",
  "username": "you@fastmail.com",
  "password_cmd": "secret-tool lookup service caldav-tui"
}
JSON
chmod 600 ~/.config/caldav-tui/config.json

Use a Fastmail app password (Settings > Privacy & Security > App passwords, with CalDAV access).`
