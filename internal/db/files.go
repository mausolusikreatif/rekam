package db

import (
	"database/sql"
	"fmt"
	"time"
)

// File is a stored binary blob (currently images uploaded from the editor).
type File struct {
	ID        string
	Mime      string
	Size      int64
	Data      []byte
	CreatedAt time.Time
}

// OpenFileStore opens (or creates) a *.files SQLite file. Blobs live here rather
// than in the *.memory DB so binary reads/writes never contend with the single,
// FTS-triggered memories connection. It is opened through the same Manager, so
// it still inherits the fd-budget and idle-reaper.
func OpenFileStore(path string) (*MemoryDB, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, fmt.Errorf("open rekam files %s: %w", path, err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS files (
		id         TEXT PRIMARY KEY,
		mime       TEXT NOT NULL,
		size       INTEGER NOT NULL,
		data       BLOB NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate rekam files %s: %w", path, err)
	}
	return &MemoryDB{db: db, path: path}, nil
}

// PutFile stores a blob under id.
func (m *MemoryDB) PutFile(id, mime string, data []byte) error {
	_, err := m.db.Exec(
		`INSERT INTO files (id, mime, size, data, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, mime, len(data), data, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("put file %s: %w", id, err)
	}
	return nil
}

// GetFile returns the blob stored under id, or an error if it does not exist.
func (m *MemoryDB) GetFile(id string) (*File, error) {
	var f File
	var created string
	err := m.db.QueryRow(
		`SELECT id, mime, size, data, created_at FROM files WHERE id = ?`, id,
	).Scan(&f.ID, &f.Mime, &f.Size, &f.Data, &created)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("file not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get file %s: %w", id, err)
	}
	f.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &f, nil
}
