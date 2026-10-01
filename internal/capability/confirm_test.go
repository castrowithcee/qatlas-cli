package capability

import (
	"context"
	"testing"
)

func TestConfirmed(t *testing.T) {
	if Confirmed(context.Background()) {
		t.Fatal("a plain context must not be confirmed")
	}
	var none context.Context
	if Confirmed(none) {
		t.Fatal("a nil context must not be confirmed")
	}
	ctx := WithConfirmed(context.Background())
	if !Confirmed(ctx) {
		t.Fatal("WithConfirmed must mark the context confirmed")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if !Confirmed(child) {
		t.Fatal("a derived context must keep the confirmation")
	}
}
