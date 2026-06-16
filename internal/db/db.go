// Package db provides SQLite-backed persistence for GACOS tasks.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection.
type DB struct {
	db     *sql.DB
	dbPath string
}

// Task represents a GACOS submission task.
type Task struct {
	ID          string
	Dates       []string
	North       float64
	West        float64
	East        float64
	South       float64
	Hour        int
	Minute      int
	Type        int
	Email       string
	Status      string
	Response    string
	SubmittedAt time.Time
	Retries     int
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Download represents a downloaded archive.
type Download struct {
	ID           int64
	TaskID       string
	URL          string
	FilePath     string
	SizeBytes    int64
	Status       string
	DownloadedAt time.Time
	Retries      int
	LastError    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Extraction represents an extracted archive.
type Extraction struct {
	ID          int64
	DownloadID  int64
	ArchivePath string
	ExtractedTo string
	Files       []string
	Status      string
	ExtractedAt time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Open opens or creates the SQLite database at path.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite requires serialized access for writes

	d := &DB{db: db, dbPath: path}
	if err := d.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate db: %w", err)
	}
	return d, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    dates TEXT NOT NULL,
    north REAL NOT NULL,
    west REAL NOT NULL,
    east REAL NOT NULL,
    south REAL NOT NULL,
    hour INTEGER NOT NULL,
    minute INTEGER NOT NULL,
    type INTEGER NOT NULL,
    email TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    response TEXT,
    submitted_at DATETIME,
    retries INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);

CREATE TABLE IF NOT EXISTS downloads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    url TEXT NOT NULL UNIQUE,
    file_path TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    downloaded_at DATETIME,
    retries INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_downloads_status ON downloads(status);

CREATE TABLE IF NOT EXISTS extractions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    download_id INTEGER NOT NULL UNIQUE REFERENCES downloads(id) ON DELETE CASCADE,
    archive_path TEXT NOT NULL,
    extracted_to TEXT,
    files TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    extracted_at DATETIME,
    last_error TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_extractions_status ON extractions(status);

CREATE TRIGGER IF NOT EXISTS trg_tasks_updated
AFTER UPDATE ON tasks
BEGIN
    UPDATE tasks SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trg_downloads_updated
AFTER UPDATE ON downloads
BEGIN
    UPDATE downloads SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trg_extractions_updated
AFTER UPDATE ON extractions
BEGIN
    UPDATE extractions SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
`
	if _, err := d.db.Exec(schema); err != nil {
		return fmt.Errorf("exec schema: %w", err)
	}
	return nil
}

// --- Task operations ---

// GetTask returns a task by id.
func (d *DB) GetTask(ctx context.Context, id string) (*Task, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT id, dates, north, west, east, south, hour, minute, type, email,
		       status, response, submitted_at, retries, last_error, created_at, updated_at
		FROM tasks WHERE id = ?`, id)
	return scanTask(row)
}

