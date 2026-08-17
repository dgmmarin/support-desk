package understand

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file is the deterministic entity extraction for stage 3 (FR-M3-04). The
// model (Classifier) proposes raw entity *hints* per unit; every hint is untrusted
// data (ADR-0016 / MOD-07). Pure code here validates and normalizes each hint at
// the boundary — dates → ISO-8601, amounts → ISO-4217 currency + integer minor
// units, refs/flight numbers → their token shape. A hint that does not match its
// field's shape is dropped (empty), never fabricated: instruction text ("ignore
// previous instructions") lands as data and is rejected, never obeyed.

// DateRange is a normalized ISO-8601 travel window; End is empty for a single date.
type DateRange struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

// Pax is passenger composition (FR-M3-04): counts plus recorded child ages.
type Pax struct {
	Adults    int   `json:"adults,omitempty"`
	Children  int   `json:"children,omitempty"`
	ChildAges []int `json:"child_ages,omitempty"`
}

// Amount is a monetary value normalized to ISO-4217 currency + integer minor units
// (e.g. cents), so no float rounding leaks downstream.
type Amount struct {
	Currency   string `json:"currency"`
	ValueMinor int64  `json:"value_minor"`
}

// Entities is the structured entity set on the Understanding (spec §3). Absent
// entities are zero-valued — never guessed (FR-M3-04 fail-closed).
type Entities struct {
	Ref         string    `json:"ref,omitempty"`
	Destination string    `json:"destination,omitempty"`
	Hotel       string    `json:"hotel,omitempty"`
	Dates       DateRange `json:"dates,omitempty"`
	Pax         Pax       `json:"pax,omitempty"`
	FlightNo    string    `json:"flight_no,omitempty"`
	Product     string    `json:"product,omitempty"`
	Amounts     []Amount  `json:"amounts,omitempty"`
}

// ExtractEntities merges the model's per-unit hints into one normalized Entities.
// Merge is order-independent: units are already an ordered slice, and each unit's
// hint keys are iterated in sorted order — so extraction is replay-deterministic
// (no reliance on Go map iteration order). First non-empty normalized value wins
// per single-valued field; amounts accumulate.
func ExtractEntities(units []ClassifiedUnit) Entities {
	var e Entities
	for _, u := range units {
		keys := make([]string, 0, len(u.Entities))
		for k := range u.Entities {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := u.Entities[k]
			switch canonKey(k) {
			case "ref":
				if e.Ref == "" {
					e.Ref = normalizeRef(v)
				}
			case "destination":
				if e.Destination == "" {
					e.Destination = freeText(v)
				}
			case "hotel":
				if e.Hotel == "" {
					e.Hotel = freeText(v)
				}
			case "dates":
				if e.Dates == (DateRange{}) {
					e.Dates = normalizeDateRange(v)
				}
			case "pax":
				if e.Pax.empty() {
					e.Pax = normalizePax(v)
				}
			case "flight_no":
				if e.FlightNo == "" {
					e.FlightNo = normalizeFlightNo(v)
				}
			case "product":
				if e.Product == "" {
					e.Product = freeText(v)
				}
			case "amount":
				if a, ok := normalizeAmount(v); ok {
					e.Amounts = append(e.Amounts, a)
				}
			}
		}
	}
	return e
}

func (p Pax) empty() bool { return p.Adults == 0 && p.Children == 0 && len(p.ChildAges) == 0 }

// canonKey folds the model's hint-key variants onto the canonical field names.
func canonKey(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "ref", "booking_ref", "booking_reference", "reference":
		return "ref"
	case "destination", "city", "resort":
		return "destination"
	case "hotel", "accommodation":
		return "hotel"
	case "dates", "date", "travel_dates":
		return "dates"
	case "pax", "passengers", "party", "composition":
		return "pax"
	case "flight_no", "flight", "flight_number":
		return "flight_no"
	case "product", "package", "product_name":
		return "product"
	case "amount", "amounts", "price", "money", "total":
		return "amount"
	}
	return ""
}

var refPattern = regexp.MustCompile(`\b[A-Z]{2,4}-?\d{3,8}\b`)

// normalizeRef validates a booking-reference token (letters+digits). Instruction
// prose has no such token and yields "" (rejected as data, FR-M3-04 fail-closed).
func normalizeRef(v string) string {
	return refPattern.FindString(strings.ToUpper(v))
}

var flightPattern = regexp.MustCompile(`\b[A-Z]{2,3}\d{1,4}[A-Z]?\b`)

// normalizeFlightNo validates an IATA-style flight designator; else "".
func normalizeFlightNo(v string) string {
	return flightPattern.FindString(strings.ToUpper(v))
}

// dateLayouts are the accepted input forms. ponytail: EU/EN forms only —
// day-before-month for numeric dates. Ceiling: no free-form NLP dates; upgrade
// path = a date-parsing lib or model-side ISO normalization.
var dateLayouts = []string{
	"2006-01-02",
	"02/01/2006", "2/1/2006",
	"02.01.2006", "2.1.2006",
	"2 January 2006", "02 January 2006",
	"January 2, 2006", "Jan 2, 2006", "2 Jan 2006",
}

