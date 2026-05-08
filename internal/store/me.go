package store

import (
	"database/sql"
)

// MeRow mirrors the tg_me table.
type MeRow struct {
	UserID      int64
	Username    sql.NullString
	Phone       sql.NullString
	FirstName   sql.NullString
	LastName    sql.NullString
	DisplayName sql.NullString
	IsBot       int
	CachedAt    string
	RawJSON     sql.NullString
}

// LoadMe returns the cached self-user row, or sql.ErrNoRows if absent.
func LoadMe(db *sql.DB) (*MeRow, error) {
	var r MeRow
	err := db.QueryRow(`
		SELECT user_id, username, phone, first_name, last_name,
		       display_name, is_bot, cached_at, raw_json
		FROM tg_me
		WHERE key = 'self'
	`).Scan(
		&r.UserID, &r.Username, &r.Phone, &r.FirstName, &r.LastName,
		&r.DisplayName, &r.IsBot, &r.CachedAt, &r.RawJSON,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpsertMe writes or replaces the single self-row in tg_me.
func UpsertMe(db *sql.DB, r MeRow) error {
	_, err := db.Exec(`
		INSERT INTO tg_me(
			key, user_id, username, phone, first_name, last_name,
			display_name, is_bot, cached_at, raw_json
		) VALUES ('self', ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			user_id      = excluded.user_id,
			username     = excluded.username,
			phone        = excluded.phone,
			first_name   = excluded.first_name,
			last_name    = excluded.last_name,
			display_name = excluded.display_name,
			is_bot       = excluded.is_bot,
			cached_at    = excluded.cached_at,
			raw_json     = excluded.raw_json
	`,
		r.UserID, r.Username, r.Phone, r.FirstName, r.LastName,
		r.DisplayName, r.IsBot, r.CachedAt, r.RawJSON,
	)
	return err
}
