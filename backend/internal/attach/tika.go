package attach

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Tika extracts plain text from a document via an Apache Tika server.
type Tika struct {
	BaseURL string
	HTTP    *http.Client
}

// NewTika returns a Tika client with a sensible timeout.
func NewTika(baseURL string) Tika {
	return Tika{BaseURL: baseURL, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Extract PUTs the bytes to Tika and returns the extracted text. contentType may
// be empty to let Tika auto-detect (works for real documents by magic bytes).
func (t Tika) Extract(ctx context.Context, contentType string, data []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, t.BaseURL+"/tika", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("tika: request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("tika: do: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("tika: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tika: status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}
