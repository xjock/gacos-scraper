package utils

import (
	"testing"
	"time"
)

func TestChunkDates(t *testing.T) {
	dates := []string{"20240101", "20240102", "20240103", "20240104", "20240105"}
	chunks := ChunkDates(dates, 2)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 2 || len(chunks[1]) != 2 || len(chunks[2]) != 1 {
		t.Fatalf("unexpected chunk sizes: %v", chunks)
	}
	if chunks[0][0] != "20240101" || chunks[2][0] != "20240105" {
		t.Fatalf("unexpected order: %v", chunks)
	}
}

func TestValidateYYYYMMDD(t *testing.T) {
	good := []string{"20240101", "20200229", "20231231"}
	for _, d := range good {
		if err := ValidateYYYYMMDD(d); err != nil {
			t.Errorf("expected %s valid, got %v", d, err)
		}
	}
	bad := []string{"20241301", "20240230", "2024-01-01", "240101"}
	for _, d := range bad {
		if err := ValidateYYYYMMDD(d); err == nil {
			t.Errorf("expected %s invalid", d)
		}
	}
}

func TestExtractTargzURLs(t *testing.T) {
	text := `Hello,
Download your file from http://www.gacos.net/M/result/20240101.tar.gz
or https://example.com/data/20240102.tar.gz
and also http://example.com/not-a-tar link.`
	urls := ExtractTargzURLs(text)
	if len(urls) != 2 {
		t.Fatalf("expected 2 urls, got %d: %v", len(urls), urls)
	}
	if urls[0] != "http://www.gacos.net/M/result/20240101.tar.gz" {
		t.Errorf("unexpected first url: %s", urls[0])
	}
}

func TestRetryBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{10, 60 * time.Second},
	}
	for _, c := range cases {
		got := RetryBackoff(c.attempt, time.Second, 60*time.Second)
		if got != c.want {
			t.Errorf("attempt %d: got %v, want %v", c.attempt, got, c.want)
		}
	}
}

func TestSubmissionKey(t *testing.T) {
	k1 := SubmissionKey(40.0, 115.0, 116.5, 39.0, 12, 0, 2, "a@b.com", []string{"20240101", "20240102"})
	k2 := SubmissionKey(40.0, 115.0, 116.5, 39.0, 12, 0, 2, "a@b.com", []string{"20240102", "20240101"})
	if k1 != k2 {
		t.Errorf("expected same key for reordered dates, got %s vs %s", k1, k2)
	}
	k3 := SubmissionKey(40.0, 115.0, 116.5, 39.0, 12, 0, 2, "A@B.COM", []string{"20240101", "20240102"})
	if k1 != k3 {
		t.Errorf("expected case-insensitive email, got %s vs %s", k1, k3)
	}
}
