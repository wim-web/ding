package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func newTestApp(t *testing.T, home string, stdin string, hasStdin bool, client *http.Client) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if client == nil {
		client = http.DefaultClient
	}
	return &app{
		stdin:        strings.NewReader(stdin),
		stdout:       stdout,
		stderr:       stderr,
		client:       client,
		homeDir:      func() (string, error) { return home, nil },
		stdinHasData: func() bool { return hasStdin },
	}, stdout, stderr
}

func TestVersionCommand(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	version, commit, date = "v1.2.3", "abc123", "2026-05-09T00:00:00Z"
	defer func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	}()

	home := t.TempDir()
	a, stdout, stderr := newTestApp(t, home, "", false, nil)
	if code := a.run([]string{"version"}); code != exitOK {
		t.Fatalf("version exit = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"ding v1.2.3", "commit: abc123", "built: 2026-05-09T00:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Fatalf("version output = %q, want %q", out, want)
		}
	}
}
