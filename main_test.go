package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type requestRecord struct {
	Body        string
	ContentType string
	UserAgent   string
}

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

func TestSendBodyJSONFileAndStdinModes(t *testing.T) {
	home := t.TempDir()
	var records []requestRecord
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		records = append(records, requestRecord{
			Body:        body.String(),
			ContentType: r.Header.Get("Content-Type"),
			UserAgent:   r.Header.Get("User-Agent"),
		})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	a, _, stderr := newTestApp(t, home, "", false, server.Client())
	if code := a.run([]string{"add", "discord", "--name", "github_pr_auto", "--webhook-url", server.URL}); code != exitOK {
		t.Fatalf("add exit = %d, stderr = %s", code, stderr.String())
	}

	if code := a.run([]string{"github_pr_auto", "--body", "hello"}); code != exitOK {
		t.Fatalf("body send exit = %d, stderr = %s", code, stderr.String())
	}
	if code := a.run([]string{"github_pr_auto", "--json", `{"embeds":[{"title":"done","color":3066993}]}`}); code != exitOK {
		t.Fatalf("json send exit = %d, stderr = %s", code, stderr.String())
	}

	payloadPath := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(payloadPath, []byte(`{"content":"from file"}`), 0o600); err != nil {
		t.Fatalf("write payload file: %v", err)
	}
	if code := a.run([]string{"github_pr_auto", "--json-file", payloadPath}); code != exitOK {
		t.Fatalf("json-file send exit = %d, stderr = %s", code, stderr.String())
	}

	stdinJSONApp, _, stdinJSONErr := newTestApp(t, home, `{"content":"from stdin json"}`, true, server.Client())
	if code := stdinJSONApp.run([]string{"github_pr_auto", "--json", "-"}); code != exitOK {
		t.Fatalf("json stdin send exit = %d, stderr = %s", code, stdinJSONErr.String())
	}

	stdinBodyApp, _, stdinBodyErr := newTestApp(t, home, "from stdin body\n", true, server.Client())
	if code := stdinBodyApp.run([]string{"github_pr_auto"}); code != exitOK {
		t.Fatalf("stdin body send exit = %d, stderr = %s", code, stdinBodyErr.String())
	}

	wantBodies := []string{
		`{"content":"hello"}`,
		`{"embeds":[{"title":"done","color":3066993}]}`,
		`{"content":"from file"}`,
		`{"content":"from stdin json"}`,
		`{"content":"from stdin body\n"}`,
	}
	if len(records) != len(wantBodies) {
		t.Fatalf("request count = %d, want %d", len(records), len(wantBodies))
	}
	for i, want := range wantBodies {
		if records[i].Body != want {
			t.Fatalf("request %d body = %q, want %q", i, records[i].Body, want)
		}
		if records[i].ContentType != "application/json" {
			t.Fatalf("request %d content type = %q", i, records[i].ContentType)
		}
		if records[i].UserAgent != "ding/"+version {
			t.Fatalf("request %d user agent = %q", i, records[i].UserAgent)
		}
	}
}

func TestJSONValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "syntax", raw: `{"content":`},
		{name: "root array", raw: `["content"]`},
		{name: "missing known key", raw: `{"username":"ding"}`},
		{name: "embeds not array", raw: `{"embeds":{"title":"bad"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := validateRawJSON([]byte(tt.raw)); !errorsIsInvalidJSON(err) {
				t.Fatalf("validateRawJSON error = %v, want invalid JSON", err)
			}
		})
	}
}

func TestInvalidJSONExitDoesNotEchoPayload(t *testing.T) {
	home := t.TempDir()
	a, _, stderr := newTestApp(t, home, "", false, nil)
	if code := a.run([]string{"add", "discord", "--name", "github_pr_auto", "--webhook-url", "https://discord.com/api/webhooks/example"}); code != exitOK {
		t.Fatalf("add exit = %d, stderr = %s", code, stderr.String())
	}

	stderr.Reset()
	payload := `{"content":"secret message"`
	code := a.run([]string{"github_pr_auto", "--json", payload})
	if code != exitInvalidJSON {
		t.Fatalf("send exit = %d, want %d; stderr = %s", code, exitInvalidJSON, stderr.String())
	}
	if strings.Contains(stderr.String(), "secret message") {
		t.Fatalf("invalid JSON stderr leaked payload: %s", stderr.String())
	}
}

func TestDiscordAPIErrorIncludesStatusAndResponseBody(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"bad webhook"}`, http.StatusBadRequest)
	}))
	defer server.Close()

	a, _, stderr := newTestApp(t, home, "", false, server.Client())
	if code := a.run([]string{"add", "discord", "--name", "github_pr_auto", "--webhook-url", server.URL}); code != exitOK {
		t.Fatalf("add exit = %d, stderr = %s", code, stderr.String())
	}
	stderr.Reset()

	code := a.run([]string{"github_pr_auto", "--body", "hello"})
	if code != exitDiscordAPI {
		t.Fatalf("send exit = %d, want %d; stderr = %s", code, exitDiscordAPI, stderr.String())
	}
	if !strings.Contains(stderr.String(), "status 400") || !strings.Contains(stderr.String(), "bad webhook") {
		t.Fatalf("stderr = %q, want status and response body", stderr.String())
	}
}

