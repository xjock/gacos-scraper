// Package state persists GACOS task progress in a SQLite database.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xjock/gacos-scraper/internal/config"
	"github.com/xjock/gacos-scraper/internal/db"
)

// State wraps a SQLite database for persistence.
type State struct {
	db     *db.DB
	dbPath string
}

// Load opens the SQLite database at path, migrating from legacy state.json if present.
func Load(path string) (*State, error) {
	legacyPath := path
	if filepath.Ext(path) == ".db" {
		legacyPath = path[:len(path)-len(filepath.Ext(path))] + ".json"
	} else {
		legacyPath = path + ".json"
	}

	d, err := db.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}

	s := &State{db: d, dbPath: path}

	if _, err := os.Stat(legacyPath); err == nil {
		if err := s.migrateFromJSON(legacyPath); err != nil {
			_ = d.Close()
			return nil, fmt.Errorf("migrate legacy state: %w", err)
		}
	}

	return s, nil
}

// Close closes the underlying database.
func (s *State) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// DBPath returns the database file path.
func (s *State) DBPath() string {
	return s.dbPath
}

// GetSubmission returns a task by id.
func (s *State) GetSubmission(key string) (db.Task, bool) {
	task, err := s.db.GetTask(context.Background(), key)
	if err != nil {
		return db.Task{}, false
	}
	return *task, true
}

// RecordSubmission records a submission attempt.
func (s *State) RecordSubmission(key string, cfg *config.GacosConfig, dates []string, status, response, err string) error {
	t := &db.Task{
		ID:        key,
		Dates:     dates,
		North:     cfg.North,
		West:      cfg.West,
		East:      cfg.East,
		South:     cfg.South,
		Hour:      cfg.Hour,
		Minute:    cfg.Minute,
		Type:      cfg.Type,
		Email:     cfg.Email,
		Status:    status,
		Response:  response,
		LastError: err,
	}
	if status == "submitted" {
		t.SubmittedAt = time.Now()
	}
	if status == "failed" {
		// Read existing retries to increment.
		if existing, _ := s.db.GetTask(context.Background(), key); existing != nil {
			t.Retries = existing.Retries + 1
		} else {
			t.Retries = 1
		}
	}
	return s.db.UpsertTask(context.Background(), t)
}

// GetAllTasks returns every tracked submission task.
func (s *State) GetAllTasks() ([]db.Task, error) {
	return s.db.GetTasksByStatus(context.Background(), "")
}

// GetPendingSubmissions returns all submitted tasks awaiting download.
func (s *State) GetPendingSubmissions() ([]db.Task, error) {
	return s.db.GetTasksByStatus(context.Background(), "submitted")
}

// IsDownloaded reports whether url has been downloaded successfully.
func (s *State) IsDownloaded(url string) bool {
	dl, err := s.db.GetDownloadByURL(context.Background(), url)
	if err != nil {
		return false
	}
	return dl.Status == "downloaded" || dl.Status == "extracted"
}

// RecordDownload records a download attempt.
func (s *State) RecordDownload(taskID, url, filePath string, size int64, status, err string) error {
	dl := &db.Download{
		TaskID:    taskID,
		URL:       url,
		FilePath:  filePath,
		SizeBytes: size,
		Status:    status,
		LastError: err,
	}
	if status == "downloaded" || status == "extracted" {
		dl.DownloadedAt = time.Now()
	}
	if status == "failed" {
		if existing, _ := s.db.GetDownloadByURL(context.Background(), url); existing != nil {
			dl.Retries = existing.Retries + 1
		} else {
			dl.Retries = 1
		}
	}
	return s.db.UpsertDownload(context.Background(), dl)
}

// IsExtracted reports whether archivePath has been extracted.
func (s *State) IsExtracted(archivePath string) bool {
	e, err := s.db.GetExtractionByArchivePath(context.Background(), archivePath)
	if err != nil {
		return false
	}
	return e.Status == "extracted"
}

