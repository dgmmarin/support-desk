package onboarding

import "testing"

// FR-M11-07 / NFR-R-04: the sandbox seam has no Sender, so a send is PHYSICALLY
// impossible (not policy-disabled). AssertNoSend re-proves that guarantee — it must
// pass, meaning deliver.New with the sandbox's (nil) sender yields ErrSendImpossible.
func TestSandboxAssertNoSend(t *testing.T) {
	if err := AssertNoSend(); err != nil {
		t.Fatalf("sandbox must be send-impossible (NFR-R-04): %v", err)
	}
}
