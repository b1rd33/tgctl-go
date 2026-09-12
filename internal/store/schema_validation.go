package store

import (
	"fmt"
	"regexp"
	"strings"
)

var schemaTables = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS (\w+)\s*\((.*?)\);`)

// validateSchema rejects unsupported or damaged caches without upgrading them.
func validateSchema(db schemaDB, allowEmpty bool) error {
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
		return err
	}
	if count == 0 && allowEmpty {
		return nil
	}
	var version int
	if err := db.QueryRow("SELECT CASE WHEN count(*) = 1 THEN min(version) ELSE -1 END FROM tg_cache_identity").Scan(&version); err != nil || version != 2 {
		return fmt.Errorf("unsupported cache schema: use a new account cache; automatic conversion is not supported")
	}
	for _, table := range schemaTables.FindAllStringSubmatch(Schema, -1) {
		var columns []string
		for _, field := range strings.Split(table[2], ",") {
			name := strings.Fields(strings.TrimSpace(field))[0]
			// Table constraints and their continuation are not column definitions.
			if strings.ContainsAny(name, "()") || name == "PRIMARY" || name == "UNIQUE" || name == "CHECK" {
				continue
			}
			columns = append(columns, name)
		}
		rows, err := db.Query("SELECT " + strings.Join(columns, ",") + " FROM " + table[1] + " LIMIT 0")
		if err != nil {
			return fmt.Errorf("unsupported cache schema for %s: %w", table[1], err)
		}
		rows.Close()
	}
	return nil
}
