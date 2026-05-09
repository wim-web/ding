# ding

`ding` is a small Go CLI for sending notifications to saved Discord Incoming Webhooks.

It stores named Discord webhook URLs locally, then sends either a plain message body or a raw Discord webhook JSON payload from the command line.

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

Add a Discord Incoming Webhook:

```sh
ding add discord --name github_pr_auto --webhook-url <url>
```

List saved channels:

```sh
ding list
```

Send a plain message:

```sh
ding github_pr_auto --body "hello"
```

Send stdin as a plain message:

```sh
echo "hello" | ding github_pr_auto
```

Send raw Discord webhook JSON:

```sh
ding github_pr_auto --json '{"content":"hello"}'
```

Send an embed payload:

```sh
ding github_pr_auto --json '{"embeds":[{"title":"done","color":3066993}]}'
```

Send JSON from a file:

```sh
ding github_pr_auto --json-file payload.json
```

Send JSON from stdin:

```sh
cat payload.json | ding github_pr_auto --json -
```

## Commands

```sh
ding add discord --name <name> --webhook-url <url>
ding list
ding remove <name>
ding test <name>

ding <name> --body "hello"
ding <name> --json '{"content":"hello"}'
ding <name> --json-file payload.json
cat payload.json | ding <name> --json -

ding schema discord
ding example discord
ding version
ding update
```

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
    }
  }
}
```

The config file is created with mode `0600`. If an existing config file is group/world readable, `ding` changes it to `0600` and prints a warning.

Webhook URLs are not printed by `ding list`, `ding test`, or send failures.

## JSON Payloads

`--body` is converted to a Discord webhook JSON payload:

```json
{"content":"hello"}
```

`--json`, `--json-file`, and `--json -` send the raw JSON payload as-is after light validation.

Validation checks:

- the payload parses as JSON
- the root value is an object
- it includes at least one of `content`, `embeds`, `components`, `poll`, or `attachments`
- if `embeds` exists, it is an array

Anything more detailed is left to Discord's API.

Useful references:

```sh
ding schema discord
ding example discord
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
| 4 | Discord API error |
| 5 | network error |

## Scope

`ding` v1 is intentionally small:

- Discord Incoming Webhooks only
- no Discord bot tokens
- no non-Discord providers
- no embed DSL
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
