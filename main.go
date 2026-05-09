package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	exitOK = iota
	exitUsage
	exitConfig
	exitInvalidJSON
	exitDiscordAPI
	exitNetwork
)

const (
	defaultGitHubAPIBaseURL = "https://api.github.com"
	defaultReleaseRepo      = "wim-web/ding"
	maxReleaseAssetSize     = 100 << 20
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

type Config struct {
	Channels map[string]Channel `json:"channels"`
}

type Channel struct {
	Type       string `json:"type"`
	WebhookURL string `json:"webhook_url"`
}

type app struct {
	stdin        io.Reader
	stdout       io.Writer
	stderr       io.Writer
	client       *http.Client
	homeDir      func() (string, error)
	stdinHasData func() bool
	executable   func() (string, error)

	releaseAPIBaseURL string
}

func main() {
	a := &app{
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
		client: &http.Client{Timeout: 15 * time.Second},
		homeDir: func() (string, error) {
			return os.UserHomeDir()
		},
		stdinHasData: defaultStdinHasData,
		executable:   os.Executable,
	}
	os.Exit(a.run(os.Args[1:]))
}

func (a *app) run(args []string) int {
	if len(args) == 0 {
		a.printUsage()
		return exitUsage
	}

	switch args[0] {
	case "-h", "--help", "help":
		a.printUsage()
		return exitOK
	case "--version":
		return a.cmdVersion(args[1:])
	case "version":
		return a.cmdVersion(args[1:])
	case "update":
		return a.cmdUpdate(args[1:])
	case "add":
		return a.cmdAdd(args[1:])
	case "list":
		return a.cmdList(args[1:])
	case "remove":
		return a.cmdRemove(args[1:])
	case "test":
		return a.cmdTest(args[1:])
	case "schema":
		return a.cmdSchema(args[1:])
	case "example":
		return a.cmdExample(args[1:])
	default:
		return a.cmdSend(args[0], args[1:])
	}
}

func (a *app) printUsage() {
	fmt.Fprintln(a.stderr, `Usage:
  ding add discord --name <name> --webhook-url <url>
  ding list
  ding remove <name>
  ding test <name>
  ding <name> --body "hello"
  ding <name> --json '{"content":"hello"}'
  ding <name> --json-file payload.json
  ding <name> --json -
  ding schema discord
  ding example discord
  ding version
  ding update`)
}

func (a *app) cmdAdd(args []string) int {
	if len(args) == 0 || args[0] != "discord" {
		fmt.Fprintln(a.stderr, "usage error: only 'ding add discord' is supported")
		a.printUsage()
		return exitUsage
	}

	fs := newFlagSet("add discord")
	name := fs.String("name", "", "channel name")
	webhookURL := fs.String("webhook-url", "", "Discord incoming webhook URL")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintf(a.stderr, "usage error: %v\n", err)
		return exitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(*name) == "" || strings.TrimSpace(*webhookURL) == "" {
		fmt.Fprintln(a.stderr, "usage error: --name and --webhook-url are required")
		return exitUsage
	}
	if strings.TrimSpace(*name) != *name {
		fmt.Fprintln(a.stderr, "usage error: channel name must not start or end with whitespace")
		return exitUsage
	}
	if err := validateWebhookURL(*webhookURL); err != nil {
		fmt.Fprintf(a.stderr, "usage error: invalid webhook url: %v\n", err)
		return exitUsage
	}

	cfg, err := a.loadConfig()
	if err != nil {
		a.printConfigError(err)
		return exitConfig
	}
	cfg.Channels[*name] = Channel{Type: "discord", WebhookURL: *webhookURL}
	if err := a.saveConfig(cfg); err != nil {
		a.printConfigError(err)
		return exitConfig
	}
	fmt.Fprintf(a.stdout, "added %s (discord)\n", *name)
	return exitOK
}

func (a *app) cmdList(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.stderr, "usage error: list takes no arguments")
		return exitUsage
	}

	cfg, err := a.loadConfig()
	if err != nil {
		a.printConfigError(err)
		return exitConfig
	}

	names := make([]string, 0, len(cfg.Channels))
	for name := range cfg.Channels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(a.stdout, "%s\t%s\n", name, cfg.Channels[name].Type)
	}
	return exitOK
}

func (a *app) cmdRemove(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage error: remove requires a channel name")
		return exitUsage
	}

	cfg, err := a.loadConfig()
	if err != nil {
		a.printConfigError(err)
		return exitConfig
	}
	if _, ok := cfg.Channels[args[0]]; !ok {
		fmt.Fprintf(a.stderr, "config error: channel not found: %s\n", args[0])
		return exitConfig
	}
	delete(cfg.Channels, args[0])
	if err := a.saveConfig(cfg); err != nil {
		a.printConfigError(err)
		return exitConfig
	}
	fmt.Fprintf(a.stdout, "removed %s\n", args[0])
	return exitOK
}