// RecordExtraction records an extraction attempt.
func (s *State) RecordExtraction(archivePath, extractedTo string, files []string, status, err string) error {
	e := &db.Extraction{
		ArchivePath: archivePath,
		ExtractedTo: extractedTo,
		Files:       files,
		Status:      status,
		LastError:   err,
	}
	if status == "extracted" {
		e.ExtractedAt = time.Now()
	}
	return s.db.UpsertExtraction(context.Background(), e)
}

// GetSummary returns aggregate progress.
func (s *State) GetSummary() db.Summary {
	sum, err := s.db.GetSummary(context.Background())
	if err != nil {
		return db.Summary{}
	}
	return *sum
}

// migrateFromJSON imports tasks from legacy JSON state file.
func (s *State) migrateFromJSON(path string) error {
	legacy, err := loadLegacyJSON(path)
	if err != nil {
		return err
	}
	if legacy == nil {
		return nil
	}
	ctx := context.Background()
	for _, sub := range legacy.Submissions {
		// Do not overwrite an existing task; legacy JSON may lack params.
		if _, err := s.db.GetTask(ctx, sub.Key); err == nil {
			continue
		}
		t := &db.Task{
			ID:          sub.Key,
			Dates:       sub.Dates,
			Status:      sub.Status,
			SubmittedAt: sub.SubmittedAt,
			Retries:     sub.Retries,
			LastError:   sub.LastError,
		}
		if err := s.db.UpsertTask(ctx, t); err != nil {
			return fmt.Errorf("migrate task %s: %w", sub.Key, err)
		}
	}
	for _, d := range legacy.Downloads {
		// Do not overwrite an existing download.
		if _, err := s.db.GetDownloadByURL(ctx, d.URL); err == nil {
			continue
		}
		dl := &db.Download{
			URL:          d.URL,
			FilePath:     d.FilePath,
			SizeBytes:    d.SizeBytes,
			Status:       d.Status,
			DownloadedAt: d.DownloadedAt,
			Retries:      d.Retries,
			LastError:    d.LastError,
		}
		if err := s.db.UpsertDownload(ctx, dl); err != nil {
			return fmt.Errorf("migrate download %s: %w", d.URL, err)
		}
	}
	for _, e := range legacy.Extractions {
		// Do not overwrite an existing extraction.
		if _, err := s.db.GetExtractionByArchivePath(ctx, e.ArchivePath); err == nil {
			continue
		}
		ex := &db.Extraction{
			ArchivePath: e.ArchivePath,
			ExtractedTo: e.ExtractedTo,
			Files:       e.Files,
			Status:      e.Status,
			ExtractedAt: e.ExtractedAt,
			LastError:   e.LastError,
		}
		if err := s.db.UpsertExtraction(ctx, ex); err != nil {
			return fmt.Errorf("migrate extraction %s: %w", e.ArchivePath, err)
		}
	}

	// Rename legacy file so we don't migrate again.
	backup := path + ".backup"
	_ = os.Rename(path, backup)
	return nil
}

// legacyState mirrors the old JSON structure for migration.
type legacyState struct {
	Submissions map[string]legacySubmission `json:"submissions"`
	Downloads   map[string]legacyDownload   `json:"downloads"`
	Extractions map[string]legacyExtraction `json:"extractions"`
}

type legacySubmission struct {
	Key         string    `json:"key"`
	Dates       []string  `json:"dates"`
	SubmittedAt time.Time `json:"submitted_at"`
	Status      string    `json:"status"`
	Retries     int       `json:"retries"`
	LastError   string    `json:"last_error"`
}
type legacyDownload struct {
	URL          string    `json:"url"`
	FilePath     string    `json:"file_path"`
	SizeBytes    int64     `json:"size_bytes"`
	DownloadedAt time.Time `json:"downloaded_at"`
	Status       string    `json:"status"`
	Retries      int       `json:"retries"`
	LastError    string    `json:"last_error"`
}
type legacyExtraction struct {
	ArchivePath string    `json:"archive_path"`
	ExtractedTo string    `json:"extracted_to"`
	ExtractedAt time.Time `json:"extracted_at"`
	Files       []string  `json:"files"`
	Status      string    `json:"status"`
	LastError   string    `json:"last_error"`
}

func loadLegacyJSON(path string) (*legacyState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	st := &legacyState{}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("parse legacy state: %w", err)
	}
	return st, nil
}
