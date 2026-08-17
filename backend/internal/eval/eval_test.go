package eval_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"tourdesk/internal/eval"
)

// test_FR_M8_06_regression_gate — the frozen-set regression gate (ADR-0013, M8 §7).
// Mirrors the spec's runnable-check `regression_gate_blocks_drop`: an accuracy drop
// blocks; all-axes-≥-baseline passes; a safety drop blocks even with accuracy up
// (safety is not tradeable); an unevaluable report blocks (fail-closed).
func TestFR_M8_06_RegressionGate(t *testing.T) {
	base := eval.Report{Accuracy: 0.94, Groundedness: 0.90, Safety: 0.99, Evaluable: true}

	// 1. accuracy drop → blocked.
	if d := eval.RegressionGate(eval.Report{Accuracy: 0.90, Groundedness: 0.90, Safety: 0.99, Evaluable: true}, base); d.Pass {
		t.Fatal("accuracy 0.90 < baseline 0.94 must block rollout (FR-M8-06)")
	}

	// 2. all axes ≥ baseline → pass.
	if d := eval.RegressionGate(eval.Report{Accuracy: 0.95, Groundedness: 0.90, Safety: 0.99, Evaluable: true}, base); !d.Pass {
		t.Fatalf("all axes ≥ baseline must pass, got %+v", d)
	}

	// 3. safety drop with accuracy improved → blocked (safety not tradeable).
	if d := eval.RegressionGate(eval.Report{Accuracy: 0.98, Groundedness: 0.90, Safety: 0.98, Evaluable: true}, base); d.Pass {
		t.Fatal("a safety drop must block even with accuracy up (FR-M8-06, safety not tradeable)")
	}

	// 4. unevaluable candidate → blocked (fail-closed).
	if d := eval.RegressionGate(eval.Report{Accuracy: 1, Groundedness: 1, Safety: 1, Evaluable: false}, base); d.Pass {
		t.Fatal("an unevaluable candidate must block rollout (FR-M8-06 guardrail)")
	}
	// 5. unevaluable baseline (no pinned target) → blocked.
	if d := eval.RegressionGate(eval.Report{Accuracy: 1, Groundedness: 1, Safety: 1, Evaluable: true}, eval.Report{Evaluable: false}); d.Pass {
		t.Fatal("a missing/unevaluable baseline must block rollout (FR-M8-06 guardrail)")
	}
}

// test_FR_M8_05_score — Score is deterministic over the frozen cases; an unscored
// case makes the whole report unevaluable (the gate then blocks). Empty set → also
// unevaluable.
func TestFR_M8_05_Score(t *testing.T) {
	cases := []eval.Case{
		{CaseID: "c1", Intent: "faq", Input: "opening hours?", Expected: "We open at 9am."},
		{CaseID: "c2", Intent: "faq", Input: "wifi?", Expected: "Yes, free wifi."},
	}
	answers := map[string]eval.Answer{
		"c1": {Text: "we open at 9am.", Grounded: true, Safe: true}, // normalised match
		"c2": {Text: "No.", Grounded: false, Safe: true},            // wrong + ungrounded
	}
	r := eval.Score(cases, answers)
	if !r.Evaluable {
		t.Fatal("a fully-answered set must be evaluable")
	}
	if r.Accuracy != 0.5 || r.Groundedness != 0.5 || r.Safety != 1.0 {
		t.Fatalf("Score = %+v, want acc 0.5 / ground 0.5 / safety 1.0", r)
	}
	// determinism: same inputs, same result.
	if eval.Score(cases, answers) != r {
		t.Fatal("Score must be deterministic (NFR-R-04)")
	}
	// an unscored case → unevaluable (the gate blocks).
	if eval.Score(cases, map[string]eval.Answer{"c1": answers["c1"]}).Evaluable {
		t.Fatal("a case without an answer must make the report unevaluable")
	}
	if eval.Score(nil, answers).Evaluable {
		t.Fatal("an empty eval set must be unevaluable")
	}
}

