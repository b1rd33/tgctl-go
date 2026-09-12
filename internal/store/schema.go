package store

// Schema mirrors tgcli/db.py SCHEMA plus the column migrations that Python
// applies in _migrate (media_path, deleted, left). Go ports the migrated
// final state in one statement.
const Schema = `
CREATE TABLE IF NOT EXISTS tg_account_identity(slot INTEGER PRIMARY KEY CHECK(slot=1),user_id INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS tg_cache_identity(version INTEGER PRIMARY KEY);
INSERT OR IGNORE INTO tg_cache_identity(version) VALUES(2);
CREATE TABLE IF NOT EXISTS tg_chats (
    chat_id      INTEGER PRIMARY KEY,
    type         TEXT,
    title        TEXT,
    username     TEXT,
    phone        TEXT,
    first_name   TEXT,
    last_name    TEXT,
    is_bot       INTEGER,
    last_seen_at TEXT,
    raw_json     TEXT,
    left         INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS tg_messages (
    chat_id          INTEGER,
    message_id       INTEGER,
    sender_id        INTEGER,
    date             TEXT,
    text             TEXT,
    is_outgoing      INTEGER,
    reply_to_msg_id  INTEGER,
    has_media        INTEGER,
    media_type       TEXT,
    media_path       TEXT,
    media_id         TEXT,
    grouped_id       INTEGER,
    raw_json         TEXT,
    deleted          INTEGER DEFAULT 0,
    edit_date INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (chat_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_messages_chat_date ON tg_messages(chat_id, date DESC);
CREATE INDEX IF NOT EXISTS idx_messages_date ON tg_messages(date DESC);

CREATE TABLE IF NOT EXISTS tg_update_state(user_id INTEGER PRIMARY KEY,pts INTEGER NOT NULL,qts INTEGER NOT NULL,date INTEGER NOT NULL,seq INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS tg_channel_state(user_id INTEGER NOT NULL,channel_id INTEGER NOT NULL,pts INTEGER NOT NULL DEFAULT 0,access_hash INTEGER,PRIMARY KEY(user_id,channel_id));
CREATE TABLE IF NOT EXISTS tg_event_outbox(id INTEGER PRIMARY KEY AUTOINCREMENT,event TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS tg_read_state(chat_id INTEGER PRIMARY KEY,max_id INTEGER NOT NULL,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS tg_common_deleted(message_id INTEGER PRIMARY KEY);
CREATE TRIGGER IF NOT EXISTS common_delete_tombstone AFTER INSERT ON tg_messages WHEN NEW.chat_id > -1000000000000 AND EXISTS(SELECT 1 FROM tg_common_deleted WHERE message_id=NEW.message_id) BEGIN UPDATE tg_messages SET deleted=1 WHERE chat_id=NEW.chat_id AND message_id=NEW.message_id; END;
CREATE TABLE IF NOT EXISTS tg_contacts (
    user_id    INTEGER PRIMARY KEY,
    phone      TEXT,
    first_name TEXT,
    last_name  TEXT,
    username   TEXT,
    is_mutual  INTEGER,
    synced_at  TEXT
);

CREATE TABLE IF NOT EXISTS tg_me (
    key          TEXT PRIMARY KEY CHECK (key = 'self'),
    user_id      INTEGER,
    username     TEXT,
    phone        TEXT,
    first_name   TEXT,
    last_name    TEXT,
    display_name TEXT,
    is_bot       INTEGER,
    cached_at    TEXT,
    raw_json     TEXT
);

CREATE TABLE IF NOT EXISTS tg_idempotency (
    key         TEXT PRIMARY KEY,
    command     TEXT NOT NULL,
    request_id  TEXT NOT NULL,
    result_json TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tg_entities (
    id          INTEGER PRIMARY KEY,
    kind        TEXT NOT NULL,
    access_hash INTEGER,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tg_rpc_cooldowns(scope TEXT PRIMARY KEY,until_unix INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS tg_write_window(id INTEGER PRIMARY KEY AUTOINCREMENT,at_unix INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS tg_write_calls (
 call_id TEXT PRIMARY KEY, request_id TEXT NOT NULL, method TEXT NOT NULL,
 fingerprint TEXT NOT NULL, request BLOB NOT NULL, response BLOB, state TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tg_sync_state (
    account         TEXT NOT NULL DEFAULT 'default',
    chat_id         INTEGER NOT NULL,
    last_message_id INTEGER NOT NULL DEFAULT 0,
    last_sync_at    TEXT,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (account, chat_id)
);

CREATE INDEX IF NOT EXISTS idx_sync_state_updated ON tg_sync_state(updated_at);
`
