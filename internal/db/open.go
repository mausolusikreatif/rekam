package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// openDB opens a SQLite database with WAL mode and foreign keys enabled.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sql.Open %s: %w", path, err)
	}
	// Single connection: SQLite is single-writer; a pool causes FTS trigger races.
	db.SetMaxOpenConns(1)
	// WAL for better read concurrency; FK for referential integrity.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma setup %s: %w", path, err)
	}
	return db, nil
}
