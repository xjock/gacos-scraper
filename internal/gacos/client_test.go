package gacos

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSubmit(t *testing.T) {
	var received url.Values
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		var err error
		received, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse query: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SubmitPath = "/M/action_page.php"
	respBody, err := c.Submit(context.Background(), Request{
		N:     "40.000000",
		W:     "115.000000",
		E:     "116.500000",
		S:     "39.000000",
		H:     "12",
		M:     "0",
		Dates: []string{"20240101", "20240102"},
		Type:  "2",
		Email: "test@example.com",
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if respBody != "success" {
		t.Fatalf("unexpected response body: %s", respBody)
	}

	if path != "/M/action_page.php" {
		t.Fatalf("unexpected path: %s", path)
	}
	if got := received.Get("N"); got != "40.000000" {
		t.Fatalf("unexpected N: %s", got)
	}
	if got := received.Get("date"); got != "20240101\n20240102" {
		t.Fatalf("unexpected date block: %q", got)
	}
	if got := received.Get("type"); got != "2" {
		t.Fatalf("unexpected type: %s", got)
	}
}

func TestSubmitTooManyDates(t *testing.T) {
	c := NewClient("")
	dates := make([]string, 21)
	for i := range dates {
		dates[i] = "20240101"
	}
	respBody, err := c.Submit(context.Background(), Request{Dates: dates})
	if err == nil || !strings.Contains(err.Error(), "too many dates") {
		t.Fatalf("expected too many dates error, got %v", err)
	}
	_ = respBody
}
