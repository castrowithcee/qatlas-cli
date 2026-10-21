package vaultproc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	namesSecret       = "synthetic-names-secret"
	namesRole         = "synthetic-role-name"
	namesCredentialID = "0123456789abcdef0123456789abcdef"
	namesConnectionID = "fedcba9876543210fedcba9876543210"
)

// namesFixture is an encrypted vault on disk holding gh-a, with a config id and a secret under a role of an
// unusual name, and the connection vorbild approved with a config id, served by a server that knows the
// vault's directory.
func namesFixture(t *testing.T) (*Server, *Client) {
	t.Helper()
	v := vault.New(t.TempDir())
	offer := func(string) (string, error) { return "s3cret", nil }
	if _, err := v.SetUndoableWithID("gh-a", namesCredentialID, namesRole, namesSecret, offer); err != nil {
		t.Fatalf("SetUndoableWithID() = %v", err)
	}
	if err := v.ApproveConnections([]vault.Scope{tokenVorbild()}, map[string]string{"vorbild": namesConnectionID}); err != nil {
		t.Fatalf("ApproveConnections() = %v", err)
	}
	snap, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(key, snap.Secrets, snap.Bindings)
	s.Verify = allow
	s.ApproveWithTokens(v.Dir())
	return s, &Client{Recipient: key.Recipient().String(), Verify: allow}
}

// The vault process tells the entry names and approval keys with their config ids, and no value, role name,
// fingerprint or scope.
func TestServerTellsTheNamesOfTheVault(t *testing.T) {
	s, c := namesFixture(t)
	resp, err := exchangeOverPipe(t, s, c, request{Op: opNames})
	if err != nil {
		t.Fatalf("names error = %v", err)
	}
	if got := resp.Names.Entries["gh-a"]; got != namesCredentialID || len(resp.Names.Entries) != 1 {
		t.Errorf("entries = %v, want gh-a with its config id", resp.Names.Entries)
	}
	if got := resp.Names.Approvals["vorbild"]; got != namesConnectionID || len(resp.Names.Approvals) != 1 {
		t.Errorf("approvals = %v, want vorbild with its config id", resp.Names.Approvals)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{namesSecret, namesRole, "s3cret", "fingerprint", "scope", "https://api.github.com"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("the answer %s holds %q", data, forbidden)
		}
	}

	names, err := namesResult(resp, nil)
	if err != nil || names.Entries["gh-a"] != namesCredentialID || names.Approvals["vorbild"] != namesConnectionID {
		t.Errorf("names = %+v, %v", names, err)
	}
}

func TestServerRefusesNamesWhereItCannotTell(t *testing.T) {
	s, c := namesFixture(t)
	plain := NewServer(testKey, testSecrets(), testBindings())
	plain.Verify = allow
	if _, err := exchangeOverPipe(t, plain, testClient(), request{Op: opNames}); !errors.Is(err, ErrNoNames) {
		t.Errorf("names without the vault directory = %v, want ErrNoNames", err)
	}

	s.stop()
	s.wipe()
	if _, err := exchangeOverPipe(t, s, c, request{Op: opNames}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("names of a locked server = %v, want ErrNotRunning", err)
	}
}

// A process of an earlier version answers every operation it does not know with bad-request, which reads as
// ErrNoNames, not as another version or a bad request; a missing process keeps its own error.
func TestClientNamesAgainstAnEarlierProcess(t *testing.T) {
	s, c := namesFixture(t)
	// An earlier process reaches the same branch for names as the current one for an operation nobody knows.
	resp, err := exchangeOverPipe(t, s, c, request{Op: "dump"})
	_, err = namesResult(resp, err)
	if !errors.Is(err, ErrNoNames) || errors.Is(err, ErrVersion) {
		t.Errorf("an earlier process's answer reads %v, want ErrNoNames", err)
	}

	missing := &Client{Path: shortTempDir(t) + "/none.sock", Recipient: testRecipient, Verify: allow}
	if _, err := missing.Names(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Names() without a process = %v, want ErrNotRunning", err)
	}
}
