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

Put them in a `.env` file in the directory where you'll run `tg`:

The values labeled `api_id` and `api_hash` on
[my.telegram.org/apps](https://my.telegram.org/apps) become `TG_API_ID`
and `TG_API_HASH` in `.env`.

```bash
cp .env.example .env
```

```bash
TG_API_ID=12345678
TG_API_HASH=abcdef0123456789abcdef0123456789
```

Or set them as shell env vars:

```bash
export TG_API_ID=12345678
export TG_API_HASH=abcdef0123456789abcdef0123456789
```

`tg` reads `.env` from the current working directory. If you keep the
repo checkout at `/Users/christiannikolov/Projects/tgctl-go`, run
login and setup commands from there:

```bash
cd /Users/christiannikolov/Projects/tgctl-go
tg login
```

If you want `tg` to work from any directory, export the variables in
your shell profile instead of relying on `.env`.

## First login

```bash
tg login
```

You'll be prompted for your phone number, then a code Telegram will
send you in the Telegram app. After that,
`accounts/default/tg.session` is created locally and you stay logged in.

## Verify

```bash
tg me
tg doctor
tg stats
```

`tg me` shows your authenticated account info. `tg doctor` checks
credentials, session, DB, schema, and version. `tg stats` shows your
local cache state.

## Troubleshooting

**`tg: command not found`** — Go did not put the binary on PATH. Check
`go env GOPATH` and add `$GOPATH/bin` to your shell PATH.

**Auth errors** — run `tg doctor --json` to see exactly which check
fails. The `--live` flag also pings Telegram to confirm network
connectivity.

**`TG_API_ID and TG_API_HASH must be set`** — you are probably running
`tg` from a directory that does not contain `.env`. Either `cd` back to
the directory with `.env`, or export both variables:

```bash
cd /Users/christiannikolov/Projects/tgctl-go
tg --account test login
```

or:

```bash
export TG_API_ID=12345678
export TG_API_HASH=abcdef0123456789abcdef0123456789
tg --account test login
```

**Account flagged or limited** — message `@SpamBot` from your Telegram
client. New accounts and accounts running automation against many
strangers are at higher risk; established personal accounts running
tgctl-go for personal use are at very low risk.

## See also

- [Quickstart](quickstart.md) — first commands after install
- [Safety model](safety.md) — what `--allow-write` and `--read-only` do
- [Multi-account](multi-account.md) — running multiple Telegram accounts
