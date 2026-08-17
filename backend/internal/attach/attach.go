package attach

import (
	"context"
	"fmt"
	"strings"
)

// Scan result classifications.
const (
	ScanClean    = "clean"
	ScanInfected = "infected"
	ScanError    = "error"
)

// Scanner malware-scans bytes (implemented by ClamAV).
type Scanner interface {
	Scan(ctx context.Context, data []byte) (ScanResult, error)
}

// Extractor extracts text from a document (implemented by Tika).
type Extractor interface {
	Extract(ctx context.Context, contentType string, data []byte) (string, error)
}

// Attachment is one inbound file to process.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Data        []byte `json:"data"`
}

// Result is the disposition of one attachment.
type Result struct {
	Filename      string    `json:"filename"`
	ScanResult    string    `json:"scan_result"` // clean | infected | error
	Signature     string    `json:"signature,omitempty"`
	Blocked       bool      `json:"blocked"`                  // infected OR masking unverifiable
	ExtractedText string    `json:"extracted_text,omitempty"` // masked; empty unless clean+verified
	PIISpans      []PIISpan `json:"pii_spans,omitempty"`
}

// Processor runs the pipeline for one attachment.
type Processor struct {
	Scanner   Scanner
	Extractor Extractor
}

// Process scans, then (only if clean) extracts and masks. Order is the security
// contract: never extract an unscanned or infected file (SEC-07). Returned errors
// are transient/infrastructure failures the caller fails closed on (route to
// human); an infected file or unverifiable masking is a Blocked Result, not an error.
func (p Processor) Process(ctx context.Context, a Attachment) (Result, error) {
	res := Result{Filename: a.Filename}

	scan, err := p.Scanner.Scan(ctx, a.Data)
	if err != nil {
		res.ScanResult = ScanError
		return res, fmt.Errorf("attach: scan: %w", err) // fail closed: do not extract
	}
	if !scan.Clean {
		res.ScanResult = ScanInfected
		res.Signature = scan.Signature
		res.Blocked = true
		return res, nil // blocked, never extracted
	}
	res.ScanResult = ScanClean

	text, err := p.Extractor.Extract(ctx, a.ContentType, a.Data)
	if err != nil {
		return res, fmt.Errorf("attach: extract: %w", err) // fail closed → human
	}

	masked, spans, err := MaskPII(text)
	if err != nil {
		// Masking could not be verified → block; never emit the raw text.
		res.Blocked = true
		return res, nil
	}
	res.ExtractedText = strings.TrimSpace(masked)
	res.PIISpans = spans
	return res, nil
}
