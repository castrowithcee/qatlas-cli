package secret

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// TestVaultProcessRemedyAfterAnUpdate checks the choice of the next step with synthetic errors and no vault:
// only a tui, web, or mcp process whose own program was replaced, refused as a program, is told to restart.
func TestVaultProcessRemedyAfterAnUpdate(t *testing.T) {
	t.Cleanup(func() {
		SetLongRunning("")
		replacedProgram = func() bool { _, r := vaultproc.ReplacedProgram(); return r }
	})
	peer := func(err error) error { return &vaultproc.PeerError{PID: 4242, Err: err} }
	refusedProgram := peer(vaultproc.ErrProgramRefused)
	refusedOther := peer(fmt.Errorf("%w: it runs another program", vaultproc.ErrRefused))
	refusedUnproven := peer(fmt.Errorf("%w: it cannot prove that it holds this vault's key", vaultproc.ErrRefused))

	const updated = "qatlas was updated since this process started; "
	steps := map[string]string{
		"tui": "restart 'qatlas tui'",
		"web": "restart 'qatlas web'",
		"mcp": "reconnect the qatlas MCP server in the client",
	}
	plain := func(err error) string {
		SetLongRunning("")
		return VaultProcessRemedy(err)
	}
	for command, step := range steps {
		for _, tc := range []struct {
			name     string
			err      error
			replaced bool
			want     string // "" keeps the plain remedy
		}{
			{"replaced program refused", refusedProgram, true, updated + step},
			{"program not replaced", refusedProgram, false, ""},
			{"another program", refusedOther, true, ""},
			{"unproven key", refusedUnproven, true, ""},
			{"version", peer(vaultproc.ErrVersion), true, ""},
			{"no refusal", errors.New("it failed to answer"), true, ""},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				replacedProgram = func() bool { return tc.replaced }
				SetLongRunning(command)
				got, end := VaultProcessRemedy(tc.err), EndVaultProcess(tc.err)
				if tc.want == "" {
					if want := plain(tc.err); got != want {
						t.Errorf("remedy = %q, want the plain %q", got, want)
					}
					return
				}
				if got != tc.want || end != step {
					t.Errorf("remedy = %q, end = %q, want %q and %q", got, end, tc.want, step)
				}
				if strings.Contains(got+end, "kill") {
					t.Errorf("remedy %q must not mention kill", got+end)
				}
			})
		}
	}
	t.Run("other commands", func(t *testing.T) {
		replacedProgram = func() bool { return true }
		SetLongRunning("")
		got := VaultProcessRemedy(refusedProgram)
		if !strings.Contains(got, "kill") || !strings.Contains(got, "4242") || strings.Contains(got, "updated") {
			t.Errorf("remedy for a one-shot command = %q, want the kill hint", got)
		}
	})
}
