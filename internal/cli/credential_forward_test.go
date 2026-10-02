package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func forwardFixture(t *testing.T) string {
	t.Helper()
	dir := keyringFixture(t)
	body := strings.Replace(keyringConfig, "connections:\n", `  shared:
    type: keyring
    forward: true
    fields: [user, pass]
    description: shared login
connections:
`, 1)
	body = strings.Replace(body, "    credential: vault-reader\n", "    credential: vault-reader\n    forward_secrets: [shared]\n", 1)
	if err := os.WriteFile(configIn(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A field of a payload secret is stored like any other secret, but a value shorter than four characters is
// refused before anything is written, and so is a name that is no field.
func TestCredentialSetForwardField(t *testing.T) {
	dir := forwardFixture(t)
	store := secret.NewMemoryStore()
	opts := testOptionsIn(t, dir, store)

	for _, tt := range []struct {
		name, field, value string
		code               int
	}{
		{"a short value", "user", "abc", exitUsage},
		{"an unknown field", "other", "abcdef", exitUsage},
		{"a provider role", "token-id", "abcdef", exitUsage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runWithInput(t, opts, tt.value+"\n",
				"credential", "set", "shared", tt.field, "--config", configIn(dir))
			if code != tt.code {
				t.Fatalf("exit = %d, want %d; stderr %q", code, tt.code, stderr)
			}
			if _, err := store.Get(context.Background(), secret.StoreKey("shared", tt.field)); err == nil {
				t.Errorf("a refused value was stored")
			}
			if strings.Contains(stdout+stderr, tt.value) && len(tt.value) >= 4 {
				t.Errorf("the output carries the value: %q", stdout+stderr)
			}
		})
	}

	code, _, stderr := runWithInput(t, opts, "abcd\n", "credential", "set", "shared", "user", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("exit = %d, want ok; stderr %q", code, stderr)
	}
	if got, err := store.Get(context.Background(), secret.StoreKey("shared", "user")); err != nil || got != "abcd" {
		t.Errorf("stored value = %q, %v", got, err)
	}
}