// GetTasksByStatus returns tasks filtered by status (empty = all).
func (d *DB) GetTasksByStatus(ctx context.Context, status string) ([]Task, error) {
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = d.db.QueryContext(ctx, `SELECT id, dates, north, west, east, south, hour, minute, type, email,
		       status, response, submitted_at, retries, last_error, created_at, updated_at FROM tasks`)
	} else {
		rows, err = d.db.QueryContext(ctx, `SELECT id, dates, north, west, east, south, hour, minute, type, email,
		       status, response, submitted_at, retries, last_error, created_at, updated_at FROM tasks WHERE status = ?`, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

// UpsertTask inserts or updates a task.
func (d *DB) UpsertTask(ctx context.Context, t *Task) error {
	datesJSON, err := json.Marshal(t.Dates)
	if err != nil {
		return fmt.Errorf("marshal dates: %w", err)
	}

	_, err = d.db.ExecContext(ctx, `
		INSERT INTO tasks (id, dates, north, west, east, south, hour, minute, type, email, status, response, submitted_at, retries, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			dates = excluded.dates,
			north = excluded.north,
			west = excluded.west,
			east = excluded.east,
			south = excluded.south,
			hour = excluded.hour,
			minute = excluded.minute,
			type = excluded.type,
			email = excluded.email,
			status = excluded.status,
			response = excluded.response,
			submitted_at = excluded.submitted_at,
			retries = excluded.retries,
			last_error = excluded.last_error`,
		t.ID, string(datesJSON), t.North, t.West, t.East, t.South, t.Hour, t.Minute, t.Type, t.Email,
		t.Status, t.Response, sqlTime(t.SubmittedAt), t.Retries, t.LastError)
	return err
}

func scanTask(scanner interface {
	Scan(dest ...interface{}) error
}) (*Task, error) {
	var t Task
	var datesJSON string
	var submittedAt sql.NullTime
	err := scanner.Scan(
		&t.ID, &datesJSON, &t.North, &t.West, &t.East, &t.South,
		&t.Hour, &t.Minute, &t.Type, &t.Email, &t.Status, &t.Response,
		&submittedAt, &t.Retries, &t.LastError, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(datesJSON), &t.Dates); err != nil {
		return nil, fmt.Errorf("unmarshal dates: %w", err)
	}
	if submittedAt.Valid {
		t.SubmittedAt = submittedAt.Time
	}
	return &t, nil
}

// --- Download operations ---

// GetDownloadByURL returns a download by URL.
func (d *DB) GetDownloadByURL(ctx context.Context, url string) (*Download, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT id, task_id, url, file_path, size_bytes, status, downloaded_at, retries, last_error, created_at, updated_at
		FROM downloads WHERE url = ?`, url)
	return scanDownload(row)
}

// UpsertDownload inserts or updates a download.
func (d *DB) UpsertDownload(ctx context.Context, dl *Download) error {
	// If ID is 0, let SQLite auto-increment. Use NULL for id in that case.
	var id interface{}
	if dl.ID != 0 {
		id = dl.ID
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO downloads (id, task_id, url, file_path, size_bytes, status, downloaded_at, retries, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
			task_id = excluded.task_id,
			file_path = excluded.file_path,
			size_bytes = excluded.size_bytes,
			status = excluded.status,
			downloaded_at = excluded.downloaded_at,
			retries = excluded.retries,
			last_error = excluded.last_error`,
		id, dl.TaskID, dl.URL, dl.FilePath, dl.SizeBytes, dl.Status,
		sqlTime(dl.DownloadedAt), dl.Retries, dl.LastError)
	return err
}

func scanDownload(scanner interface {
	Scan(dest ...interface{}) error
}) (*Download, error) {
	var dl Download
	var downloadedAt sql.NullTime
	err := scanner.Scan(
		&dl.ID, &dl.TaskID, &dl.URL, &dl.FilePath, &dl.SizeBytes, &dl.Status,
		&downloadedAt, &dl.Retries, &dl.LastError, &dl.CreatedAt, &dl.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if downloadedAt.Valid {
		dl.DownloadedAt = downloadedAt.Time
	}
	return &dl, nil
}

// --- Extraction operations ---

// GetExtractionByArchivePath returns an extraction by archive path.
func (d *DB) GetExtractionByArchivePath(ctx context.Context, archivePath string) (*Extraction, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT id, download_id, archive_path, extracted_to, files, status, extracted_at, last_error, created_at, updated_at
		FROM extractions WHERE archive_path = ?`, archivePath)
	return scanExtraction(row)
}

// UpsertExtraction inserts or updates an extraction.
func (d *DB) UpsertExtraction(ctx context.Context, e *Extraction) error {
	filesJSON, err := json.Marshal(e.Files)
	if err != nil {
		return fmt.Errorf("marshal files: %w", err)
	}
	// If ID is 0, let SQLite auto-increment.
	var id interface{}
	if e.ID != 0 {
		id = e.ID
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO extractions (id, download_id, archive_path, extracted_to, files, status, extracted_at, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(download_id) DO UPDATE SET
			archive_path = excluded.archive_path,
			extracted_to = excluded.extracted_to,
			files = excluded.files,
			status = excluded.status,
			extracted_at = excluded.extracted_at,
			last_error = excluded.last_error`,
		id, e.DownloadID, e.ArchivePath, e.ExtractedTo, string(filesJSON), e.Status,
		sqlTime(e.ExtractedAt), e.LastError)
	return err
}

func scanExtraction(scanner interface {
	Scan(dest ...interface{}) error
}) (*Extraction, error) {
	var e Extraction
	var filesJSON string
	var extractedAt sql.NullTime
	err := scanner.Scan(
		&e.ID, &e.DownloadID, &e.ArchivePath, &e.ExtractedTo, &filesJSON,
		&e.Status, &extractedAt, &e.LastError, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(filesJSON), &e.Files); err != nil {
		return nil, fmt.Errorf("unmarshal files: %w", err)
	}
	if extractedAt.Valid {
		e.ExtractedAt = extractedAt.Time
	}
	return &e, nil
}

// --- Summary ---

// Summary holds aggregate counts.
type Summary struct {
	SubmissionsTotal     int
	SubmissionsSubmitted int
	SubmissionsCompleted int
	SubmissionsFailed    int
	DownloadsTotal       int
	DownloadsPending     int
	DownloadsFailed      int
	ExtractionsTotal     int
	ExtractionsFailed    int
}

// GetSummary returns aggregate counts.
func (d *DB) GetSummary(ctx context.Context) (*Summary, error) {
	s := &Summary{}
	err := d.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'submitted' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM tasks`).Scan(&s.SubmissionsTotal, &s.SubmissionsSubmitted, &s.SubmissionsCompleted, &s.SubmissionsFailed)
	if err != nil {
		return nil, fmt.Errorf("tasks summary: %w", err)
	}

	err = d.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM downloads`).Scan(&s.DownloadsTotal, &s.DownloadsPending, &s.DownloadsFailed)
	if err != nil {
		return nil, fmt.Errorf("downloads summary: %w", err)
	}

	err = d.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM extractions`).Scan(&s.ExtractionsTotal, &s.ExtractionsFailed)
	if err != nil {
		return nil, fmt.Errorf("extractions summary: %w", err)
	}
	return s, nil
}

func sqlTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}