func TestSchemaAndExample(t *testing.T) {
	home := t.TempDir()
	a, stdout, stderr := newTestApp(t, home, "", false, nil)

	if code := a.run([]string{"schema", "discord"}); code != exitOK {
		t.Fatalf("schema exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "https://docs.discord.com/developers/resources/webhook#execute-webhook-jsonform-params") {
		t.Fatalf("schema output missing webhook docs: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := a.run([]string{"example", "discord"}); code != exitOK {
		t.Fatalf("example exit = %d, stderr = %s", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("example output is not JSON: %v\n%s", err, stdout.String())
	}
	if _, ok := payload["embeds"]; !ok {
		t.Fatalf("example output missing embeds: %s", stdout.String())
	}
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

func TestUpdateReplacesExecutableFromLatestRelease(t *testing.T) {
	oldVersion := version
	version = "dev"
	defer func() {
		version = oldVersion
	}()

	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	newBinary := []byte("new ding binary")
	archive := makeReleaseArchive(t, newBinary)

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/wim-web/ding/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"browser_download_url":%q}]}`, assetName, serverURL+"/download")
		case "/download":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = server.URL
	defer server.Close()

	exePath := filepath.Join(t.TempDir(), "ding")
	if runtime.GOOS == "windows" {
		exePath += ".exe"
	}
	if err := os.WriteFile(exePath, []byte("old ding binary"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	a, stdout, stderr := newTestApp(t, t.TempDir(), "", false, server.Client())
	a.releaseAPIBaseURL = server.URL
	a.executable = func() (string, error) { return exePath, nil }

	if code := a.run([]string{"update"}); code != exitOK {
		t.Fatalf("update exit = %d, stderr = %s", code, stderr.String())
	}
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read executable: %v", err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("updated executable = %q, want %q", got, newBinary)
	}
	if !strings.Contains(stdout.String(), "updated ding to v9.9.9") {
		t.Fatalf("stdout = %q, want update message", stdout.String())
	}
}

func TestUpdateAlreadyCurrentSkipsReplacement(t *testing.T) {
	oldVersion := version
	version = "v9.9.9"
	defer func() {
		version = oldVersion
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/wim-web/ding/releases/latest" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"tag_name":"v9.9.9","assets":[]}`)
	}))
	defer server.Close()

	exePath := filepath.Join(t.TempDir(), "ding")
	if err := os.WriteFile(exePath, []byte("old ding binary"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	a, stdout, stderr := newTestApp(t, t.TempDir(), "", false, server.Client())
	a.releaseAPIBaseURL = server.URL
	a.executable = func() (string, error) { return exePath, nil }

	if code := a.run([]string{"update"}); code != exitOK {
		t.Fatalf("update exit = %d, stderr = %s", code, stderr.String())
	}
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read executable: %v", err)
	}
	if string(got) != "old ding binary" {
		t.Fatalf("executable = %q, want unchanged", got)
	}
	if !strings.Contains(stdout.String(), "already up to date") {
		t.Fatalf("stdout = %q, want already current message", stdout.String())
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

func errorsIsInvalidJSON(err error) bool {
	return err != nil && strings.Contains(err.Error(), errInvalidJSON.Error())
}

func makeReleaseArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	name := "ding"
	if runtime.GOOS == "windows" {
		name = "ding.exe"
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o755,
		Size: int64(len(binary)),
	}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}
