package store

import (
	"database/sql"
	"fmt"
)

// Old positive IDs can have irreversibly mixed user, group and channel rows.
// Preserve those caches verbatim, rather than guess message ownership. A fresh
// discovery/backfill repopulates the canonical tables; legacy export remains
// available for inspecting the preserved snapshot.
func archiveLegacyIdentity(tx *sql.Tx) error {
	if !tableExists(tx, "tg_chats") || tableExists(tx, "tg_cache_identity") {
		return nil
	}
	var total int
	for _, table := range []string{"tg_chats", "tg_entities", "tg_messages", "tg_sync_state"} {
		if !tableExists(tx, table) {
			continue
		}
		var count int
		if err := tx.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			return err
		}
		total += count
	}
	if total == 0 {
		return nil
	}
	for _, table := range []string{"tg_chats", "tg_entities", "tg_messages", "tg_sync_state"} {
		if tableExists(tx, table) {
			if _, err := tx.Exec("ALTER TABLE " + table + " RENAME TO " + table + "_legacy_v1"); err != nil {
				return fmt.Errorf("preserve legacy cache: %w", err)
			}
		}
	}
	for _, index := range []string{"idx_messages_chat_date", "idx_messages_date", "idx_messages_chat_grouped", "idx_sync_state_updated"} {
		if _, err := tx.Exec("DROP INDEX IF EXISTS " + index); err != nil {
			return err
		}
	}
	return nil
}

// ConnectLegacyReadonly exposes the preserved snapshot through connection-local
// views. Only export should use this: legacy IDs must never reach Telegram.
func ConnectLegacyReadonly(path string) (*sql.DB, error) {
	db, err := ConnectReadonly(path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if !tableExists(db, "tg_messages_legacy_v1") {
		db.Close()
		return nil, fmt.Errorf("no preserved legacy cache exists")
	}
	if _, err = db.Exec("PRAGMA temp_store=MEMORY"); err != nil {
		db.Close()
		return nil, err
	}
	for _, table := range []string{"tg_chats", "tg_messages"} {
		if _, err = db.Exec("CREATE TEMP VIEW " + table + " AS SELECT * FROM main." + table + "_legacy_v1"); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}
