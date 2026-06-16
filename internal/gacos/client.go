package gacos

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Default endpoints.
const (
	DefaultBaseURL   = "http://www.gacos.net"
	DefaultSubmitPath = "/M/action_page.php"
)

// Client submits requests to the GACOS website.
type Client struct {
	BaseURL    string
	SubmitPath string
	HTTP       *http.Client
}

// Request is a single submission payload.
type Request struct {
	N      string
	W      string
	E      string
	S      string
	H      string
	M      string
	Dates  []string
	Type   string
	Email  string
}

// NewClient creates a GACOS client with sensible defaults.
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		SubmitPath: DefaultSubmitPath,
		HTTP: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Submit sends the form POST to GACOS and returns the response body snippet.
func (c *Client) Submit(ctx context.Context, req Request) (string, error) {
	if len(req.Dates) == 0 {
		return "", fmt.Errorf("no dates in request")
	}
	if len(req.Dates) > 20 {
		return "", fmt.Errorf("too many dates in request: %d (max 20)", len(req.Dates))
	}

	form := url.Values{}
	form.Set("N", req.N)
	form.Set("W", req.W)
	form.Set("E", req.E)
	form.Set("S", req.S)
	form.Set("H", req.H)
	form.Set("M", req.M)
	form.Set("date", strings.Join(req.Dates, "\n"))
	form.Set("type", req.Type)
	form.Set("email", req.Email)

	postURL := c.BaseURL + c.SubmitPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.0")
	httpReq.Header.Set("Referer", c.BaseURL+"/")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("post to %s: %w", postURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	bodyStr := string(body)
	if resp.StatusCode >= 400 {
		return bodyStr, fmt.Errorf("gacos returned HTTP %d: %s", resp.StatusCode, truncate(bodyStr, 200))
	}

	return truncate(bodyStr, 5000), nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
