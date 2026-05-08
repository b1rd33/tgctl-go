package store

import (
	"database/sql"
	"time"
)

// EntityKind is the access-hash partition. Telegram peer ids are *not* unique
// across kinds: a user with id 100 and a channel with id 100 are different
// entities and need different access hashes.
type EntityKind string

const (
	EntityUser    EntityKind = "user"
	EntityChannel EntityKind = "channel"
	EntityChat    EntityKind = "chat" // basic group, no access_hash
)

// UpsertEntity persists an (id, kind, access_hash) tuple. Use AccessHash=0 for
// basic groups.
func UpsertEntity(db *sql.DB, id int64, kind EntityKind, accessHash int64) error {
	_, err := db.Exec(`
		INSERT INTO tg_entities(id, kind, access_hash, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind        = excluded.kind,
			access_hash = excluded.access_hash,
			updated_at  = excluded.updated_at`,
		id, string(kind), accessHash, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// LoadEntity returns (kind, access_hash, true) when a row exists, else
// ("", 0, false).
func LoadEntity(db *sql.DB, id int64) (EntityKind, int64, bool) {
	var k string
	var hash sql.NullInt64
	err := db.QueryRow(
		"SELECT kind, access_hash FROM tg_entities WHERE id = ?", id,
	).Scan(&k, &hash)
	if err != nil {
		return "", 0, false
	}
	if hash.Valid {
		return EntityKind(k), hash.Int64, true
	}
	return EntityKind(k), 0, true
}
