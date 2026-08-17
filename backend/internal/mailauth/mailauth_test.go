package mailauth

import "testing"

// test_FR_M1_08_dmarc_pass_parsed
func TestFRM108DmarcPassParsed(t *testing.T) {
	h := "mx.op.com; spf=pass smtp.mailfrom=cust@x.com; dkim=pass header.d=x.com; dmarc=pass (p=none) header.from=x.com"
	r := Parse(h)
	if r.SPF != "pass" || r.DKIM != "pass" || r.DMARC != "pass" {
		t.Fatalf("parsed = %+v, want all pass", r)
	}
	if !r.DMARCPass {
		t.Fatal("DMARCPass should be true when dmarc=pass")
	}
}

// test_FR_M1_08_dmarc_fail_or_absent_is_not_pass (fail-closed)
func TestFRM108DmarcFailOrAbsentIsNotPass(t *testing.T) {
	fail := Parse("mx.op.com; spf=softfail; dkim=fail; dmarc=fail header.from=x.com")
	if fail.DMARCPass {
		t.Fatal("dmarc=fail must not be pass")
	}

	absent := Parse("")
	if absent.DMARCPass {
		t.Fatal("absent header must not be pass (fail-closed)")
	}
	if absent.DMARC != "" && absent.DMARC != "none" {
		t.Fatalf("absent dmarc = %q, want empty/none", absent.DMARC)
	}

	garbled := Parse("this is not an auth results header")
	if garbled.DMARCPass {
		t.Fatal("garbled header must not be pass")
	}
}

// Case-insensitivity and whitespace tolerance.
func TestParseIsCaseInsensitive(t *testing.T) {
	r := Parse("mx; SPF=Pass; DKIM=PASS; DMARC=Pass header.from=x")
	if !r.DMARCPass {
		t.Fatalf("case-insensitive dmarc=Pass should be pass: %+v", r)
	}
}
