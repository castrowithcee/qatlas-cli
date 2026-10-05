package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// handoverRun is one update through the command or the editor's updater, and what it said.
type handoverRun struct {
	err     error
	updated bool
	note    string
	program string
	// ranBefore is whether the program's replacement was reached.
	ranBefore bool
	// runningBefore is whether the vault process answered right before the replacement.
	runningBefore bool
	// replacedAfter is whether the new program was in place when the update was done.
	replacedAfter bool
}

// updateSurfaces are the two ways to update: 'qatlas update --yes' and the editor's updater.
var updateSurfaces = map[string]func(t *testing.T, dir string, updater *selfupdate.Client) (updated bool, note string, err error){
	"command": func(t *testing.T, dir string, updater *selfupdate.Client) (bool, string, error) {
		code, stdout, stderr := runWithInput(t, &Options{Updater: updater}, "", "update", "--yes", "--config", configIn(dir),
			"--output", "json")
		if code != exitOK {
			return false, "", errors.New(stderr)
		}
		return strings.Contains(stdout, `"updated":true`), stderr, nil
	},
	"editor": func(t *testing.T, dir string, updater *selfupdate.Client) (bool, string, error) {
		u := tuiUpdater(&Options{Updater: updater, Config: configIn(dir)}, "v1.0.0")
		result, err := u.Update(context.Background())
		return result.Updated, u.(interface{ Note() string }).Note(), err
	},
}

// handoverServer serves the fixture vault in this process with a handover that trusts key. The successor
// cannot be started: the test binary's own program is never replaced, so a commit is refused.
func handoverServer(t *testing.T, dir string, key ssh.PublicKey) *vaultproc.Client {
	t.Helper()
	_, client := serveVaultInProcessWith(t, dir, func(s *vaultproc.Server) {
		s.AllowHandover(vault.New(dir).Dir(), []ssh.PublicKey{key},
			func(context.Context, string, vault.Snapshot) (int, func(), error) {
				return 0, nil, errors.New("no successor in this test")
			})
	})
	return client
}

func runHandoverUpdate(t *testing.T, surface, dir string, signer ssh.Signer, trusted ssh.PublicKey, client *vaultproc.Client) handoverRun {
	t.Helper()
	var run handoverRun
	executable := installedProgram(t)
	updater := signedReleaseUpdater(t, executable, signer, trusted, func() {
		run.ranBefore = true
		if client != nil {
			_, err := client.Status(context.Background())
			run.runningBefore = err == nil
		}
		if data, _ := os.ReadFile(executable); string(data) != "old-program" {
			t.Error("the program was replaced before the vault process was prepared or locked")
		}
	})
	updater.AfterReplace = func(context.Context, selfupdate.Release) {
		data, _ := os.ReadFile(executable)
		run.replacedAfter = string(data) == "new-program"
	}
	run.updated, run.note, run.err = updateSurfaces[surface](t, dir, updater)
	data, _ := os.ReadFile(executable)
	run.program = string(data)
	return run
}

func running(client *vaultproc.Client) bool {
	_, err := client.Status(context.Background())
	return err == nil
}

// A signed release with the setting handover prepares the handover before the replacement without locking
// the vault process; the commit comes after it. Here the successor cannot take over, so the vault process
// locks itself and the message names 'qatlas vault unlock'.
func TestUpdatePreparesTheHandoverBeforeAndCommitsAfterTheReplacement(t *testing.T) {
	for surface := range updateSurfaces {
		t.Run(surface, func(t *testing.T) {
			dir := encryptedVaultFixture(t, "")
			signer := releasetest.NewSigner(t)
			client := handoverServer(t, dir, signer.PublicKey())
			run := runHandoverUpdate(t, surface, dir, signer, signer.PublicKey(), client)
			if run.err != nil || !run.updated || run.program != "new-program" || !run.replacedAfter {
				t.Fatalf("run = %+v", run)
			}
			if !run.runningBefore {
				t.Error("the vault process was locked before the replacement although a handover was prepared")
			}
			for _, want := range []string{"could not hand the vault over", "locked itself", "'qatlas vault unlock'"} {
				if !strings.Contains(run.note, want) {
					t.Errorf("note does not say %q: %q", want, run.note)
				}
			}
			if running(client) {
				t.Error("the vault process still runs although its handover failed")
			}
		})
	}
}