// test_FR_M8_05_no_eval_set_caps_level_at_L1 — the eval-set-missing guardrail caps
// an intent at L1 (ties CAL-03). Present → no-op.
func TestFR_M8_05_CapLevelForEvalSet(t *testing.T) {
	if got := eval.CapLevelForEvalSet(false, 4); got != 1 {
		t.Fatalf("no eval set must cap level to L1, got L%d (FR-M8-05)", got)
	}
	if got := eval.CapLevelForEvalSet(false, 1); got != 1 {
		t.Fatalf("level already ≤ L1 unchanged, got L%d", got)
	}
	if got := eval.CapLevelForEvalSet(false, 0); got != 0 {
		t.Fatalf("L0 unchanged, got L%d", got)
	}
	if got := eval.CapLevelForEvalSet(true, 4); got != 4 {
		t.Fatalf("present eval set must not cap, got L%d", got)
	}
}

// test_FR_M8_11_cross_tenant_default_off — learning is tenant-isolated by default;
// opt-in is limited to non-identifying/non-competitive artefacts; personal data and
// tenant answers never flow, opted in or not.
func TestFR_M8_11_CrossTenantAllowed(t *testing.T) {
	// default (no opt-in) → nothing flows.
	for _, a := range []eval.Artefact{eval.ArtefactInjectionPattern, eval.ArtefactLanguagePattern, eval.ArtefactPersonalData, eval.ArtefactTenantAnswers} {
		if eval.CrossTenantAllowed(false, a) {
			t.Fatalf("default must be no cross-tenant flow for %q (FR-M8-11 guardrail)", a)
		}
	}
	// opt-in → only non-identifying/non-competitive artefacts.
	if !eval.CrossTenantAllowed(true, eval.ArtefactInjectionPattern) {
		t.Fatal("opt-in should permit non-identifying safety artefacts (injection patterns)")
	}
	if !eval.CrossTenantAllowed(true, eval.ArtefactLanguagePattern) {
		t.Fatal("opt-in should permit non-identifying language patterns")
	}
	if eval.CrossTenantAllowed(true, eval.ArtefactPersonalData) {
		t.Fatal("personal data must NEVER cross tenants, opted in or not (FR-M8-11)")
	}
	if eval.CrossTenantAllowed(true, eval.ArtefactTenantAnswers) {
		t.Fatal("competitive tenant answers must NEVER cross tenants (FR-M8-11)")
	}
}

// test_FR_M8_12_no_fine_tuning_code_path — v1 has no fine-tuning entrypoint
// (FR-M8-12). Walk the internal/ tree and assert no Go symbol *defines* a
// fine-tuning path. The concept may appear in comments/strings (the docs discuss
// why it's excluded) — only declarations are forbidden.
func TestFR_M8_12_NoFineTuningCodePath(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	// func FineTune / func (x) FineTune / type FineTuner / var fineTuneJob …
	decl := regexp.MustCompile(`(?i)^\s*(func|type|var|const)\b.*fine[\s_-]?tun`)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		// Skip test files: a guard asserting the absence of a fine-tuning path
		// naturally names it, and a test is not a production code path.
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if decl.MatchString(line) {
				t.Errorf("fine-tuning code path found (FR-M8-12): %s:%d: %s", path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
}

// test_FR_M8_05_eval_set_is_held_out — the frozen eval set is held out from all
// improvement work (ADR-0013): the `evaluation_cases` table is referenced ONLY by
// the store (persistence) and this eval package/tests — never by a generation,
// retrieval or learning stage that could leak it into an answer or training path.
func TestFR_M8_05_EvalSetHeldOut(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	allowed := map[string]bool{"store": true, "eval": true} // package dir names
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "evaluation_cases") {
			return nil
		}
		pkg := filepath.Base(filepath.Dir(path))
		if !allowed[pkg] {
			t.Errorf("held-out eval set referenced outside store/eval (ADR-0013): %s (package %q)", path, pkg)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
}
