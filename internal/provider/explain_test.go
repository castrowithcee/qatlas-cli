package provider

import "testing"

func TestExplainCoversEveryFailureClass(t *testing.T) {
	for _, class := range []Class{ClassUnreachable, ClassTLS, ClassAuth, ClassPermission, ClassTimeout,
		ClassRateLimited, ClassInvalidResponse, ClassProviderError, ClassNotFound} {
		if got := Explain(class, "X"); got == "" || got == Explain("unknown", "X") {
			t.Errorf("Explain(%q) = %q, want a class-specific text", class, got)
		}
	}
}
