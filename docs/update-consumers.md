# Durable update consumers

A successful stdout write only means the operating system accepted bytes. It
does not prove another program saved an update. The default `listen` acknowledges
after stdout accepts the event, so a downstream crash can lose that program's
work. Use explicit acknowledgement when building an inbox or another durable
consumer.

## Receive, save, acknowledge

1. Run `tg --account work listen --once --manual-ack --allow-write --json`.
   The `listen.event` envelope contains `event_id`, `receipt` and
   `ack_required: true`. The final `listen` summary is not an acknowledgement.
   The command releases its Telegram session when it exits.
2. Commit the event and a deduplication key to your own durable inbox. Use a
   namespace for the account/cache instance plus `event_id`. A copied, restored
   or replaced cache needs reconciliation or a fresh consumer namespace.
   An event ID identifies one local queue occurrence. Telegram recovery can
   repeat the same logical update in a new occurrence, so also deduplicate
   business actions by their source message/update identity and intended action.
3. Run `tg --account work events-ack "$RECEIPT" --allow-write --json`, using
   the receipt from that event. This deletes only that local queue row. It does
   not mark a Telegram message read or send a message.
4. Repeat. Process the durable inbox separately. Persist before acknowledging;
   perform replies or other remote actions through their usual explicit write
   gates and idempotency keys derived from that stable action identity, not
   solely from the queue event ID.

After a crash, `tg --account work events-list --read-only --limit 20 --json`
returns the oldest pending rows without connecting to Telegram or consuming
anything. Each result contains `event_id`, `receipt` and nested `event`.
Repeated listing is stable until something acknowledges rows or new updates
arrive. `has_more` indicates additional pending rows; save and acknowledge the
current batch before requesting the next one. The limit is 1–100.

`events-ack --dry-run --allow-write` validates a receipt and reports `pending`
without removing it. A repeated acknowledgement returns `already_absent: true`;
that means the row is absent, not proof that a consumer processed it. If output
is lost after a successful acknowledgement, repeating the same receipt is safe.
Receipts bind the account name, cache path, row ID and exact saved payload.
They are consistency tokens, not credentials or authorization capabilities.
Wrong-account and changed-payload receipts fail without deleting data.

## Scope and operational limits

This is a single shared queue with at-least-once delivery, not independent
subscriptions or exactly-once processing. Keep one consumer workflow in control.
Do not mix manual consumption with ordinary `listen` or `sync --follow`: those
commands automatically acknowledge rows, including filtered-out events.
`--manual-ack` therefore requires `--once` and forbids event filters.
Filter only after saving the complete event in the consumer inbox.

Local list/ack operations can run while another process owns the Telegram
session. Receiving fresh updates still needs a writable Telegram connection;
listing the queue does not refresh it. There is no daemon, automatic reply,
worker lease or fan-out service. Once-mode deliberately releases the session so
a caller can run another network command. Reconnecting for every event has a
cost; drain pending rows locally before reopening Telegram.

The queue persists normalized updates, not an authoritative immutable history.
Updates can be replayed, and gaps that Telegram can no longer recover remain
errors. Pending rows are retained until acknowledged, except that observing an
expiring-message update removes older pending payloads for that peer/message.
The content-free `unsupported_expiring_message` notice lets consumers discard
their own saved payloads and cancel pending actions for that message. Already
delivered copies cannot be retracted by the CLI. Queue IDs can have gaps; absence
is not proof of successful processing. Consumers must monitor disk space. Corrupt
or oversized rows fail visibly rather than being discarded.
Treat event contents as private untrusted input. These commands do not establish
permission to process third-party messages with an AI service or to reply to them.

## Roadmap and verification

Priority A, small-to-medium reliability repair. The concrete use case is a
consumer crashing after pipe delivery but before its database commit. Evidence:
`commands/live.go` previously acknowledged immediately after `output.Emit`.
The existing gotd recovery manager and transactional SQLite outbox provide the
required support; no new Telegram RPC, dependency or schema migration is needed.
Account safety remains unchanged: session ownership is exclusive, local ack is
write-gated, and no remote mutation occurs.

Store tests cover non-consuming replay, ordering/bounds, selective and repeated
acknowledgement, concurrent acknowledgements, account/cache and payload mismatch,
cancellation, corruption and dry-run. Command tests cover offline operation while
the session is locked, no unintended state creation, write/read-only gates,
manual replay and broken stdout. A real update-storage regression verifies that
replaying an older durable event cannot overwrite a newer cached update.
