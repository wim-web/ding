package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAddListRemoveAndConfigPermissions(t *testing.T) {
	home := t.TempDir()
	a, stdout, stderr := newTestApp(t, home, "", false, nil)

	code := a.run([]string{"add", "discord", "--name", "github_pr_auto", "--webhook-url", "https://discord.com/api/webhooks/example"})
	if code != exitOK {
		t.Fatalf("add exit = %d, stderr = %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "discord.com/api/webhooks") {
		t.Fatalf("add leaked webhook URL: %s", stdout.String())
	}

	configPath := filepath.Join(home, ".config", "ding", "config.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %04o, want 0600", info.Mode().Perm())
	}

	stdout.Reset()
	stderr.Reset()
	code = a.run([]string{"list"})
	if code != exitOK {
		t.Fatalf("list exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "github_pr_auto\tdiscord\n"; got != want {
		t.Fatalf("list output = %q, want %q", got, want)
	}
	if strings.Contains(stdout.String(), "discord.com/api/webhooks") {
		t.Fatalf("list leaked webhook URL: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = a.run([]string{"remove", "github_pr_auto"})
	if code != exitOK {
		t.Fatalf("remove exit = %d, stderr = %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = a.run([]string{"list"})
	if code != exitOK {
		t.Fatalf("list after remove exit = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("list after remove output = %q, want empty", stdout.String())
	}
}

func TestReadableConfigIsRepaired(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are platform dependent on Windows")
	}

	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "ding", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"channels":{}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	a, _, stderr := newTestApp(t, home, "", false, nil)
	if code := a.run([]string{"list"}); code != exitOK {
		t.Fatalf("list exit = %d, stderr = %s", code, stderr.String())
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %04o, want 0600", info.Mode().Perm())
	}
	if !strings.Contains(stderr.String(), "warning: config file permissions") {
		t.Fatalf("stderr = %q, want permissions warning", stderr.String())
	}
}
