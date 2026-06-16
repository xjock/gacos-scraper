package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/xjock/gacos-scraper/internal/config"
	"github.com/xjock/gacos-scraper/internal/download"
	"github.com/xjock/gacos-scraper/internal/extract"
	"github.com/xjock/gacos-scraper/internal/gacos"
	"github.com/xjock/gacos-scraper/internal/imap"
	"github.com/xjock/gacos-scraper/internal/state"
	"github.com/xjock/gacos-scraper/internal/utils"
)

// Orchestrator coordinates submission, polling, downloading, and extraction.
type Orchestrator struct {
	cfg        *config.Config
	st         *state.State
	gacos      *gacos.Client
	downloader *download.Downloader
	imap       *imap.Client
}

// New creates an Orchestrator.
func New(cfg *config.Config, st *state.State) *Orchestrator {
	return &Orchestrator{
		cfg:        cfg,
		st:         st,
		gacos:      gacos.NewClient(gacos.DefaultBaseURL),
		downloader: download.NewDownloader(cfg.Polling.DownloadTimeout),
		imap: &imap.Client{
			Server:        cfg.IMAP.Server,
			Username:      cfg.IMAP.Username,
			Password:      cfg.IMAP.Password,
			UseTLS:        cfg.IMAP.UseTLS,
			SkipTLSVerify: cfg.IMAP.SkipTLSVerify,
			Mailbox:       cfg.IMAP.Mailbox,
			SenderFilter:  cfg.IMAP.SenderFilter,
			SubjectFilter: cfg.IMAP.SubjectFilter,
		},
	}
}

// RunOnce performs one full cycle: submit, poll, download, extract.
func (o *Orchestrator) RunOnce(ctx context.Context) error {
	if err := o.submitAll(ctx); err != nil {
		return err
	}
	return o.pollDownloadExtract(ctx)
}

// Run starts submission and then runs poll/download/extract in a loop until ctx is cancelled.
// Submission and polling run concurrently so that download emails for early chunks can be
// processed while later chunks are still being submitted.
func (o *Orchestrator) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	// Goroutine: continuously poll mailbox for download links.
	g.Go(func() error {
		for {
			if err := o.pollDownloadExtract(ctx); err != nil {
				slog.Error("poll/download/extract cycle failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(o.cfg.Polling.Interval):
			}
		}
	})

	// Main goroutine: submit all chunks.
	if err := o.submitAll(ctx); err != nil {
		return err
	}

	// After submission finishes, keep polling until context is cancelled.
	return g.Wait()
}

func (o *Orchestrator) submitAll(ctx context.Context) error {
	chunks := utils.ChunkDates(o.cfg.Gacos.Dates, 20)
	slog.Info("submitting GACOS requests", "chunks", len(chunks), "total_dates", len(o.cfg.Gacos.Dates))

	for i, chunk := range chunks {
		key := o.cfg.Gacos.SubmissionKey(chunk)
		sub, exists := o.st.GetSubmission(key)
		if exists && (sub.Status == "submitted" || sub.Status == "completed") {
			slog.Info("skipping already submitted chunk", "chunk", i+1, "key", key)
			continue
		}
		if exists && sub.Status == "failed" && sub.Retries >= o.cfg.Polling.MaxRetries {
			slog.Warn("chunk exceeded max retries, skipping", "chunk", i+1, "key", key)
			continue
		}

		req := gacos.Request{
			N:     o.cfg.Gacos.N(),
			W:     o.cfg.Gacos.W(),
			E:     o.cfg.Gacos.E(),
			S:     o.cfg.Gacos.S(),
			H:     o.cfg.Gacos.H(),
			M:     o.cfg.Gacos.M(),
			Dates: chunk,
			Type:  o.cfg.Gacos.TypeStr(),
			Email: o.cfg.Gacos.Email,
		}

		resp, err := o.submitWithRetry(ctx, req)
		if err != nil {
			slog.Error("failed to submit chunk", "chunk", i+1, "error", err)
			if recErr := o.st.RecordSubmission(key, &o.cfg.Gacos, chunk, "failed", resp, err.Error()); recErr != nil {
				slog.Error("failed to record submission failure", "error", recErr)
			}
			continue
		}

		if recErr := o.st.RecordSubmission(key, &o.cfg.Gacos, chunk, "submitted", resp, ""); recErr != nil {
			return fmt.Errorf("record submission: %w", recErr)
		}
		slog.Info("submitted chunk", "chunk", i+1, "dates", len(chunk))
	}
	return nil
}

func (o *Orchestrator) submitWithRetry(ctx context.Context, req gacos.Request) (string, error) {
	var lastErr error
	var lastResp string
	for attempt := 0; attempt <= o.cfg.Polling.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := utils.RetryBackoff(attempt-1, time.Second, 60*time.Second)
			slog.Warn("retrying GACOS submission", "attempt", attempt, "backoff", backoff)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}
		resp, err := o.gacos.Submit(ctx, req)
		lastResp = resp
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// If it's a clear client error, don't retry.
		if strings.Contains(err.Error(), "HTTP 4") {
			return resp, err
		}
	}
	return lastResp, fmt.Errorf("after %d retries: %w", o.cfg.Polling.MaxRetries, lastErr)
}

