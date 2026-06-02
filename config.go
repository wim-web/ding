package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	Channels map[string]Channel `json:"channels"`
}

type Channel struct {
	Type       string `json:"type"`
	WebhookURL string `json:"webhook_url"`
}

func (a *app) loadConfig() (Config, error) {
	path, err := a.configPath()
	if err != nil {
		return Config{}, err
	}
	if err := a.repairConfigPermissions(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Channels: map[string]Channel{}}, nil
	}
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Channels == nil {
		cfg.Channels = map[string]Channel{}
	}
	return cfg, nil
}

func (a *app) saveConfig(cfg Config) error {
	path, err := a.configPath()
	if err != nil {
		return err
	}
	if err := ensureNotInGitRepo(path); err != nil {
		return err
	}
	if cfg.Channels == nil {
		cfg.Channels = map[string]Channel{}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := a.repairConfigPermissions(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	encodeErr := enc.Encode(cfg)
	closeErr := f.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(path, 0o600)
}

func (a *app) configPath() (string, error) {
	home, err := a.homeDir()
	if err != nil {
		return "", err
	}
	if home == "" {
		return "", errors.New("home directory is empty")
	}
	return filepath.Join(home, ".config", "ding", "config.json"), nil
}

func (a *app) repairConfigPermissions(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	oldMode := info.Mode().Perm()
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(a.stderr, "warning: config file permissions were %04o; changed to 0600\n", oldMode)
	return nil
}

func ensureNotInGitRepo(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return fmt.Errorf("refusing to create config under git repository: %s", abs)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func (a *app) printConfigError(err error) {
	fmt.Fprintf(a.stderr, "config error: %v\n", err)
}

func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("scheme must be http or https")
	}
	if u.Host == "" {
		return errors.New("host is required")
	}
	return nil
}
