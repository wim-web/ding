package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type requestRecord struct {
	Body        string
	ContentType string
	UserAgent   string
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

func TestSlackSendBodyAsTextAndRawPassthrough(t *testing.T) {
	home := t.TempDir()
	var records []requestRecord
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(r.Body)
		records = append(records, requestRecord{
			Body:        body.String(),
			ContentType: r.Header.Get("Content-Type"),
		})
		// Slack incoming webhooks answer 200 with a plain "ok" body.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	a, _, stderr := newTestApp(t, home, "", false, server.Client())
	if code := a.run([]string{"add", "slack", "--name", "alerts", "--webhook-url", server.URL}); code != exitOK {
		t.Fatalf("add slack exit = %d, stderr = %s", code, stderr.String())
	}

	if code := a.run([]string{"alerts", "--body", "hello"}); code != exitOK {
		t.Fatalf("slack body send exit = %d, stderr = %s", code, stderr.String())
	}
	if code := a.run([]string{"alerts", "--json", `{"blocks":[{"type":"divider"}]}`}); code != exitOK {
		t.Fatalf("slack json send exit = %d, stderr = %s", code, stderr.String())
	}

	wantBodies := []string{
		`{"text":"hello"}`,
		`{"blocks":[{"type":"divider"}]}`,
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
	}
}

func TestSlackRawJSONHintsAtMismatch(t *testing.T) {
	home := t.TempDir()
	a, _, stderr := newTestApp(t, home, "", false, nil)
	if code := a.run([]string{"add", "slack", "--name", "alerts", "--webhook-url", "https://hooks.slack.com/services/example"}); code != exitOK {
		t.Fatalf("add slack exit = %d, stderr = %s", code, stderr.String())
	}

	stderr.Reset()
	// Discord-dialect payload sent to a slack channel: validation fails before
	// any network call, and the error hints at the likely mismatch.
	code := a.run([]string{"alerts", "--json", `{"content":"hi"}`})
	if code != exitInvalidJSON {
		t.Fatalf("send exit = %d, want %d; stderr = %s", code, exitInvalidJSON, stderr.String())
	}
	for _, want := range []string{
		"payload must include one of text, blocks, attachments",
		"this looks like a discord payload",
		`"alerts" is a slack channel`,
		"ding schema slack",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want substring %q", stderr.String(), want)
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
			if _, err := (discordProvider{}).ValidateRawJSON([]byte(tt.raw)); !errorsIsInvalidJSON(err) {
				t.Fatalf("ValidateRawJSON error = %v, want invalid JSON", err)
			}
		})
	}
}

func TestBodyPayloadRejectsDiscordContentOverLimit(t *testing.T) {
	home := t.TempDir()
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	a, _, stderr := newTestApp(t, home, "", false, server.Client())
	if code := a.run([]string{"add", "discord", "--name", "github_pr_auto", "--webhook-url", server.URL}); code != exitOK {
		t.Fatalf("add exit = %d, stderr = %s", code, stderr.String())
	}

	longBody := strings.Repeat("あ", 2001)
	stderr.Reset()
	code := a.run([]string{"github_pr_auto", "--body", longBody})
	if code != exitUsage {
		t.Fatalf("send exit = %d, want %d; stderr = %s", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Discord content limit exceeded: 2001 > 2000") {
		t.Fatalf("stderr = %q, want content limit error", stderr.String())
	}

	stdinApp, _, stdinErr := newTestApp(t, home, longBody, true, server.Client())
	code = stdinApp.run([]string{"github_pr_auto"})
	if code != exitUsage {
		t.Fatalf("stdin body exit = %d, want %d; stderr = %s", code, exitUsage, stdinErr.String())
	}
	if !strings.Contains(stdinErr.String(), "Discord content limit exceeded: 2001 > 2000") {
		t.Fatalf("stdin stderr = %q, want content limit error", stdinErr.String())
	}

	if requestCount != 0 {
		t.Fatalf("request count = %d, want 0", requestCount)
	}
}

func TestSlackBodyHasNoContentLimit(t *testing.T) {
	home := t.TempDir()
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	a, _, stderr := newTestApp(t, home, "", false, server.Client())
	if code := a.run([]string{"add", "slack", "--name", "alerts", "--webhook-url", server.URL}); code != exitOK {
		t.Fatalf("add slack exit = %d, stderr = %s", code, stderr.String())
	}

	longBody := strings.Repeat("あ", 2001)
	if code := a.run([]string{"alerts", "--body", longBody}); code != exitOK {
		t.Fatalf("slack long body exit = %d, stderr = %s", code, stderr.String())
	}
	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1", requestCount)
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

func TestWebhookAPIErrorIncludesStatusAndResponseBody(t *testing.T) {
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
	if code != exitWebhookAPI {
		t.Fatalf("send exit = %d, want %d; stderr = %s", code, exitWebhookAPI, stderr.String())
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

	stdout.Reset()
	stderr.Reset()
	if code := a.run([]string{"schema", "slack"}); code != exitOK {
		t.Fatalf("schema slack exit = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "docs.slack.dev") {
		t.Fatalf("slack schema output missing docs: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := a.run([]string{"example", "slack"}); code != exitOK {
		t.Fatalf("example slack exit = %d, stderr = %s", code, stderr.String())
	}
	var slackPayload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &slackPayload); err != nil {
		t.Fatalf("slack example output is not JSON: %v\n%s", err, stdout.String())
	}
	if _, ok := slackPayload["blocks"]; !ok {
		t.Fatalf("slack example output missing blocks: %s", stdout.String())
	}
}

func TestUnknownProviderIsRejected(t *testing.T) {
	home := t.TempDir()
	a, _, stderr := newTestApp(t, home, "", false, nil)
	code := a.run([]string{"add", "telegram", "--name", "x", "--webhook-url", "https://example.com/hook"})
	if code != exitUsage {
		t.Fatalf("add unknown provider exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown provider: telegram") {
		t.Fatalf("stderr = %q, want unknown provider error", stderr.String())
	}
}

func errorsIsInvalidJSON(err error) bool {
	return err != nil && strings.Contains(err.Error(), errInvalidJSON.Error())
}
