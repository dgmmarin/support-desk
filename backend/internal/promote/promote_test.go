package promote

import "testing"

// met is measured evidence that clears the CAL-02/03 minimums for a promotion above L1.
func met() Measured {
	return Measured{AuditedCases: 200, Correct: 200, Calibrated: true, Evaluable: true}
}

// TestEvaluate_FR_M6_10 pins the supervisor-gated, measured-criteria promotion
// contract (FR-M6-10, CAL-01/02/03): the product proposes, the human disposes, and
// every unmet gate is fail-closed toward less autonomy.
func TestEvaluate_FR_M6_10(t *testing.T) {
	cr := DefaultCriteria

	t.Run("blocked without supervisor attribution", func(t *testing.T) {
		d := Evaluate("", L1, L1+1, met(), cr)
		if d.Allowed {
			t.Fatalf("promotion without a supervisor must be blocked (FR-M6-10): %+v", d)
		}
	})

	t.Run("blocked when audited cases below CAL-03 minimum", func(t *testing.T) {
		m := met()
		m.AuditedCases, m.Correct = 199, 199 // <200
		d := Evaluate("sup-1", L1, L1+1, m, cr)
		if d.Allowed {
			t.Fatalf("promotion above L1 with <200 audited cases must be blocked (CAL-03): %+v", d)
		}
	})

	t.Run("blocked when precision below CAL-02 target", func(t *testing.T) {
		m := met()
		m.Correct = 190 // 190/200 = 0.95 < 0.98
		d := Evaluate("sup-1", L1, L1+1, m, cr)
		if d.Allowed {
			t.Fatalf("promotion above L1 below target precision must be blocked (CAL-02): %+v", d)
		}
	})

	t.Run("blocked when intent uncalibrated", func(t *testing.T) {
		m := met()
		m.Calibrated = false
		d := Evaluate("sup-1", L1, L1+1, m, cr)
		if d.Allowed {
			t.Fatalf("an uncalibrated intent may not be promoted above L1 (CAL-01): %+v", d)
		}
	})

	t.Run("blocked when criteria unevaluable (fail-closed)", func(t *testing.T) {
		m := met()
		m.Evaluable = false
		d := Evaluate("sup-1", L1, L1+1, m, cr)
		if d.Allowed {
			t.Fatalf("unevaluable measured criteria must block the promotion (fail-closed): %+v", d)
		}
	})

	t.Run("blocked on a multi-level jump", func(t *testing.T) {
		d := Evaluate("sup-1", L1, L1+2, met(), cr)
		if d.Allowed {
			t.Fatalf("promotion must be exactly one level up: %+v", d)
		}
	})

	t.Run("allowed above L1 with supervisor and all criteria met", func(t *testing.T) {
		d := Evaluate("sup-1", L1, L1+1, met(), cr)
		if !d.Allowed {
			t.Fatalf("supervisor + met criteria must allow the promotion: reasons=%v", d.Reasons)
		}
		if len(d.Reasons) != 0 {
			t.Fatalf("an allowed decision carries no reasons, got %v", d.Reasons)
		}
	})

	t.Run("L0 to L1 needs supervisor only (no audit floor before autonomy)", func(t *testing.T) {
		// Shadow→Assisted still human-approves every send, so CAL-03 does not gate it.
		none := Measured{Evaluable: true}
		if d := Evaluate("sup-1", L0, L1, none, cr); !d.Allowed {
			t.Fatalf("L0→L1 with a supervisor must be allowed without the audit floor: %v", d.Reasons)
		}
		if d := Evaluate("", L0, L1, none, cr); d.Allowed {
			t.Fatal("L0→L1 still requires a supervisor")
		}
	})
}