func TestUpdateLocksBeforeTheReplacementWhenTheSettingIsLock(t *testing.T) {
	for surface := range updateSurfaces {
		t.Run(surface, func(t *testing.T) {
			dir := encryptedVaultFixture(t, "")
			unlocked := vault.New(dir)
			if _, err := unlocked.Unlock("s3cret-phrase"); err != nil {
				t.Fatal(err)
			}
			if err := unlocked.SetUpdateBehaviour(vault.UpdateLock); err != nil {
				t.Fatal(err)
			}
			signer := releasetest.NewSigner(t)
			client := handoverServer(t, dir, signer.PublicKey())
			run := runHandoverUpdate(t, surface, dir, signer, signer.PublicKey(), client)
			if run.err != nil || !run.updated || run.program != "new-program" {
				t.Fatalf("run = %+v", run)
			}
			if run.runningBefore || running(client) {
				t.Error("the vault process was not locked before the replacement")
			}
			for _, want := range []string{"locked as the update behaviour says", "'qatlas vault unlock'"} {
				if !strings.Contains(run.note, want) {
					t.Errorf("note does not say %q: %q", want, run.note)
				}
			}
		})
	}
}

func TestUpdateLocksBeforeTheReplacementWhenTheReleaseIsNotSigned(t *testing.T) {
	for surface := range updateSurfaces {
		t.Run(surface, func(t *testing.T) {
			dir := encryptedVaultFixture(t, "")
			client := handoverServer(t, dir, releasetest.NewSigner(t).PublicKey())
			run := runHandoverUpdate(t, surface, dir, nil, nil, client)
			if run.err != nil || !run.updated || run.program != "new-program" {
				t.Fatalf("run = %+v", run)
			}
			if run.runningBefore || running(client) {
				t.Error("the vault process was not locked before the replacement")
			}
			for _, want := range []string{"not signed", "'qatlas vault unlock'"} {
				if !strings.Contains(run.note, want) {
					t.Errorf("note does not say %q: %q", want, run.note)
				}
			}
		})
	}
}

func TestUpdateWithAnInvalidSignatureChangesNothing(t *testing.T) {
	for surface := range updateSurfaces {
		t.Run(surface, func(t *testing.T) {
			dir := encryptedVaultFixture(t, "")
			signer := releasetest.NewSigner(t)
			client := handoverServer(t, dir, signer.PublicKey())
			run := runHandoverUpdate(t, surface, dir, signer, releasetest.NewSigner(t).PublicKey(), client)
			if run.err == nil || !strings.Contains(run.err.Error(), "signature is invalid") {
				t.Fatalf("err = %v, want an invalid signature", run.err)
			}
			if run.updated || run.program != "old-program" || run.ranBefore || run.note != "" {
				t.Errorf("run = %+v, want nothing changed", run)
			}
			if !running(client) {
				t.Error("the vault process was locked by a refused update")
			}
		})
	}
}

func TestUpdateWithoutAVaultProcessSaysNothingAboutOne(t *testing.T) {
	for surface := range updateSurfaces {
		t.Run(surface, func(t *testing.T) {
			dir := encryptedVaultFixture(t, "")
			t.Setenv("XDG_RUNTIME_DIR", shortRuntimeDir(t))
			signer := releasetest.NewSigner(t)
			run := runHandoverUpdate(t, surface, dir, signer, signer.PublicKey(), nil)
			if run.err != nil || !run.updated || run.program != "new-program" || run.note != "" {
				t.Errorf("run = %+v", run)
			}
		})
	}
}
