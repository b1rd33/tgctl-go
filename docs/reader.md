# Local reader

`reader` opens one explicitly selected chat in a temporary localhost web interface.
It reads bounded history, keeps ordinary attachments in Telegram, and displays
supported sponsored messages. It has no message composer or background inbox.

```bash
tg --account work reader self --allow-write --human
tg --account work reader @example_channel --allow-write --limit 50 --duration 15m --human
tg --account work reader self --allow-write --dry-run --json
```

Open the printed private URL in your browser and keep the CLI running. No browser
opens automatically. JSON mode emits `reader.ready` with the URL and expiry,
then a final result on shutdown. The fragment is an access token: do not publish
the URL, record stdout in shared logs, or automate a browser against real ads.
The browser removes the fragment after loading; a full browser reload needs the
original URL again. Use the page's **Refresh** button for normal refreshes.

**Close reader**, Ctrl-C, or the lifetime limit stops the server. Merely closing
the browser tab leaves the server running until its lifetime ends. The default
is 15 minutes, with a 1–60 minute range. Each page contains 1–100 messages and
the server issues at most 20 continuation offsets. Refresh fetches current
history; this is not a live-update consumer. Group authors without a resolved
display name are labeled with their sender ID. Ordinary media is described,
not downloaded or played inside this reader; view it in the official client.

## Session and writes

The reader binds its first successful request to the authenticated user and
resolved peer, then checks them on subsequent requests. Telegram session
ownership is released between requests. An idle browser does not hold the
session lock. Another CLI operation can still cause a transient lock conflict;
the reader never kills it or copies its session.

History and advertisement retrieval use the existing read-only client and do
not mark history read or cache its content on disk. An existing authorized
session, ownership sidecar, and matching account cache are required. No login
or account initialization happens in the reader. Explicit `--account` and
`--allow-write` are required; `--read-only` rejects startup. A dry run opens no
listener, client, or account path.

The write gate authorizes genuine sponsored-message callbacks from this human
surface. They use the normal rate guard, durable request ledger, audit and
idempotency pipeline. The server attempts a view once for each cached ad; the
account's durable key also suppresses a repeated view of the same Telegram ad ID.
Uncertain clicks/reports are never retried automatically. A new deliberate click
is a new interaction. Inspect `operations-list` when a write outcome is unclear.
The browser requires trusted input for clicks/reports and focused, unobscured,
fully visible ad text for views. These checks are not proof against a malicious
process that already has the local access token.

## Sponsored-message surface

The implementation follows the [official sponsored-message interface](https://core.telegram.org/api/sponsored-messages)
and the supported [Telegram link forms](https://core.telegram.org/api/links):

- Cache retrieval for five minutes, including empty responses. Display the first
  ad after the latest channel message, or above a bot chat. There is no inserted
  multi-ad timeline, so minimum post spacing is not shortened.
- Show title, formatted text, required supported media/avatar, the supplied
  button, Recommended/Sponsored labeling or a blue bot prefix, sponsor details,
  and server-provided report choices when reporting is allowed.
- Send view callbacks only when the whole ad text is visible, and use the
  media/fullscreen flags for the corresponding human interactions. Expired
  advertisements stop the reading surface until Refresh.
- Confirm external destinations. Understood Telegram username, bot-start and
  message/topic links use native `tg://` links. Other Telegram deep-link forms
  are withheld rather than losing their parameters or opening the wrong target.

Supported ad media: JPEG/PNG, MP4/WebM video and animation, bounded to 20 MiB per
file and at most two cached assets. Basic and retrieved RGB accent palettes are
supported. Custom emoji, collectible/background emoji patterns, unsupported
entities/media and channel-video overlay advertisements are not supported: the
reader withholds the page instead of silently removing required ad content.
Ordinary channel videos have no in-reader playback or overlay-ad surface.

This is a bounded additional surface, not a claim that the existing headless
channel commands meet every Telegram display requirement. That separate release
scope question, and disposable-account/forum live acceptance, remain open.

## Verification

Go tests cover localhost Host/Origin/token checks, JSON bounds, cancellation,
continuations, five-minute caching, asset readiness, UTF-16 boundaries, report
options, account/peer binding, durable callback routing and unknown outcomes.
Run browser policy tests with `node --test internal/reader/logic_test.mjs` (Node 22).

For browser development, use an explicit synthetic fixture:

```bash
TGCTL_READER_BROWSER_DIR=/tmp/tgctl-reader-fixture \
  go test ./internal/reader -run '^TestBrowserFixture$' -timeout 35m
```

It writes the local URL to that directory and records synthetic actions there.
It has no Telegram client. Browser automation must use this fixture; never
manufacture real ad views, clicks or moderator reports for a smoke test.
The official ad test channel may be fetched separately without engagement.
