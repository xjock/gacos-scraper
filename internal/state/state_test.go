package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xjock/gacos-scraper/internal/config"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	defer s.Close()

	cfg := &config.GacosConfig{
		North: 40.0, South: 39.0, West: 115.0, East: 116.5,
		Hour: 12, Minute: 0, Type: 2, Email: "test@example.com",
	}
	if err := s.RecordSubmission("key1", cfg, []string{"20240101"}, "submitted", "response1", ""); err != nil {
		t.Fatalf("record submission: %v", err)
	}
	if err := s.RecordDownload("", "http://example.com/a.tar.gz", "/tmp/a.tar.gz", 1234, "downloaded", ""); err != nil {
		t.Fatalf("record download: %v", err)
	}

	// Reload.
	s2, err := Load(path)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	defer s2.Close()

	task, ok := s2.GetSubmission("key1")
	if !ok {
		t.Fatalf("expected submission key1")
	}
	if task.Status != "submitted" {
		t.Fatalf("unexpected status: %s", task.Status)
	}
	if !s2.IsDownloaded("http://example.com/a.tar.gz") {
		t.Fatalf("expected URL downloaded")
	}
}

func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.db")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("expected nil error for missing db, got %v", err)
	}
	if s == nil {
		t.Fatalf("expected non-nil state")
	}
	s.Close()
}

func TestSummary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	defer s.Close()

	cfg := &config.GacosConfig{
		North: 40.0, South: 39.0, West: 115.0, East: 116.5,
		Hour: 12, Minute: 0, Type: 2, Email: "test@example.com",
	}
	if err := s.RecordSubmission("k1", cfg, []string{"20240101"}, "submitted", "", ""); err != nil {
		t.Fatalf("record submission: %v", err)
	}
	if err := s.RecordSubmission("k2", cfg, []string{"20240102"}, "failed", "", "boom"); err != nil {
		t.Fatalf("record submission: %v", err)
	}

	sum := s.GetSummary()
	if sum.SubmissionsTotal != 2 {
		t.Fatalf("expected 2 submissions, got %d", sum.SubmissionsTotal)
	}
	if sum.SubmissionsSubmitted != 1 {
		t.Fatalf("expected 1 submitted submission, got %d", sum.SubmissionsSubmitted)
	}
	if sum.SubmissionsFailed != 1 {
		t.Fatalf("expected 1 failed submission, got %d", sum.SubmissionsFailed)
	}
}

func TestLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "state.json")
	dbPath := filepath.Join(dir, "state.db")

	legacy := []byte(`{
		"submissions": {
			"abc123": {
				"key": "abc123",
				"dates": ["20240101"],
				"submitted_at": "2024-01-01T00:00:00Z",
				"status": "submitted",
				"retries": 0,
				"last_error": ""
			}
		},
		"downloads": {},
		"extractions": {}
	}`)
	if err := writeFile(jsonPath, legacy); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	s, err := Load(dbPath)
	if err != nil {
		t.Fatalf("load with migration: %v", err)
	}
	defer s.Close()

	task, ok := s.GetSubmission("abc123")
	if !ok {
		t.Fatalf("expected migrated submission")
	}
	if task.Status != "submitted" {
		t.Fatalf("unexpected status: %s", task.Status)
	}
}

func writeFile(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	_ = f.Close()
	return err
}
