package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Downloader downloads files over HTTP.
type Downloader struct {
	HTTP *http.Client
}

// NewDownloader creates a Downloader.
func NewDownloader(timeout time.Duration) *Downloader {
	return &Downloader{
		HTTP: &http.Client{Timeout: timeout},
	}
}

// Download fetches url and saves it to destPath atomically.
// If destPath already exists and has a size matching the server, the download is skipped.
func (d *Downloader) Download(ctx context.Context, url, destPath string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, fmt.Errorf("create download dir: %w", err)
	}

	// Check existing file size.
	var existingSize int64
	if info, err := os.Stat(destPath); err == nil {
		existingSize = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.0")
	if existingSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existingSize))
	}

	resp, err := d.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		// Server says we already have the whole file.
		return existingSize, nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("download %s returned HTTP %d", url, resp.StatusCode)
	}

	tmpPath := destPath + ".part"
	var out *os.File
	if existingSize > 0 && resp.StatusCode == http.StatusPartialContent {
		// Append to existing temp file.
		out, err = os.OpenFile(tmpPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			out, err = os.Create(tmpPath)
		}
	} else {
		out, err = os.Create(tmpPath)
	}
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}

	n, err := io.Copy(out, resp.Body)
	_ = out.Close()
	if err != nil {
		return 0, fmt.Errorf("write download body: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("rename downloaded file: %w", err)
	}

	finalSize := existingSize + n
	return finalSize, nil
}
