# ding

`ding` is a small Go CLI for sending notifications to saved Discord and Slack Incoming Webhooks.

It stores named webhook URLs locally, each tagged with a provider (`discord` or `slack`), then sends either a plain message body or a raw webhook JSON payload from the command line.

## Install

Download the latest release for your platform from:

https://github.com/wim-web/ding/releases/latest

For macOS Apple Silicon:

```sh
curl -L -o ding_darwin_arm64.tar.gz \
  https://github.com/wim-web/ding/releases/download/v0.1.0/ding_darwin_arm64.tar.gz
tar -xzf ding_darwin_arm64.tar.gz
install -m 0755 ding ~/.local/bin/ding
```

Check the installed version:

```sh
ding version
```

## Quick Start

Add an Incoming Webhook (the provider is fixed at this point):

```sh
ding add discord --name github_pr_auto --webhook-url <url>
ding add slack   --name alerts         --webhook-url <url>
```

List saved channels (shows the provider, never the URL):

```sh
ding list
```

Send a plain message — `ding` formats it for the channel's provider, so you
only ever name the channel:

```sh
ding github_pr_auto --body "hello"   # -> {"content":"hello"} (discord)
ding alerts --body "hello"           # -> {"text":"hello"}    (slack)
```

Send stdin as a plain message:

```sh
echo "hello" | ding alerts
```

Send raw provider JSON (must match the channel's provider dialect):

```sh
ding github_pr_auto --json '{"embeds":[{"title":"done","color":3066993}]}'
ding alerts --json '{"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"*done*"}}]}'
```

Send JSON from a file or stdin:

```sh
ding alerts --json-file payload.json
cat payload.json | ding alerts --json -
```

## Commands

```sh
ding add discord --name <name> --webhook-url <url>
ding add slack   --name <name> --webhook-url <url>
ding list
ding remove <name>
ding test <name>

ding <name> --body "hello"
ding <name> --json '{"content":"hello"}'
ding <name> --json-file payload.json
cat payload.json | ding <name> --json -

ding schema <discord|slack>
ding example <discord|slack>
ding version
ding update
```

The provider is chosen once, at `add` time. When sending you only name the
channel — `ding` looks up its provider from the config.

## Config

The config file is stored at:

```text
~/.config/ding/config.json
```

Example:

```json
{
  "channels": {
    "github_pr_auto": {
      "type": "discord",
      "webhook_url": "https://discord.com/api/webhooks/..."
    },
    "alerts": {
      "type": "slack",
      "webhook_url": "https://hooks.slack.com/services/..."
    }
  }
}
```

The config file is created with mode `0600`. If an existing config file is group/world readable, `ding` changes it to `0600` and prints a warning.

Webhook URLs are not printed by `ding list`, `ding test`, or send failures.

## JSON Payloads

`--body` (and stdin body input) is converted to the channel provider's plain
message payload:

| Provider | Payload | Notes |
| --- | --- | --- |
| discord | `{"content":"hello"}` | checked before sending — `content` is limited to 2000 characters |
| slack | `{"text":"hello"}` | no client-side length check |

`--json`, `--json-file`, and `--json -` send the raw JSON payload as-is after
light, provider-specific validation.

Validation checks:

- the payload parses as JSON
- the root value is an object
- it includes at least one allowed top-level key for the channel's provider:
  - discord: `content`, `embeds`, `components`, `poll`, or `attachments` (and `embeds`, if present, is an array)
  - slack: `text`, `blocks`, or `attachments` (and `blocks`/`attachments`, if present, are arrays)

Anything more detailed is left to the provider's API.

If a raw payload fails validation but would be valid for the *other* provider,
`ding` hints at the likely mismatch:

```text
$ ding alerts --json '{"content":"hi"}'      # alerts is a slack channel
invalid JSON: payload must include one of text, blocks, attachments
hint: this looks like a discord payload, but "alerts" is a slack channel
      try: ding schema slack
```

Useful references:

```sh
ding schema slack
ding example slack
```

## Updating

Update to the latest GitHub release:

```sh
ding update
```

`ding update` downloads the matching release archive for the current OS and architecture, then replaces the current executable.

## Exit Codes

| Code | Meaning |
| ---: | --- |
| 0 | success |
| 1 | usage error |
| 2 | config error or channel not found |
| 3 | invalid JSON |
| 4 | webhook API error |
| 5 | network error |

## Scope

`ding` is intentionally small:

- Discord and Slack Incoming Webhooks only
- no bot tokens
- no embed / Block Kit DSL
- no daemon, queue, or retry worker
- no config encryption

## Development

Run tests:

```sh
go test ./...
```

Build:

```sh
go build -o ding .
```

## License

MIT
