//go:build integration

package attach

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// The EICAR anti-malware test string (harmless; every scanner flags it).
var eicar = []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)

// test_SEC_07_clamav_flags_eicar
func TestSEC07ClamAVFlagsEicar(t *testing.T) {
	addr := os.Getenv("CLAMAV_ADDR")
	if addr == "" {
		t.Skip("CLAMAV_ADDR not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := ClamAV{Addr: addr}

	infected, err := c.Scan(ctx, eicar)
	if err != nil {
		t.Fatalf("scan eicar: %v", err)
	}
	if infected.Clean {
		t.Fatal("EICAR must be flagged as infected (SEC-07)")
	}
	if infected.Signature == "" {
		t.Fatal("expected a signature name for EICAR")
	}

	clean, err := c.Scan(ctx, []byte("just a harmless note"))
	if err != nil {
		t.Fatalf("scan clean: %v", err)
	}
	if !clean.Clean {
		t.Fatalf("harmless payload flagged: %+v", clean)
	}
}

// test_tika_extracts_text
func TestTikaExtractsText(t *testing.T) {
	base := os.Getenv("TIKA_URL")
	if base == "" {
		t.Skip("TIKA_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	text, err := NewTika(base).Extract(ctx, "text/plain", []byte("hello from tika"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "hello from tika") {
		t.Fatalf("unexpected extraction: %q", text)
	}
}
