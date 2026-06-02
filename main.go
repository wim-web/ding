package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	exitOK = iota
	exitUsage
	exitConfig
	exitInvalidJSON
	exitWebhookAPI
	exitNetwork
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

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
  ding add slack   --name <name> --webhook-url <url>
  ding list
  ding remove <name>
  ding test <name>
  ding <name> --body "hello"
  ding <name> --json '{"content":"hello"}'
  ding <name> --json-file payload.json
  ding <name> --json -
  ding schema <discord|slack>
  ding example <discord|slack>
  ding version
  ding update`)
}

func (a *app) cmdAdd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage error: add requires a provider (discord or slack)")
		a.printUsage()
		return exitUsage
	}
	providerType := args[0]
	if _, ok := providers[providerType]; !ok {
		fmt.Fprintf(a.stderr, "usage error: unknown provider: %s\n", providerType)
		a.printUsage()
		return exitUsage
	}

	fs := newFlagSet("add " + providerType)
	name := fs.String("name", "", "channel name")
	webhookURL := fs.String("webhook-url", "", "incoming webhook URL")
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
	cfg.Channels[*name] = Channel{Type: providerType, WebhookURL: *webhookURL}
	if err := a.saveConfig(cfg); err != nil {
		a.printConfigError(err)
		return exitConfig
	}
	fmt.Fprintf(a.stdout, "added %s (%s)\n", *name, providerType)
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

	ch, p, code := a.resolveChannel(args[0])
	if code != exitOK {
		return code
	}
	payload, err := p.BodyPayload("ding test")
	if err != nil {
		fmt.Fprintf(a.stderr, "usage error: %v\n", err)
		return exitUsage
	}
	if code := a.post(ch, payload); code != exitOK {
		return code
	}
	fmt.Fprintf(a.stdout, "sent test to %s\n", args[0])
	return exitOK
}

func (a *app) cmdSend(name string, args []string) int {
	fs := newFlagSet("send")
	body := fs.String("body", "", "message body")
	jsonArg := fs.String("json", "", "raw webhook JSON payload, or '-' for stdin")
	jsonFile := fs.String("json-file", "", "raw webhook JSON payload file")
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
	if boolInt(hasBody)+boolInt(hasJSON)+boolInt(hasJSONFile) > 1 {
		fmt.Fprintln(a.stderr, "usage error: choose only one of --body, --json, or --json-file")
		return exitUsage
	}

	ch, p, code := a.resolveChannel(name)
	if code != exitOK {
		return code
	}

	var payload []byte
	var err error
	switch {
	case hasBody:
		payload, err = p.BodyPayload(*body)
	case hasJSON:
		var raw []byte
		if raw, err = a.readRawArg(*jsonArg); err == nil {
			payload, err = a.validateForProvider(name, p, raw)
		}
	case hasJSONFile:
		var raw []byte
		if raw, err = readFileRaw(*jsonFile); err == nil {
			payload, err = a.validateForProvider(name, p, raw)
		}
	case a.stdinHasData != nil && a.stdinHasData():
		payload, err = a.stdinBodyPayload(p)
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

	return a.post(ch, payload)
}

func (a *app) cmdSchema(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage error: schema requires a provider (discord or slack)")
		return exitUsage
	}
	p, ok := providers[args[0]]
	if !ok {
		fmt.Fprintf(a.stderr, "usage error: unknown provider: %s\n", args[0])
		return exitUsage
	}
	fmt.Fprintln(a.stdout, p.SchemaDoc())
	return exitOK
}

func (a *app) cmdExample(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage error: example requires a provider (discord or slack)")
		return exitUsage
	}
	p, ok := providers[args[0]]
	if !ok {
		fmt.Fprintf(a.stderr, "usage error: unknown provider: %s\n", args[0])
		return exitUsage
	}
	fmt.Fprintln(a.stdout, p.ExampleDoc())
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
