package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

const discordContentLimit = 2000

// Provider captures the per-destination differences between webhook dialects.
// Adding a provider means adding one implementation here and one registry entry.
type Provider interface {
	Type() string
	BodyPayload(body string) ([]byte, error)
	ValidateRawJSON(raw []byte) ([]byte, error)
	SchemaDoc() string
	ExampleDoc() string
}

var providers = map[string]Provider{
	"discord": discordProvider{},
	"slack":   slackProvider{},
}

// ---- discord ----

type discordProvider struct{}

func (discordProvider) Type() string { return "discord" }

func (discordProvider) BodyPayload(body string) ([]byte, error) {
	if count := utf8.RuneCountInString(body); count > discordContentLimit {
		return nil, fmt.Errorf("Discord content limit exceeded: %d > %d", count, discordContentLimit)
	}
	return json.Marshal(struct {
		Content string `json:"content"`
	}{Content: body})
}

func (discordProvider) ValidateRawJSON(raw []byte) ([]byte, error) {
	return validateWebhookKeys(raw,
		[]string{"content", "embeds", "components", "poll", "attachments"},
		[]string{"embeds"})
}

func (discordProvider) SchemaDoc() string {
	return `Discord webhook payload docs:
https://docs.discord.com/developers/resources/webhook#execute-webhook-jsonform-params

Discord embed object docs:
https://docs.discord.com/developers/resources/channel#embed-object

Minimum:
{"content":"hello"}

Embed:
{"embeds":[{"title":"Title","description":"Body","color":3066993}]}`
}

func (discordProvider) ExampleDoc() string {
	return `{
  "embeds": [
    {
      "title": "Renovate automation finished",
      "description": "8 repos checked, 1 PR merged, 0 blocked",
      "color": 3066993
    }
  ]
}`
}

// ---- slack ----

type slackProvider struct{}

func (slackProvider) Type() string { return "slack" }

func (slackProvider) BodyPayload(body string) ([]byte, error) {
	return json.Marshal(struct {
		Text string `json:"text"`
	}{Text: body})
}

func (slackProvider) ValidateRawJSON(raw []byte) ([]byte, error) {
	return validateWebhookKeys(raw,
		[]string{"text", "blocks", "attachments"},
		[]string{"blocks", "attachments"})
}

func (slackProvider) SchemaDoc() string {
	return `Slack incoming webhook payload docs:
https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/

Slack Block Kit docs:
https://docs.slack.dev/block-kit/

Minimum:
{"text":"hello"}

Block Kit:
{"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"*hello*"}}]}`
}

func (slackProvider) ExampleDoc() string {
	return `{
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "*Renovate automation finished*\n8 repos checked, 1 PR merged, 0 blocked"
      }
    }
  ]
}`
}

// ---- channel resolution ----

func (a *app) resolveChannel(name string) (Channel, Provider, int) {
	cfg, err := a.loadConfig()
	if err != nil {
		a.printConfigError(err)
		return Channel{}, nil, exitConfig
	}
	ch, ok := cfg.Channels[name]
	if !ok {
		fmt.Fprintf(a.stderr, "config error: channel not found: %s\n", name)
		return Channel{}, nil, exitConfig
	}
	p, ok := providers[ch.Type]
	if !ok {
		fmt.Fprintf(a.stderr, "config error: unsupported channel type for %s: %s\n", name, ch.Type)
		return Channel{}, nil, exitConfig
	}
	if err := validateWebhookURL(ch.WebhookURL); err != nil {
		fmt.Fprintf(a.stderr, "config error: invalid webhook url for %s: %v\n", name, err)
		return Channel{}, nil, exitConfig
	}
	return ch, p, exitOK
}

// ---- sending ----

func (a *app) post(ch Channel, payload []byte) int {
	req, err := http.NewRequest(http.MethodPost, ch.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		fmt.Fprintf(a.stderr, "config error: invalid webhook url: %v\n", err)
		return exitConfig
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ding/"+version)

	resp, err := a.client.Do(req)
	if err != nil {
		fmt.Fprintf(a.stderr, "network error: %v\n", err)
		return exitNetwork
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		return exitOK
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	fmt.Fprintf(a.stderr, "%s api error: status %d\n", ch.Type, resp.StatusCode)
	if len(body) > 0 {
		fmt.Fprintln(a.stderr, string(body))
	}
	return exitWebhookAPI
}

// ---- payload acquisition ----

func (a *app) readRawArg(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%w: --json requires a value or '-'", errInvalidJSON)
	}
	if value == "-" {
		raw, err := io.ReadAll(a.stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return raw, nil
	}
	return []byte(value), nil
}

func readFileRaw(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--json-file requires a file path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read json file: %w", err)
	}
	return raw, nil
}

func (a *app) stdinBodyPayload(p Provider) ([]byte, error) {
	body, err := io.ReadAll(a.stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return p.BodyPayload(string(body))
}

// ---- raw JSON validation ----

var errInvalidJSON = errors.New("invalid JSON")

// validateForProvider validates raw against p's dialect. If it fails the
// provider-specific key check, it tries the other providers and, when one
// would accept the payload, adds a hint pointing at the likely mismatch.
func (a *app) validateForProvider(name string, p Provider, raw []byte) ([]byte, error) {
	out, err := p.ValidateRawJSON(raw)
	if err == nil {
		return out, nil
	}
	if other := guessOtherProvider(p.Type(), raw); other != "" {
		return nil, fmt.Errorf("%w\nhint: this looks like a %s payload, but %q is a %s channel\n      try: ding schema %s",
			err, other, name, p.Type(), p.Type())
	}
	return nil, err
}

func guessOtherProvider(currentType string, raw []byte) string {
	types := make([]string, 0, len(providers))
	for typ := range providers {
		types = append(types, typ)
	}
	sort.Strings(types)
	for _, typ := range types {
		if typ == currentType {
			continue
		}
		if _, err := providers[typ].ValidateRawJSON(raw); err == nil {
			return typ
		}
	}
	return ""
}

func validateWebhookKeys(raw []byte, allowedKeys, arrayKeys []string) ([]byte, error) {
	obj, err := decodeWebhookObject(raw)
	if err != nil {
		return nil, err
	}
	if !containsAnyKey(obj, allowedKeys...) {
		return nil, fmt.Errorf("%w: payload must include one of %s", errInvalidJSON, strings.Join(allowedKeys, ", "))
	}
	for _, key := range arrayKeys {
		if v, ok := obj[key]; ok {
			if _, ok := v.([]any); !ok {
				return nil, fmt.Errorf("%w: %s must be an array", errInvalidJSON, key)
			}
		}
	}
	return raw, nil
}

func decodeWebhookObject(raw []byte) (map[string]any, error) {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidJSON, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("%w: multiple JSON values", errInvalidJSON)
		}
		return nil, fmt.Errorf("%w: %v", errInvalidJSON, err)
	}

	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: root must be an object", errInvalidJSON)
	}
	return obj, nil
}

func containsAnyKey(obj map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := obj[key]; ok {
			return true
		}
	}
	return false
}

func invalidJSONDetail(err error) string {
	return strings.TrimPrefix(err.Error(), errInvalidJSON.Error()+": ")
}
