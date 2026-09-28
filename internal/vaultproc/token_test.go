package vaultproc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// tokenFixture is an encrypted vault on disk holding gh-a, the connection vorbild approved to read it, and
// the agent token agent with vorbild as its vorbild, served by a server with the vault's key that approves
// with tokens and keeps the log. It returns the server, a client, the token's value, and the vault directory.
func tokenFixture(t *testing.T) (*Server, *Client, string, string) {
	t.Helper()
	v := vault.New(t.TempDir())
	if err := v.Set("gh-a", "token", "synthetic-token", func(string) (string, error) { return "s3cret", nil }); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	if err := v.Approve([]vault.Scope{tokenVorbild()}); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	token, err := v.CreateToken("agent", []string{"vorbild"}, nil)
	if err != nil {
		t.Fatalf("CreateToken() = %v", err)
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
	s.KeepLog(v.Dir(), 90)
	return s, &Client{Recipient: key.Recipient().String(), Verify: allow}, token.Value, v.Dir()
}

func tokenVorbild() vault.Scope {
	return vault.Scope{Connection: "vorbild", Credential: "gh-a", Provider: "github",
		Origin: "https://api.github.com", Permissions: []string{"read", "create"}}
}

func tokenChange(name string, permissions ...string) vault.Scope {
	scope := tokenVorbild()
	scope.Connection, scope.Permissions = name, permissions
	return scope
}

// logText returns every invocation log line below dir.
func logText(t *testing.T, dir string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "logs", "*.jsonl"))
	var text strings.Builder
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text.Write(data)
	}
	return text.String()
}

// The vault process approves what the token covers, checks every later get against it at once, leaves the
// rest open, and logs each decision with the token's name, never its value.
func TestServerApprovesWithAToken(t *testing.T) {
	s, c, token, dir := tokenFixture(t)
	covered, wide := tokenChange("covered", "read"), tokenChange("wide", "read", "delete")
	resp, err := exchangeOverPipe(t, s, c, request{Op: opApproveToken, Token: token,
		Scopes: []vault.Scope{tokenVorbild(), covered, wide}})
	if err != nil || resp.Approval == nil || resp.Unlogged {
		t.Fatalf("approve-token = %+v, %v", resp, err)
	}
	result := *resp.Approval
	if result.Token != "agent" || len(result.Changes) != 2 || !result.Changes[0].Approved ||
		result.Changes[1].Approved || result.Changes[1].Gaps[0] != vault.GapPermissions {
		t.Fatalf("approval = %+v, want covered approved and wide open for its permissions", result)
	}
	if resp, err := exchangeOverPipe(t, s, c, request{Op: opCheck, Scope: &covered}); err != nil || !resp.Found {
		t.Errorf("check of the approved connection = %+v, %v, want it allowed at once", resp, err)
	}
	if _, err := exchangeOverPipe(t, s, c, request{Op: opCheck, Scope: &wide}); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("check of the open connection = %v, want ErrApprovalRequired", err)
	}

	log := logText(t, dir)
	for _, want := range []string{`"connection":"covered","effect":"create","result":"success","token":"agent"`,
		`"connection":"wide","effect":"create","result":"admin-required","token":"agent"`} {
		if !strings.Contains(log, want) {
			t.Errorf("log = %s, want %s", log, want)
		}
	}
	if strings.Contains(log, token) {
		t.Errorf("the log holds the token's value")
	}
}

// A token the vault does not hold is refused and logged without a name; a server that does not approve with
// tokens, or has locked, refuses before it looks at one.
func TestServerRefusesTokens(t *testing.T) {
	s, c, token, dir := tokenFixture(t)
	scopes := []vault.Scope{tokenVorbild(), tokenChange("covered", "read")}
	if _, err := exchangeOverPipe(t, s, c, request{Op: opApproveToken, Token: "qat_wrong", Scopes: scopes}); !errors.Is(err, vault.ErrTokenUnknown) {
		t.Errorf("approve-token with an unknown token = %v, want ErrTokenUnknown", err)
	}
	if log := logText(t, dir); !strings.Contains(log, `"operation":"vault.approve","result":"admin-required"`) ||
		strings.Contains(log, "qat_wrong") {
		t.Errorf("log = %s, want the refusal without the value", log)
	}

	plain := NewServer(testKey, testSecrets(), testBindings())
	plain.Verify = allow
	if _, err := exchangeOverPipe(t, plain, testClient(), request{Op: opApproveToken, Token: token}); !errors.Is(err, ErrNoTokens) {
		t.Errorf("approve-token without ApproveWithTokens = %v, want ErrNoTokens", err)
	}

	s.stop()
	s.wipe()
	if _, err := exchangeOverPipe(t, s, c, request{Op: opApproveToken, Token: token, Scopes: scopes}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("approve-token of a locked server = %v, want ErrNotRunning", err)
	}
}

// Token approvals running at the same time as each other and as gets neither race nor lose an approval.
func TestServerApprovesWithTokensConcurrently(t *testing.T) {
	s, c, token, dir := tokenFixture(t)
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		name := fmt.Sprintf("agent-%d", i)
		go func() {
			defer wg.Done()
			scopes := []vault.Scope{tokenVorbild(), tokenChange(name, "read")}
			resp, err := exchangeOverPipe(t, s, c, request{Op: opApproveToken, Token: token, Scopes: scopes,
				Names: []string{name}})
			if err != nil || resp.Approval == nil || len(resp.Approval.Changes) != 1 || !resp.Approval.Changes[0].Approved {
				t.Errorf("approve-token %s = %+v, %v", name, resp, err)
			}
		}()
		go func() {
			defer wg.Done()
			vorbild := tokenVorbild()
			if _, err := exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "gh-a", Role: "token",
				Scope: &vorbild}); err != nil {
				t.Errorf("get during approvals = %v", err)
			}
		}()
	}
	wg.Wait()

	opened, err := vault.OpenWithKey(dir, s.key.(*age.X25519Identity))
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := opened.Approvals()
	if err != nil || len(approvals) != n+1 {
		t.Errorf("approvals on disk = %d, %v, want every one of %d plus the vorbild", len(approvals), err, n)
	}
}
