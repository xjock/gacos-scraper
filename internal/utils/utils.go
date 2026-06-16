package utils

import (
	"crypto/sha256"
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ChunkDates splits a date list into chunks of at most chunkSize.
// It preserves order and returns empty if dates is empty.
func ChunkDates(dates []string, chunkSize int) [][]string {
	if chunkSize <= 0 {
		chunkSize = 20
	}
	var chunks [][]string
	for i := 0; i < len(dates); i += chunkSize {
		end := i + chunkSize
		if end > len(dates) {
			end = len(dates)
		}
		chunks = append(chunks, dates[i:end])
	}
	return chunks
}

// YYYYMMDD regex.
var yyyymmddRe = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})$`)

// ValidateYYYYMMDD checks that s is exactly YYYYMMDD and is a real calendar date.
func ValidateYYYYMMDD(s string) error {
	if !yyyymmddRe.MatchString(s) {
		return fmt.Errorf("date %q is not in YYYYMMDD format", s)
	}
	m := yyyymmddRe.FindStringSubmatch(s)
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return fmt.Errorf("date %q is not a valid calendar date", s)
	}
	return nil
}

// tarGzURLRe matches HTTP(S) URLs ending in .tar.gz (case-insensitive).
var tarGzURLRe = regexp.MustCompile(`(?i)https?://[^\s"<>\]\[(){}]+\.tar\.gz`)

// ExtractTargzURLs finds all tar.gz download URLs in a text.
func ExtractTargzURLs(text string) []string {
	found := tarGzURLRe.FindAllString(text, -1)
	if found == nil {
		return nil
	}
	// Deduplicate while preserving order.
	seen := make(map[string]struct{}, len(found))
	uniq := make([]string, 0, len(found))
	for _, u := range found {
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		uniq = append(uniq, u)
	}
	return uniq
}

// ValidateEmail checks that addr looks like an email address.
func ValidateEmail(addr string) error {
	_, err := mail.ParseAddress(addr)
	return err
}

// SubmissionKey returns a deterministic idempotency key for a GACOS submission.
func SubmissionKey(n, w, e, s float64, h, m, typ int, email string, dates []string) string {
	sorted := make([]string, len(dates))
	copy(sorted, dates)
	sort.Strings(sorted)
	raw := fmt.Sprintf("%.6f|%.6f|%.6f|%.6f|%d|%d|%d|%s|%s",
		n, w, e, s, h, m, typ, strings.ToLower(email), strings.Join(sorted, ","))
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum)
}

// RetryBackoff returns a sleep duration for the given attempt (0-based).
// It doubles each time up to max.
func RetryBackoff(attempt int, base, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := base * time.Duration(1<<attempt)
	if d > max || d <= 0 {
		d = max
	}
	return d
}

// NormalizeDates trims whitespace and removes empty entries.
func NormalizeDates(dates []string) []string {
	out := make([]string, 0, len(dates))
	for _, d := range dates {
		d = strings.TrimSpace(d)
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}
