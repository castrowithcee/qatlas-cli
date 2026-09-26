package vault

import (
	"errors"
	"testing"
	"time"
)

// A process whose standard streams are all redirected never waits for a prompt, even where a console or a
// controlling terminal could be opened. Each call runs with a deadline, so the test fails instead of
// hanging should the rule ever be missing.
func TestPromptsRefuseANonInteractiveProcess(t *testing.T) {
	original := interactive
	interactive = func() bool { return false }
	t.Cleanup(func() { interactive = original })

	done := make(chan [2]error, 1)
	go func() {
		_, passErr := ReadPassphrase("vault passphrase: ")
		_, confirmErr := ReadConfirm("continue? [y/N] ")
		done <- [2]error{passErr, confirmErr}
	}()

	select {
	case errs := <-done:
		for i, err := range errs {
			if !errors.Is(err, ErrNoTerminal) {
				t.Errorf("prompt %d: err = %v, want ErrNoTerminal", i, err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a prompt waited for input in a process that is not interactive")
	}
}
