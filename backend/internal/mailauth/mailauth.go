// Package mailauth parses the inbound Authentication-Results header (RFC 8601)
// stamped by the receiving MTA into SPF/DKIM/DMARC verdicts. It never computes
// auth itself (DNS + crypto is the MTA's job) and treats a missing/garbled header
// as failure — auth is never passed by omission (FR-M1-08, fail-closed).
//
// The header text is parsed as data, never executed (ADR-0016).
package mailauth

import (
	"regexp"
	"strings"
)

// Result holds the parsed inbound authentication verdicts.
type Result struct {
	SPF       string // "pass" | "fail" | "softfail" | "none" | ""
	DKIM      string
	DMARC     string
	DMARCPass bool // true only when DMARC explicitly passed
}

// methodRe matches `method=result`, e.g. `dmarc=pass`, tolerating surrounding
// whitespace and case.
var methodRe = regexp.MustCompile(`(?i)\b(spf|dkim|dmarc)\s*=\s*([a-z]+)`)

// Parse extracts SPF/DKIM/DMARC results from an Authentication-Results header
// value (the part after the header name). Unknown/absent → zero values; DMARCPass
// is true only for an explicit dmarc=pass.
func Parse(header string) Result {
	var r Result
	for _, m := range methodRe.FindAllStringSubmatch(header, -1) {
		method := strings.ToLower(m[1])
		result := strings.ToLower(m[2])
		switch method {
		case "spf":
			r.SPF = result
		case "dkim":
			r.DKIM = result
		case "dmarc":
			r.DMARC = result
		}
	}
	r.DMARCPass = r.DMARC == "pass"
	return r
}
