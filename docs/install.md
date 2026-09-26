# Install

## Requirements

- Go 1.25 or newer for `go install`
- A real Telegram account (not a bot account)
- `TG_API_ID` and `TG_API_HASH` from <https://my.telegram.org/apps>

## Install with Go

```bash
go install github.com/b1rd33/tgctl-go/cmd/tg@latest
```

This builds the `tg` binary and puts it on your Go bin path.

## Download release binaries

Pre-built binaries are published on GitHub Releases:

```bash
open https://github.com/b1rd33/tgctl-go/releases/latest
```

Linux, macOS, and Windows builds are published by GoReleaser.

## Homebrew

```bash
brew install b1rd33/tap/tgctl-go
```

Homebrew tap publishing is configured for tagged releases.

## Shell completion

```bash
# bash:
tg completion bash | sudo tee /etc/bash_completion.d/tg

# zsh (e.g. with ohmyzsh, $fpath dir):
tg completion zsh > "${fpath[1]}/_tg"

# fish:
tg completion fish | source

# PowerShell:
tg completion powershell | Out-String | Invoke-Expression
```

Run `tg completion --help` for the supported shells.

## Set up API credentials

Telegram requires you to register a personal app once. It's free and takes ~2 minutes:

1. Visit <https://my.telegram.org/apps> and sign in with your phone number
2. Click "Create new application"
3. Give it any title (e.g. "Personal Archiver"); platform = "Desktop"
4. Copy the resulting `api_id` (an integer) and `api_hash` (32-char hex)

Run the interactive setup command to save them in the stable configuration
root. The API hash is hidden when entered in a terminal:

```bash
tg setup --human
```

By default, the root is `~/Library/Application Support/tgctl` on macOS,
`~/.config/tgctl` on Linux (or the configured XDG directory), and
`%AppData%/tgctl` on Windows. Set `TGCTL_HOME` to an absolute path before setup
and subsequent commands when using a different root. The current working
directory does not select the account or credential file.

You can also supply `TG_API_ID` and `TG_API_HASH` through your process environment.
Keep credentials private; do not put them in source control. See
[reliability](reliability.md) for account paths and ownership.

## First login

```bash
tg --account default login --human
tg --account default discover --allow-write --json
```

You'll be prompted for your phone number, then a code Telegram will
send you in the Telegram app. After that,
`accounts/default/tg.session` is created locally and you stay logged in.

## Verify

```bash
tg --account default me --read-only --json
tg --account default doctor --read-only --json
tg --account default stats --read-only --json
```

`tg me` shows your authenticated account info. `tg doctor` checks
credentials, session, DB, schema, and version. `tg stats` shows your
local cache state.

## Troubleshooting

**`tg: command not found`** — Go did not put the binary on PATH. Check
`go env GOPATH` and add `$GOPATH/bin` to your shell PATH.

**Auth errors** — run `tg --account default doctor --read-only --json` for
local configuration/session/cache diagnostics. Use `me --read-only --json` to
check the existing live authorization. Read-only live commands need an existing
ownership sidecar and account identity; after a new login, initialize the cache
with `tg --account default discover --allow-write --json`.

**`TG_API_ID and TG_API_HASH must be set`** — run `tg setup --human` for the
same stable root used by your commands, or provide both process environment
variables. Changing directories does not make the CLI load another `.env`.

**Account flagged or limited** — use Telegram's official client to inspect the
restriction and contact `@SpamBot` if appropriate. Respect reported waits and
restrictions; an established account is not a guarantee against server limits.

## See also

- [Quickstart](quickstart.md) — first commands after install
- [Safety model](safety.md) — what `--allow-write` and `--read-only` do
- [Multi-account](multi-account.md) — running multiple Telegram accounts