func (a *app) cmdTest(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage error: test requires a channel name")
		return exitUsage
	}

	ch, code := a.getDiscordChannel(args[0])
	if code != exitOK {
		return code
	}
	payload, err := bodyPayload("ding test")
	if err != nil {
		fmt.Fprintf(a.stderr, "invalid JSON: %v\n", err)
		return exitInvalidJSON
	}
	if code := a.postDiscord(ch, payload); code != exitOK {
		return code
	}
	fmt.Fprintf(a.stdout, "sent test to %s\n", args[0])
	return exitOK
}

func (a *app) cmdSend(name string, args []string) int {
	fs := newFlagSet("send")
	body := fs.String("body", "", "message body")
	jsonArg := fs.String("json", "", "raw Discord webhook JSON payload, or '-' for stdin")
	jsonFile := fs.String("json-file", "", "raw Discord webhook JSON payload file")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(a.stderr, "usage error: %v\n", err)
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.stderr, "usage error: unexpected arguments")
		return exitUsage
	}

	hasBody := flagProvided(fs, "body")
	hasJSON := flagProvided(fs, "json")
	hasJSONFile := flagProvided(fs, "json-file")
	modeCount := boolInt(hasBody) + boolInt(hasJSON) + boolInt(hasJSONFile)
	if modeCount > 1 {
		fmt.Fprintln(a.stderr, "usage error: choose only one of --body, --json, or --json-file")
		return exitUsage
	}

	var payload []byte
	var err error
	switch {
	case hasBody:
		payload, err = bodyPayload(*body)
	case hasJSON:
		payload, err = a.rawJSONPayload(*jsonArg)
	case hasJSONFile:
		payload, err = rawJSONFilePayload(*jsonFile)
	case a.stdinHasData != nil && a.stdinHasData():
		payload, err = a.stdinBodyPayload()
	default:
		fmt.Fprintln(a.stderr, "usage error: provide --body, --json, --json-file, or stdin")
		return exitUsage
	}
	if err != nil {
		if errors.Is(err, errInvalidJSON) {
			fmt.Fprintf(a.stderr, "invalid JSON: %s\n", invalidJSONDetail(err))
			return exitInvalidJSON
		}
		fmt.Fprintf(a.stderr, "usage error: %v\n", err)
		return exitUsage
	}

	ch, code := a.getDiscordChannel(name)
	if code != exitOK {
		return code
	}
	return a.postDiscord(ch, payload)
}

func (a *app) cmdSchema(args []string) int {
	if len(args) != 1 || args[0] != "discord" {
		fmt.Fprintln(a.stderr, "usage error: only 'ding schema discord' is supported")
		return exitUsage
	}
	fmt.Fprintln(a.stdout, `Discord webhook payload docs:
https://docs.discord.com/developers/resources/webhook#execute-webhook-jsonform-params

Discord embed object docs:
https://docs.discord.com/developers/resources/channel#embed-object

Minimum:
{"content":"hello"}

Embed:
{"embeds":[{"title":"Title","description":"Body","color":3066993}]}`)
	return exitOK
}

func (a *app) cmdExample(args []string) int {
	if len(args) != 1 || args[0] != "discord" {
		fmt.Fprintln(a.stderr, "usage error: only 'ding example discord' is supported")
		return exitUsage
	}
	fmt.Fprintln(a.stdout, `{
  "embeds": [
    {
      "title": "Renovate automation finished",
      "description": "8 repos checked, 1 PR merged, 0 blocked",
      "color": 3066993
    }
  ]
}`)
	return exitOK
}

func (a *app) cmdVersion(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.stderr, "usage error: version takes no arguments")
		return exitUsage
	}
	fmt.Fprintf(a.stdout, "ding %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
	return exitOK
}

func (a *app) cmdUpdate(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.stderr, "usage error: update takes no arguments")
		return exitUsage
	}

	latest, err := a.fetchLatestRelease()
	if err != nil {
		fmt.Fprintf(a.stderr, "update error: %v\n", err)
		return exitNetwork
	}
	if sameVersion(version, latest.TagName) {
		fmt.Fprintf(a.stdout, "ding is already up to date (%s)\n", latest.TagName)
		return exitOK
	}

	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	asset, ok := latest.findAsset(assetName)
	if !ok {
		fmt.Fprintf(a.stderr, "update error: release asset not found: %s\n", assetName)
		return exitNetwork
	}

	archive, err := a.downloadReleaseAsset(asset.BrowserDownloadURL)
	if err != nil {
		fmt.Fprintf(a.stderr, "update error: %v\n", err)
		return exitNetwork
	}
	binary, err := extractDingBinary(archive)
	if err != nil {
		fmt.Fprintf(a.stderr, "update error: %v\n", err)
		return exitConfig
	}
	if err := a.replaceExecutable(binary); err != nil {
		fmt.Fprintf(a.stderr, "update error: %v\n", err)
		return exitConfig
	}

	fmt.Fprintf(a.stdout, "updated ding to %s\n", latest.TagName)
	return exitOK
}

