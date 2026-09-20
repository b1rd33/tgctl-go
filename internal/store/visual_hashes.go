package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/bits"
	"sort"
	"strconv"
)

func StoreVisualHash(ctx context.Context, db *sql.DB, sha, dhash string) error {
	if _, err := normalizeSHA256(sha); err != nil {
		return err
	}
	if _, err := ParseVisualHash(dhash); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tg_visual_hashes(sha256 TEXT PRIMARY KEY,dhash TEXT NOT NULL)`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO tg_visual_hashes(sha256,dhash) VALUES(?,?) ON CONFLICT(sha256) DO UPDATE SET dhash=excluded.dhash`, sha, dhash)
	return err
}

func ParseVisualHash(value string) (uint64, error) {
	if len(value) != 16 {
		return 0, fmt.Errorf("dhash must contain exactly 16 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return 0, err
	}
	return strconv.ParseUint(value, 16, 64)
}

type VisualMatch struct {
	MediaHashMatch
	Distance int `json:"distance"`
}

// FindSimilarMedia scans at most 10000 indexed representations. It reports
// incomplete coverage separately from result truncation, and never hides it.
func FindSimilarMedia(ctx context.Context, db *sql.DB, hash string, maxDistance, limit int) ([]VisualMatch, bool, bool, error) {
	query, err := ParseVisualHash(hash)
	if err != nil {
		return nil, false, false, err
	}
	if maxDistance < 0 || maxDistance > 64 || limit < 1 || limit > 1000 {
		return nil, false, false, fmt.Errorf("invalid visual search bounds")
	}
	matches := []VisualMatch{}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('tg_visual_hashes','tg_media_hashes')`).Scan(&count); err != nil {
		return nil, false, false, err
	}
	if count != 2 {
		return matches, false, false, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT h.chat_id,h.message_id,h.sha256,h.bytes,h.media_type,h.media_path,h.representation,v.dhash
 FROM tg_media_hashes h JOIN tg_visual_hashes v ON v.sha256=h.sha256 JOIN tg_messages m ON m.chat_id=h.chat_id AND m.message_id=h.message_id
 WHERE COALESCE(m.deleted,0)=0 AND COALESCE(m.has_media,0)!=0 AND COALESCE(m.media_id,'')=COALESCE(h.media_id,'')
 AND (h.representation='upload_original' OR COALESCE(m.media_path,'')=h.media_path)
 ORDER BY h.chat_id,h.message_id,h.representation LIMIT 10001`)
	if err != nil {
		return nil, false, false, err
	}
	defer rows.Close()
	scanned := 0
	incomplete := false
	for rows.Next() {
		scanned++
		if scanned > 10000 {
			incomplete = true
			break
		}
		var m VisualMatch
		var raw string
		if err := rows.Scan(&m.ChatID, &m.MessageID, &m.SHA256, &m.Bytes, &m.MediaType, &m.MediaPath, &m.Representation, &raw); err != nil {
			return nil, false, false, err
		}
		value, err := ParseVisualHash(raw)
		if err != nil {
			return nil, false, false, err
		}
		m.Distance = bits.OnesCount64(query ^ value)
		if m.Distance <= maxDistance {
			matches = append(matches, m)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, false, err
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Distance < matches[j].Distance })
	truncated := len(matches) > limit
	if truncated {
		matches = matches[:limit]
	}
	return matches, incomplete, truncated, nil
}
