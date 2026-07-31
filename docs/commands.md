# Commands

`tg --help` shows all 70 commands. This page is generated from the local Cobra help output, then kept as plain Markdown for the docs site.

Every command supports the global flags shown by `tg --help`: `--account`, `--full`, `--json`, `--human`, `--lock-wait`, `--read-only`, and `--version` where applicable.

## Index

| Command | What |
|---|---|
| [`tg account-sessions`](#tg-account-sessions) | List authorized Telegram sessions |
| [`tg accounts-add`](#tg-accounts-add) | Create a new account directory |
| [`tg accounts-list`](#tg-accounts-list) | List known accounts |
| [`tg accounts-remove`](#tg-accounts-remove) | Delete an account directory |
| [`tg accounts-show`](#tg-accounts-show) | Show the currently selected account and its paths |
| [`tg accounts-use`](#tg-accounts-use) | Select an existing account |
| [`tg backfill`](#tg-backfill) | Backfill cached messages for a chat |
| [`tg backfill-entities`](#tg-backfill-entities) | Populate the local entity cache so chat_id-keyed sends work |
| [`tg ban-from-chat`](#tg-ban-from-chat) | ban-from-chat user in chat |
| [`tg block-user`](#tg-block-user) | Block a user |
| [`tg chat-description`](#tg-chat-description) | Edit chat description |
| [`tg chat-invite-link`](#tg-chat-invite-link) | Export an invite link |
| [`tg chat-members`](#tg-chat-members) | List chat members |
| [`tg chat-photo`](#tg-chat-photo) | Edit chat photo |
| [`tg chat-pinned-list`](#tg-chat-pinned-list) | List pinned dialogs for a chat |
| [`tg chat-title`](#tg-chat-title) | Edit chat title |
| [`tg chats-info`](#tg-chats-info) | Show chat info for comma-separated chat ids |
| [`tg completion`](#tg-completion) | Generate the autocompletion script for tg for the specified shell. |
| [`tg contacts`](#tg-contacts) | List cached contacts |
| [`tg delete-msg`](#tg-delete-msg) | Delete one or more messages (revoke for everyone by default) |
| [`tg demote`](#tg-demote) | demote user in chat |
| [`tg discover`](#tg-discover) | Discover dialogs and cache chat metadata |
| [`tg doctor`](#tg-doctor) | Diagnose tgctl-go setup |
| [`tg edit-msg`](#tg-edit-msg) | Edit a previously sent message |
| [`tg folder-add-chat`](#tg-folder-add-chat) | Mutate folder chat membership |
| [`tg folder-create`](#tg-folder-create) | Create a dialog folder |
| [`tg folder-delete`](#tg-folder-delete) | Delete a dialog folder |
| [`tg folder-edit`](#tg-folder-edit) | Edit a dialog folder |
| [`tg folder-remove-chat`](#tg-folder-remove-chat) | Mutate folder chat membership |
| [`tg folder-show`](#tg-folder-show) | Show one dialog folder |
| [`tg folders-list`](#tg-folders-list) | List dialog folders |
| [`tg folders-reorder`](#tg-folders-reorder) | Reorder dialog folders |
| [`tg forward`](#tg-forward) | Forward one or more messages between chats |
| [`tg get-msg`](#tg-get-msg) | Print one cached message in full |
| [`tg help`](#tg-help) | Help provides help for any command in the application. |
| [`tg import-telethon-session`](#tg-import-telethon-session) | Adopt a Python tgctl/Telethon session as the current Go account's session |
| [`tg kick`](#tg-kick) | kick user in chat |
| [`tg leave-chat`](#tg-leave-chat) | Leave a group or channel (typed confirm required) |
| [`tg list-msgs`](#tg-list-msgs) | List cached messages in a chat with optional date filters |
| [`tg listen`](#tg-listen) | Listen for live Telegram updates |
| [`tg login`](#tg-login) | Interactively authorize this account against Telegram |
| [`tg mark-read`](#tg-mark-read) | Mark history read up to and including --up-to |
| [`tg me`](#tg-me) | Print authenticated user info |
| [`tg pin-msg`](#tg-pin-msg) | Pin a message in a chat |
| [`tg promote`](#tg-promote) | promote user in chat |
| [`tg react`](#tg-react) | Send a reaction to a message |
| [`tg search`](#tg-search) | Search cached messages in a chat |
| [`tg send`](#tg-send) | Send a text message |
| [`tg send-by-username`](#tg-send-by-username) | Send a text message by resolving an @username (no entity cache required) |
| [`tg set-permissions`](#tg-set-permissions) | Set default chat permissions |
| [`tg show`](#tg-show) | Show recent cached messages in a chat |
| [`tg stats`](#tg-stats) | Show local cache statistics |
| [`tg sync-contacts`](#tg-sync-contacts) | Sync Telegram contacts into the local DB |
| [`tg terminate-session`](#tg-terminate-session) | Terminate one of your authorized Telegram sessions |
| [`tg topic-create`](#tg-topic-create) | Create a forum topic |
| [`tg topic-edit`](#tg-topic-edit) | Edit a forum topic |
| [`tg topic-pin`](#tg-topic-pin) | Pin a forum topic |
| [`tg topic-unpin`](#tg-topic-unpin) | Unpin a forum topic |
| [`tg topics-list`](#tg-topics-list) | List forum topics |
| [`tg unban-from-chat`](#tg-unban-from-chat) | unban-from-chat user in chat |
| [`tg unblock-user`](#tg-unblock-user) | Unblock a previously blocked user |
| [`tg unpin-msg`](#tg-unpin-msg) | Unpin a previously pinned message |
| [`tg unread`](#tg-unread) | List recently cached incoming messages |
| [`tg upload-document`](#tg-upload-document) | Upload a document |
| [`tg upload-photo`](#tg-upload-photo) | Upload a photo |
| [`tg upload-video`](#tg-upload-video) | Upload a video |
| [`tg upload-voice`](#tg-upload-voice) | Upload an OGG/Opus voice message |
| [`tg version`](#tg-version) | Print build version |

## `tg account-sessions`

List authorized Telegram sessions

**Use**

```text
tg account-sessions [flags]
```

**Example**

```bash
tg account-sessions --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for account-sessions |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg accounts-add`

Create a new account directory

**Use**

```text
tg accounts-add <name> [flags]
```

**Example**

```bash
tg accounts-add work --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for accounts-add |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg accounts-list`

List known accounts

**Use**

```text
tg accounts-list [flags]
```

**Example**

```bash
tg accounts-list --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for accounts-list |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg accounts-remove`

Delete an account directory

**Use**

```text
tg accounts-remove <name> [flags]
```

**Example**

```bash
tg accounts-remove work --confirm work --json
```

**Flags**

| Flag | Description |
|---|---|
| `--confirm string` | Typed account name to confirm removal |
| `-h, --help` | help for accounts-remove |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg accounts-show`

Show the currently selected account and its paths

**Use**

```text
tg accounts-show [flags]
```

**Example**

```bash
tg accounts-show --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for accounts-show |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg accounts-use`

Select an existing account

**Use**

```text
tg accounts-use <name> [flags]
```

**Example**

```bash
tg accounts-use work --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for accounts-use |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg backfill`

Backfill cached messages for a chat

**Use**

```text
tg backfill <chat> [flags]
```

**Example**

```bash
tg backfill 1240314255 --max-messages 100 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for local DB writes |
| `--download-media` | Download media during backfill |
| `-h, --help` | help for backfill |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--max-db-size-mb int` | Maximum main database plus WAL allocation in MiB (0 disables the cap) |
| `--max-messages int` | Maximum cached messages per chat (maximum 10000) (default 100) |
| `--throttle-seconds float` | Seconds to sleep between Telegram history pages |

## `tg backfill-entities`

Populate the local entity cache so chat_id-keyed sends work

**Use**

```text
tg backfill-entities [flags]
```

**Example**

```bash
tg backfill-entities --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for backfill-entities |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Max dialogs to fetch in one pass (Telegram caps at ~200) (default 200) |

## `tg ban-from-chat`

ban-from-chat user in chat

**Use**

```text
tg ban-from-chat <chat> <user-id> [flags]
```

**Example**

```bash
tg ban-from-chat <group-chat-id> 1240314255 --allow-write --confirm 1240314255 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for ban-from-chat |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg block-user`

Block a user

**Use**

```text
tg block-user <user> [flags]
```

**Example**

```bash
tg block-user 1240314255 --allow-write --confirm 1240314255 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for block-user |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg chat-description`

Edit chat description

**Use**

```text
tg chat-description <chat> <value> [flags]
```

**Example**

```bash
tg chat-description <group-chat-id> "Description" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for chat-description |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg chat-invite-link`

Export an invite link

**Use**

```text
tg chat-invite-link <chat> [flags]
```

**Example**

```bash
tg chat-invite-link <group-chat-id> --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for chat-invite-link |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg chat-members`

List chat members

**Use**

```text
tg chat-members <chat> [flags]
```

**Example**

```bash
tg chat-members <group-chat-id> --limit 50 --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for chat-members |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Maximum members (default 50) |

## `tg chat-photo`

Edit chat photo

**Use**

```text
tg chat-photo <chat> <value> [flags]
```

**Example**

```bash
tg chat-photo <group-chat-id> ./photo.png --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for chat-photo |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg chat-pinned-list`

List pinned dialogs for a chat

**Use**

```text
tg chat-pinned-list <chat> [flags]
```

**Example**

```bash
tg chat-pinned-list 1240314255 --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for chat-pinned-list |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg chat-title`

Edit chat title

**Use**

```text
tg chat-title <chat> <value> [flags]
```

**Example**

```bash
tg chat-title <group-chat-id> "New title" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for chat-title |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg chats-info`

Show chat info for comma-separated chat ids

**Use**

```text
tg chats-info <chat-ids> [flags]
```

**Example**

```bash
tg chats-info 1240314255 --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for chats-info |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg completion`

Generate the autocompletion script for tg for the specified shell.

**Use**

```text
tg completion [command]
```

**Example**

```bash
tg completion zsh > ~/.zsh/completions/_tg
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for completion |

## `tg contacts`

List cached contacts

**Use**

```text
tg contacts [flags]
```

**Example**

```bash
tg contacts --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for contacts |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Maximum contacts (default 100) |

## `tg delete-msg`

Delete one or more messages (revoke for everyone by default)

**Use**

```text
tg delete-msg <chat> <message-ids> [flags]
```

**Example**

```bash
tg delete-msg 1240314255 1 --allow-write --confirm 1240314255 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--for-everyone` | Force revoke for everyone |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for delete-msg |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--no-for-everyone` | Force delete only for self |

## `tg demote`

demote user in chat

**Use**

```text
tg demote <chat> <user-id> [flags]
```

**Example**

```bash
tg demote 987654321 1240314255 --allow-write --confirm 987654321 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for demote |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg discover`

Discover dialogs and cache chat metadata

**Use**

```text
tg discover [flags]
```

**Example**

```bash
tg discover --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for local DB writes |
| `-h, --help` | help for discover |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Maximum dialogs to fetch (default 200) |

## `tg doctor`

Diagnose tgctl-go setup

**Use**

```text
tg doctor [flags]
```

**Example**

```bash
tg doctor --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for doctor |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg edit-msg`

Edit a previously sent message

**Use**

```text
tg edit-msg <chat> <message-id> <new-text> [flags]
```

**Example**

```bash
tg edit-msg 1240314255 1 "updated" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for edit-msg |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg folder-add-chat`

Mutate folder chat membership

**Use**

```text
tg folder-add-chat <id> <chat> [flags]
```

**Example**

```bash
tg folder-add-chat 2 1240314255 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for folder-add-chat |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg folder-create`

Create a dialog folder

**Use**

```text
tg folder-create <name> [flags]
```

**Example**

```bash
tg folder-create "support" --include-chats 1240314255 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--emoji string` | Folder emoji |
| `--exclude-chats string` | Comma-separated chat ids to exclude |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for folder-create |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--include-chats string` | Comma-separated chat ids to include |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg folder-delete`

Delete a dialog folder

**Use**

```text
tg folder-delete <id> [flags]
```

**Example**

```bash
tg folder-delete 2 --allow-write --confirm 2 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for folder-delete |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg folder-edit`

Edit a dialog folder

**Use**

```text
tg folder-edit <id> [flags]
```

**Example**

```bash
tg folder-edit 2 --name "support" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--add string` | Comma-separated chat ids to add |
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--emoji string` | Folder emoji |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for folder-edit |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--name string` | New folder name |
| `--remove string` | Comma-separated chat ids to remove |

## `tg folder-remove-chat`

Mutate folder chat membership

**Use**

```text
tg folder-remove-chat <id> <chat> [flags]
```

**Example**

```bash
tg folder-remove-chat 2 1240314255 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for folder-remove-chat |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg folder-show`

Show one dialog folder

**Use**

```text
tg folder-show <id> [flags]
```

**Example**

```bash
tg folder-show 2 --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for folder-show |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg folders-list`

List dialog folders

**Use**

```text
tg folders-list [flags]
```

**Example**

```bash
tg folders-list --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for folders-list |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--query string` | Filter by title |

## `tg folders-reorder`

Reorder dialog folders

**Use**

```text
tg folders-reorder <id-csv> [flags]
```

**Example**

```bash
tg folders-reorder 2,3,4 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for folders-reorder |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg forward`

Forward one or more messages between chats

**Use**

```text
tg forward <from-chat> <to-chat> <message-ids> [flags]
```

**Example**

```bash
tg forward 1240314255 1240314255 1 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for forward |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--topic int` | Forum topic id on destination |

## `tg get-msg`

Print one cached message in full

**Use**

```text
tg get-msg <chat> <message-id> [flags]
```

**Example**

```bash
tg get-msg 1240314255 1 --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for get-msg |
| `--human` | Force human-readable output (default on a TTY) |
| `--include-deleted` | Look up tombstoned messages too |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg help`

Help provides help for any command in the application.

**Use**

```text
tg help [command] [flags]
```

**Example**

```bash
tg help send
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for help |

## `tg import-telethon-session`

Adopt a Python tgctl/Telethon session as the current Go account's session.

**Use**

```text
tg import-telethon-session <path>
```

**Flags**

Standard global flags only. No write gate (this is a local-file copy,
not a Telegram-side write).

**Example**

```bash
tg import-telethon-session ~/path/to/python/tgctl/accounts/default/tg.session
```

The auth_key is reused, so no SMS round-trip is needed. The
destination is the current `--account`'s `tg.session` file.

## `tg kick`

kick user in chat

**Use**

```text
tg kick <chat> <user-id> [flags]
```

**Example**

```bash
tg kick <group-chat-id> 1240314255 --allow-write --confirm 1240314255 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for kick |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg leave-chat`

Leave a group or channel (typed confirm required)

**Use**

```text
tg leave-chat <chat> [flags]
```

**Example**

```bash
tg leave-chat <group-chat-id> --allow-write --confirm <group-chat-id> --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for leave-chat |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg list-msgs`

List cached messages in a chat with optional date filters

**Use**

```text
tg list-msgs <chat> [flags]
```

**Example**

```bash
tg list-msgs 1240314255 --limit 10 --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for list-msgs |
| `--human` | Force human-readable output (default on a TTY) |
| `--include-deleted` | Include tombstoned messages |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Max messages to return (default 50) |
| `--reverse` | Oldest first |
| `--since string` | YYYY-MM-DD inclusive lower bound |
| `--until string` | YYYY-MM-DD inclusive upper bound |

## `tg listen`

Listen for live Telegram updates

**Use**

```text
tg listen [flags]
```

**Example**

```bash
tg listen --once --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for local DB writes |
| `-h, --help` | help for listen |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--once` | Exit after one update |

## `tg login`

Interactively authorize this account against Telegram

**Use**

```text
tg login [flags]
```

**Example**

```bash
tg login
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for login |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg mark-read`

Mark history read up to and including --up-to

**Use**

```text
tg mark-read <chat> [flags]
```

**Example**

```bash
tg mark-read 1240314255 --up-to 1 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for mark-read |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--up-to int` | Mark read up to and including this message id; 0 means latest |

## `tg me`

Print authenticated user info

**Use**

```text
tg me [flags]
```

**Example**

```bash
tg me --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for me |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--offline` | Read cached self user info without connecting to Telegram |

## `tg pin-msg`

Pin a message in a chat

**Use**

```text
tg pin-msg <chat> <message-id> [flags]
```

**Example**

```bash
tg pin-msg 1240314255 1 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for pin-msg |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--silent` | Pin silently (no notification) |

## `tg promote`

promote user in chat

**Use**

```text
tg promote <chat> <user-id> [flags]
```

**Example**

```bash
tg promote 987654321 1240314255 --allow-write --confirm 987654321 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for promote |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg react`

Send a reaction to a message

**Use**

```text
tg react <chat> <message-id> <emoji> [flags]
```

**Example**

```bash
tg react 1240314255 1 "👍" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--big` | Send a big reaction (Premium) |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for react |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg search`

Search cached messages in a chat

**Use**

```text
tg search <chat> <query> [flags]
```

**Example**

```bash
tg search 1240314255 "shipping" --limit 20 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--case-sensitive` | Case-sensitive matching |
| `-h, --help` | help for search |
| `--human` | Force human-readable output (default on a TTY) |
| `--include-deleted` | Include tombstoned messages |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Max messages to return (default 50) |

## `tg send`

Send a text message

**Use**

```text
tg send <chat> <text> [flags]
```

**Example**

```bash
tg send 1240314255 "hello" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for send |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--no-webpage` | Disable link preview |
| `--reply-to int` | Reply-to message id |
| `--silent` | Send silently (no notification) |
| `--topic int` | Forum topic id |

## `tg send-by-username`

Send a text message by resolving an @username (no entity cache required)

**Use**

```text
tg send-by-username <@user|@channel> <text> [flags]
```

**Example**

```bash
tg send-by-username @username "hello" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--dry-run` | Print payload preview without contacting Telegram |
| `-h, --help` | help for send-by-username |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--no-webpage` | Disable link preview |
| `--reply-to int` | Reply-to message id |
| `--silent` | Send silently |

## `tg set-permissions`

Set default chat permissions

**Use**

```text
tg set-permissions <chat> [permissions] [flags]
```

**Example**

```bash
tg set-permissions <group-chat-id> --send-messages --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for set-permissions |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--send-messages` | Allow sending messages |

## `tg show`

Show recent cached messages in a chat

**Use**

```text
tg show <chat> [flags]
```

**Example**

```bash
tg show 1240314255 --limit 5 --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for show |
| `--human` | Force human-readable output (default on a TTY) |
| `--include-deleted` | Include tombstoned messages |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Max messages to return (default 20) |
| `--reverse` | Show oldest first |

## `tg stats`

Show local cache statistics

**Use**

```text
tg stats [flags]
```

**Example**

```bash
tg stats --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for stats |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg sync-contacts`

Sync Telegram contacts into the local DB

**Use**

```text
tg sync-contacts [flags]
```

**Example**

```bash
tg sync-contacts --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for local DB writes |
| `-h, --help` | help for sync-contacts |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg terminate-session`

Terminate one of your authorized Telegram sessions

**Use**

```text
tg terminate-session <session-hash> [flags]
```

**Example**

```bash
tg terminate-session <session-hash> --allow-write --confirm <session-hash> --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for terminate-session |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg topic-create`

Create a forum topic

**Use**

```text
tg topic-create <chat> <title> [flags]
```

**Example**

```bash
tg topic-create <forum-chat-id> "Support" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for topic-create |
| `--human` | Force human-readable output (default on a TTY) |
| `--icon-color int` | Topic icon color |
| `--icon-emoji-id int` | Topic icon custom emoji id |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg topic-edit`

Edit a forum topic

**Use**

```text
tg topic-edit <chat> <topic-id> [flags]
```

**Example**

```bash
tg topic-edit <forum-chat-id> 1 --title "Renamed" --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for topic-edit |
| `--human` | Force human-readable output (default on a TTY) |
| `--icon-emoji-id int` | New icon custom emoji id |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--title string` | New topic title |

## `tg topic-pin`

Pin a forum topic

**Use**

```text
tg topic-pin <chat> <topic-id> [flags]
```

**Example**

```bash
tg topic-pin <forum-chat-id> 1 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for topic-pin |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg topic-unpin`

Unpin a forum topic

**Use**

```text
tg topic-unpin <chat> <topic-id> [flags]
```

**Example**

```bash
tg topic-unpin <forum-chat-id> 1 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for topic-unpin |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg topics-list`

List forum topics

**Use**

```text
tg topics-list <chat> [flags]
```

**Example**

```bash
tg topics-list <forum-chat-id> --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for topics-list |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Maximum topics (default 50) |
| `--query string` | Filter query |

## `tg unban-from-chat`

unban-from-chat user in chat

**Use**

```text
tg unban-from-chat <chat> <user-id> [flags]
```

**Example**

```bash
tg unban-from-chat <group-chat-id> 1240314255 --allow-write --confirm 1240314255 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for unban-from-chat |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg unblock-user`

Unblock a previously blocked user

**Use**

```text
tg unblock-user <user> [flags]
```

**Example**

```bash
tg unblock-user 1240314255 --allow-write --confirm 1240314255 --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for unblock-user |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |

## `tg unpin-msg`

Unpin a previously pinned message

**Use**

```text
tg unpin-msg <chat> <message-id> [flags]
```

**Example**

```bash
tg unpin-msg 1240314255 1 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for unpin-msg |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--silent` | Pin silently (no notification) |

## `tg unread`

List recently cached incoming messages

**Use**

```text
tg unread [flags]
```

**Example**

```bash
tg unread --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for unread |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--limit int` | Maximum messages (default 50) |

## `tg upload-document`

Upload a document

**Use**

```text
tg upload-document <chat> <file> [flags]
```

**Example**

```bash
tg upload-document 1240314255 ./file.txt --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--caption string` | Media caption |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--filename string` | Override uploaded filename |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for upload-document |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--max-size-mb int` | Maximum upload size in MiB (default 100) |
| `--reply-to int` | Reply-to message id |
| `--silent` | Send silently |

## `tg upload-photo`

Upload a photo

**Use**

```text
tg upload-photo <chat> <file> [flags]
```

**Example**

```bash
tg upload-photo 1240314255 ./photo.png --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--caption string` | Media caption |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for upload-photo |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--max-size-mb int` | Maximum upload size in MiB (default 100) |
| `--reply-to int` | Reply-to message id |
| `--silent` | Send silently |

## `tg upload-video`

Upload a video

**Use**

```text
tg upload-video <chat> <file> [flags]
```

**Example**

```bash
tg upload-video 1240314255 ./video.mp4 --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--caption string` | Media caption |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for upload-video |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--max-size-mb int` | Maximum upload size in MiB (default 100) |
| `--reply-to int` | Reply-to message id |
| `--silent` | Send silently |
| `--supports-streaming` | Mark video as streamable |

## `tg upload-voice`

Upload an OGG/Opus voice message

**Use**

```text
tg upload-voice <chat> <file> [flags]
```

**Example**

```bash
tg upload-voice 1240314255 ./voice.ogg --allow-write --json
```

**Flags**

| Flag | Description |
|---|---|
| `--allow-write` | Required for any Telegram-side write |
| `--caption string` | Media caption |
| `--confirm string` | Typed confirm against the resolved id |
| `--dry-run` | Print payload preview without contacting Telegram |
| `--fuzzy` | Allow title-based selectors for write commands |
| `-h, --help` | help for upload-voice |
| `--human` | Force human-readable output (default on a TTY) |
| `--idempotency-key string` | Per-account replay-safe key |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
| `--max-size-mb int` | Maximum upload size in MiB (default 100) |
| `--reply-to int` | Reply-to message id |
| `--silent` | Send silently |

## `tg version`

Print build version

**Use**

```text
tg version [flags]
```

**Example**

```bash
tg version --json
```

**Flags**

| Flag | Description |
|---|---|
| `-h, --help` | help for version |
| `--human` | Force human-readable output (default on a TTY) |
| `--json` | Force JSON envelope output (default when stdout is not a TTY) |