// dateSplit separates a range; the dash form requires surrounding whitespace so it
// never splits the hyphens inside an ISO date ("2026-07-03").
var dateSplit = regexp.MustCompile(`(?i)\s+to\s+|\s+until\s+|\s+[-–—]\s+`)

// normalizeDateRange parses one or two dates from a hint into ISO-8601. A range is
// split on "to"/"until"/dash; unparseable text yields an empty range (not guessed).
func normalizeDateRange(v string) DateRange {
	parts := dateSplit.Split(strings.TrimSpace(v), -1)
	var iso []string
	for _, p := range parts {
		if d := parseDate(strings.TrimSpace(p)); d != "" {
			iso = append(iso, d)
		}
	}
	switch len(iso) {
	case 0:
		return DateRange{}
	case 1:
		return DateRange{Start: iso[0]}
	default:
		return DateRange{Start: iso[0], End: iso[1]}
	}
}

func parseDate(s string) string {
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

var (
	adultRe  = regexp.MustCompile(`(?i)(\d+)\s*adults?`)
	childRe  = regexp.MustCompile(`(?i)(\d+)\s*(?:child(?:ren)?|kids?)`)
	peopleRe = regexp.MustCompile(`(?i)(\d+)\s*(?:people|persons?|passengers?|travell?ers?|guests?|pax)`)
	agesRe   = regexp.MustCompile(`(?i)ages?\s*([\d,\sand]+)`)
	digitsRe = regexp.MustCompile(`\d+`)
)

// normalizePax parses passenger composition. A bare count with no adult/child word
// (e.g. "4 people") is treated as adults. Non-pax text yields an empty Pax.
func normalizePax(v string) Pax {
	var p Pax
	if m := adultRe.FindStringSubmatch(v); m != nil {
		p.Adults = atoi(m[1])
	}
	if m := childRe.FindStringSubmatch(v); m != nil {
		p.Children = atoi(m[1])
	}
	if m := agesRe.FindStringSubmatch(v); m != nil {
		for _, d := range digitsRe.FindAllString(m[1], -1) {
			p.ChildAges = append(p.ChildAges, atoi(d))
		}
	}
	if p.Adults == 0 && p.Children == 0 {
		if m := peopleRe.FindStringSubmatch(v); m != nil {
			p.Adults = atoi(m[1])
		}
	}
	return p
}

var currencyMap = map[string]string{
	"€": "EUR", "eur": "EUR", "euro": "EUR", "euros": "EUR",
	"$": "USD", "usd": "USD", "dollar": "USD", "dollars": "USD",
	"£": "GBP", "gbp": "GBP", "pound": "GBP", "pounds": "GBP", "sterling": "GBP",
	"dkk": "DKK", "kroner": "DKK", "kr": "DKK",
	"sek": "SEK", "nok": "NOK",
}

var (
	currencyRe = regexp.MustCompile(`(?i)€|£|\$|\beur\b|\busd\b|\bgbp\b|\bdkk\b|\bsek\b|\bnok\b|euros?|dollars?|pounds?|sterling|kroner|\bkr\b`)
	numberRe   = regexp.MustCompile(`\d[\d.,]*\d|\d`)
	euDecimal  = regexp.MustCompile(`^\d{1,3}(\.\d{3})*,\d{2}$|^\d+,\d{2}$`)
)

// normalizeAmount parses "€200"/"200 EUR"/"$1,200.50"/"200,50 DKK" into an
// ISO-4217 currency + integer minor units. A hint with no recognizable currency is
// dropped (ok=false) — a value is never given a fabricated currency (FR-M3-04).
func normalizeAmount(v string) (Amount, bool) {
	cur := currencyRe.FindString(strings.ToLower(v))
	if cur == "" {
		return Amount{}, false
	}
	code, ok := currencyMap[strings.TrimSpace(cur)]
	if !ok {
		return Amount{}, false
	}
	num := numberRe.FindString(v)
	if num == "" {
		return Amount{}, false
	}
	minor, ok := parseMoney(num)
	if !ok {
		return Amount{}, false
	}
	return Amount{Currency: code, ValueMinor: minor}, true
}

// parseMoney converts a numeric money token to integer minor units. It recognizes
// the EU grouped/decimal form ("1.234,56") and the EN form ("1,234.56"/"1234.5").
// ponytail: two-decimal minor units only; upgrade path = per-currency exponents.
func parseMoney(num string) (int64, bool) {
	num = strings.TrimSpace(num)
	if euDecimal.MatchString(num) {
		num = strings.ReplaceAll(num, ".", "")
		num = strings.ReplaceAll(num, ",", ".")
	} else {
		num = strings.ReplaceAll(num, ",", "")
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	// Round to the nearest minor unit to avoid binary float drift.
	return int64(f*100 + 0.5), true
}

var wsRe = regexp.MustCompile(`\s+`)

// freeText keeps a free-text entity (destination/hotel/product) as inert data:
// trimmed, whitespace-collapsed, control chars stripped, length-capped. The string
// is stored, never interpreted as an instruction (ADR-0016 / MOD-07).
func freeText(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, v)
	v = strings.TrimSpace(wsRe.ReplaceAllString(v, " "))
	if len(v) > 200 {
		v = strings.TrimSpace(v[:200])
	}
	return v
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
