package store

// Schema mirrors tgcli/db.py SCHEMA plus the column migrations that Python
// applies in _migrate (media_path, deleted, left). Go ports the migrated
// final state in one statement.
const Schema = `
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
    raw_json         TEXT,
    deleted          INTEGER DEFAULT 0,
    PRIMARY KEY (chat_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_messages_chat_date ON tg_messages(chat_id, date DESC);
CREATE INDEX IF NOT EXISTS idx_messages_date ON tg_messages(date DESC);

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
`
