package attach

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// clamavChunk bounds each INSTREAM chunk (ClamAV's StreamMaxLength governs the
// total; 64 KiB chunks are well within any default).
const clamavChunk = 64 * 1024

// ClamAV scans bytes over the clamd INSTREAM protocol.
type ClamAV struct {
	Addr    string        // host:port of clamd
	Timeout time.Duration // per-scan deadline (default 30s)
}

// ScanResult is the verdict for one payload.
type ScanResult struct {
	Clean     bool
	Signature string // set when a threat is found
}

// Scan streams data to clamd and returns the verdict. A protocol/connection
// error is returned as err (the caller fails closed and does not extract).
func (c ClamAV) Scan(ctx context.Context, data []byte) (ScanResult, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return ScanResult{}, fmt.Errorf("clamav: dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return ScanResult{}, fmt.Errorf("clamav: write cmd: %w", err)
	}
	var lenbuf [4]byte
	for off := 0; off < len(data); off += clamavChunk {
		end := min(off+clamavChunk, len(data))
		binary.BigEndian.PutUint32(lenbuf[:], uint32(end-off))
		if _, err := conn.Write(lenbuf[:]); err != nil {
			return ScanResult{}, fmt.Errorf("clamav: write len: %w", err)
		}
		if _, err := conn.Write(data[off:end]); err != nil {
			return ScanResult{}, fmt.Errorf("clamav: write chunk: %w", err)
		}
	}
	// Zero-length chunk terminates the stream.
	binary.BigEndian.PutUint32(lenbuf[:], 0)
	if _, err := conn.Write(lenbuf[:]); err != nil {
		return ScanResult{}, fmt.Errorf("clamav: write terminator: %w", err)
	}

	respBytes, err := io.ReadAll(conn)
	if err != nil {
		return ScanResult{}, fmt.Errorf("clamav: read: %w", err)
	}
	resp := strings.TrimRight(string(respBytes), "\x00\n ")

	switch {
	case strings.HasSuffix(resp, "OK"):
		return ScanResult{Clean: true}, nil
	case strings.HasSuffix(resp, "FOUND"):
		// Format: "stream: <Signature> FOUND"
		sig := strings.TrimSpace(strings.TrimPrefix(resp, "stream:"))
		sig = strings.TrimSpace(strings.TrimSuffix(sig, "FOUND"))
		return ScanResult{Clean: false, Signature: sig}, nil
	default:
		return ScanResult{}, fmt.Errorf("clamav: unexpected response %q", resp)
	}
}