func (a *app) getDiscordChannel(name string) (Channel, int) {
	cfg, err := a.loadConfig()
	if err != nil {
		a.printConfigError(err)
		return Channel{}, exitConfig
	}
	ch, ok := cfg.Channels[name]
	if !ok {
		fmt.Fprintf(a.stderr, "config error: channel not found: %s\n", name)
		return Channel{}, exitConfig
	}
	if ch.Type != "discord" {
		fmt.Fprintf(a.stderr, "config error: unsupported channel type for %s: %s\n", name, ch.Type)
		return Channel{}, exitConfig
	}
	if err := validateWebhookURL(ch.WebhookURL); err != nil {
		fmt.Fprintf(a.stderr, "config error: invalid webhook url for %s: %v\n", name, err)
		return Channel{}, exitConfig
	}
	return ch, exitOK
}

func (a *app) postDiscord(ch Channel, payload []byte) int {
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
	fmt.Fprintf(a.stderr, "discord api error: status %d\n", resp.StatusCode)
	if len(body) > 0 {
		fmt.Fprintln(a.stderr, string(body))
	}
	return exitDiscordAPI
}

func (a *app) rawJSONPayload(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%w: --json requires a value or '-'", errInvalidJSON)
	}
	if value == "-" {
		raw, err := io.ReadAll(a.stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return validateRawJSON(raw)
	}
	return validateRawJSON([]byte(value))
}

func rawJSONFilePayload(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--json-file requires a file path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read json file: %w", err)
	}
	return validateRawJSON(raw)
}

func (a *app) stdinBodyPayload() ([]byte, error) {
	body, err := io.ReadAll(a.stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return bodyPayload(string(body))
}

func bodyPayload(body string) ([]byte, error) {
	return json.Marshal(struct {
		Content string `json:"content"`
	}{Content: body})
}

var errInvalidJSON = errors.New("invalid JSON")

func validateRawJSON(raw []byte) ([]byte, error) {
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
	if !containsAnyKey(obj, "content", "embeds", "components", "poll", "attachments") {
		return nil, fmt.Errorf("%w: payload must include one of content, embeds, components, poll, or attachments", errInvalidJSON)
	}
	if embeds, ok := obj["embeds"]; ok {
		if _, ok := embeds.([]any); !ok {
			return nil, fmt.Errorf("%w: embeds must be an array", errInvalidJSON)
		}
	}
	return raw, nil
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

func defaultStdinHasData() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice == 0
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func flagProvided(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (r githubRelease) findAsset(name string) (githubReleaseAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

func (a *app) fetchLatestRelease() (githubRelease, error) {
	raw, err := a.getBytes(a.latestReleaseURL(), maxReleaseAssetSize)
	if err != nil {
		return githubRelease{}, err
	}
	var release githubRelease
	if err := json.Unmarshal(raw, &release); err != nil {
		return githubRelease{}, fmt.Errorf("parse latest release response: %w", err)
	}
	if release.TagName == "" {
		return githubRelease{}, errors.New("latest release response is missing tag_name")
	}
	return release, nil
}

func (a *app) latestReleaseURL() string {
	base := strings.TrimRight(a.releaseAPIBaseURL, "/")
	if base == "" {
		base = defaultGitHubAPIBaseURL
	}
	return base + "/repos/" + defaultReleaseRepo + "/releases/latest"
}

func (a *app) downloadReleaseAsset(rawURL string) ([]byte, error) {
	if rawURL == "" {
		return nil, errors.New("release asset is missing browser_download_url")
	}
	return a.getBytes(rawURL, maxReleaseAssetSize)
}

func (a *app) getBytes(rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ding/"+version)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := readAllLimit(resp.Body, limit)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GET %s returned status %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func readAllLimit(r io.Reader, limit int64) ([]byte, error) {
	lr := &io.LimitedReader{R: r, N: limit + 1}
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	return body, nil
}

func releaseAssetName(goos, goarch string) string {
	return fmt.Sprintf("ding_%s_%s.tar.gz", goos, goarch)
}

func extractDingBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open release archive: %w", err)
	}
	defer gz.Close()

	wanted := "ding"
	if runtime.GOOS == "windows" {
		wanted = "ding.exe"
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read release archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != wanted {
			continue
		}
		return readAllLimit(tr, maxReleaseAssetSize)
	}
	return nil, fmt.Errorf("release archive does not contain %s", wanted)
}

func (a *app) replaceExecutable(binary []byte) error {
	executable := os.Executable
	if a.executable != nil {
		executable = a.executable
	}
	exePath, err := executable()
	if err != nil {
		return err
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return err
	}

	mode := os.FileMode(0o755)
	if info, err := os.Stat(exePath); err == nil {
		mode = info.Mode().Perm()
		if mode&0o111 == 0 {
			mode |= 0o111
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(exePath), ".ding-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, exePath)
}

func sameVersion(current, latest string) bool {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	latest = strings.TrimPrefix(strings.TrimSpace(latest), "v")
	return current != "" && latest != "" && current == latest
}