func (o *Orchestrator) pollDownloadExtract(ctx context.Context) error {
	// Poll from the oldest pending submission date, or last 7 days if none.
	since := time.Now().Add(-7 * 24 * time.Hour)
	pending, err := o.st.GetPendingSubmissions()
	if err != nil {
		return fmt.Errorf("get pending submissions: %w", err)
	}
	for _, sub := range pending {
		if !sub.SubmittedAt.IsZero() && sub.SubmittedAt.Before(since) {
			since = sub.SubmittedAt
		}
	}

	urls, urlToUID, err := o.imap.Poll(ctx, since)
	if err != nil {
		return fmt.Errorf("poll imap: %w", err)
	}
	if len(urls) == 0 {
		slog.Info("no new GACOS emails found")
		return nil
	}
	slog.Info("found download links", "count", len(urls))

	seenUIDs := make(map[uint32]struct{})
	for _, u := range urls {
		if o.st.IsDownloaded(u) {
			slog.Info("skipping already downloaded URL", "url", u)
			seenUIDs[urlToUID[u]] = struct{}{}
			continue
		}

		fileName := filenameFromURL(u)
		if fileName == "" {
			fileName = fmt.Sprintf("gacos_%d.tar.gz", time.Now().Unix())
		}
		destPath := filepath.Join(o.cfg.StagingDir(), fileName)

		size, err := o.downloader.Download(ctx, u, destPath)
		if err != nil {
			slog.Error("download failed", "url", u, "error", err)
			if recErr := o.st.RecordDownload("", u, destPath, 0, "failed", err.Error()); recErr != nil {
				slog.Error("failed to record download failure", "error", recErr)
			}
			continue
		}

		if recErr := o.st.RecordDownload("", u, destPath, size, "downloaded", ""); recErr != nil {
			return fmt.Errorf("record download: %w", recErr)
		}
		slog.Info("downloaded archive", "url", u, "size", size, "path", destPath)
		seenUIDs[urlToUID[u]] = struct{}{}

		if o.cfg.Output.Extract {
			files, err := o.extractArchive(destPath)
			if err != nil {
				slog.Error("extraction failed", "path", destPath, "error", err)
				if recErr := o.st.RecordExtraction(destPath, "", nil, "failed", err.Error()); recErr != nil {
					slog.Error("failed to record extraction failure", "error", recErr)
				}
				continue
			}
			// Try to link this download/extraction to the corresponding task by dates.
			if err := o.linkDownloadToTask(u, files); err != nil {
				slog.Warn("failed to link download to task", "url", u, "error", err)
			}
		} else {
			// If not extracting, still try to link by filename pattern in archive name.
			_ = o.linkDownloadToTask(u, []string{destPath})
		}
	}

	// Mark processed emails as seen.
	var uids []uint32
	for uid := range seenUIDs {
		uids = append(uids, uid)
	}
	if len(uids) > 0 {
		if err := o.imap.MarkSeen(ctx, uids...); err != nil {
			slog.Warn("failed to mark emails as seen", "error", err)
		}
	}

	return nil
}

func (o *Orchestrator) extractArchive(srcPath string) ([]string, error) {
	if o.st.IsExtracted(srcPath) {
		slog.Info("skipping already extracted archive", "path", srcPath)
		return nil, nil
	}

	baseName := strings.TrimSuffix(filepath.Base(srcPath), ".tar.gz")
	destDir := filepath.Join(o.cfg.ExtractDir(), baseName)

	files, err := extract.Extract(srcPath, destDir)
	if err != nil {
		return nil, err
	}

	if recErr := o.st.RecordExtraction(srcPath, destDir, files, "extracted", ""); recErr != nil {
		return nil, fmt.Errorf("record extraction: %w", recErr)
	}
	slog.Info("extracted archive", "path", srcPath, "files", len(files), "to", destDir)
	return files, nil
}

// dateFromFileRe extracts YYYYMMDD dates from filenames such as 20240101.ztd.tif.
var dateFromFileRe = regexp.MustCompile(`(\d{8})\.ztd`)

// linkDownloadToTask tries to associate a downloaded archive with the submission
// task that produced it by matching dates found in the extracted filenames.
func (o *Orchestrator) linkDownloadToTask(url string, files []string) error {
	dates := extractDatesFromFiles(files)
	if len(dates) == 0 {
		return nil
	}
	task, ok := o.st.FindTaskByDates(dates)
	if !ok {
		return fmt.Errorf("no task matches dates %v", dates)
	}
	if err := o.st.UpdateDownloadTaskID(url, task.ID); err != nil {
		return fmt.Errorf("update download task_id: %w", err)
	}
	if err := o.st.MarkTaskCompleted(task.ID); err != nil {
		return fmt.Errorf("mark task completed: %w", err)
	}
	slog.Info("linked download to task", "url", url, "task", task.ID)
	return nil
}

func extractDatesFromFiles(files []string) []string {
	seen := make(map[string]struct{})
	var dates []string
	for _, f := range files {
		matches := dateFromFileRe.FindStringSubmatch(filepath.Base(f))
		if len(matches) == 2 {
			d := matches[1]
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				dates = append(dates, d)
			}
		}
	}
	return dates
}

func filenameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base := filepath.Base(u.Path)
	base = strings.TrimSpace(base)
	if base == "" || base == "/" || base == "." {
		return ""
	}
	return base
}

// EnsureOutputDirs creates the output subdirectories.
func EnsureOutputDirs(cfg *config.Config) error {
	for _, d := range []string{cfg.StagingDir(), cfg.ExtractDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}
	return nil
}
