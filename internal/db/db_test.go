package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDBTaskCRUD(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	task := &Task{
		ID:     "task1",
		Dates:  []string{"20240101", "20240102"},
		North:  40.0, South: 39.0, West: 115.0, East: 116.5,
		Hour:   12, Minute: 0, Type: 2,
		Email:  "test@example.com",
		Status: "submitted",
	}
	if err := d.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("upsert task: %v", err)
	}

	got, err := d.GetTask(context.Background(), "task1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != "submitted" {
		t.Fatalf("unexpected status: %s", got.Status)
	}
	if len(got.Dates) != 2 {
		t.Fatalf("unexpected dates: %v", got.Dates)
	}
}

func TestDBSummary(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	ctx := context.Background()
	if err := d.UpsertTask(ctx, &Task{ID: "t1", Status: "submitted"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := d.UpsertTask(ctx, &Task{ID: "t2", Status: "failed"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := d.UpsertDownload(ctx, &Download{URL: "http://a.tar.gz", Status: "pending"}); err != nil {
		t.Fatalf("upsert download: %v", err)
	}

	sum, err := d.GetSummary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.SubmissionsTotal != 2 {
		t.Fatalf("expected 2 submissions, got %d", sum.SubmissionsTotal)
	}
	if sum.DownloadsPending != 1 {
		t.Fatalf("expected 1 pending download, got %d", sum.DownloadsPending)
	}
}
