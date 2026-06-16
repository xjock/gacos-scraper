package extract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtract(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "test.tar.gz")
	if err := createTestArchive(archive); err != nil {
		t.Fatalf("create archive: %v", err)
	}

	dest := filepath.Join(dir, "out")
	files, err := Extract(archive, dest)
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(content) != "hello gacos" {
		t.Fatalf("unexpected content: %s", string(content))
	}
}

func createTestArchive(path string) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	data := []byte("hello gacos")
	hdr := &tar.Header{
		Name: "hello.txt",
		Mode: 0o644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
