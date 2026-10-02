package vaultproc

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const sharedCredentialID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// forwardScopeOf is testScope releasing the forward credential shared with two fields.
func forwardScopeOf() *vault.Scope {
	s := *testScope()
	s.Forward = []vault.ForwardSecret{{Name: "shared", Fields: []string{"user", "pass"}}}
	return &s
}

func forwardServer() *Server {
	bindings := vault.Bindings{
		IDs: map[string]string{"wiki-reader": testCredentialID, "shared": sharedCredentialID},
		Approvals: map[string]string{
			"wiki": vault.Fingerprint(*forwardScopeOf(), testCredentialID),
		},
	}
	secrets := testSecrets()
	secrets["shared"] = map[string]string{"user": "synthetic-user", "pass": "synthetic-pass", "key": "synthetic-key"}
	s := NewServer(testKey, secrets, bindings)
	s.Verify = allow
	return s
}

func TestServerHandsForwardCredentialToTheReleasingConnection(t *testing.T) {
	s, c := forwardServer(), testClient()

	resp, err := exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "shared", Role: "user", Scope: forwardScopeOf()})
	if err != nil || !resp.Found || resp.Value != "synthetic-user" {
		t.Fatalf("get of a released forward credential = %+v, %v, want its value", resp, err)
	}
	if _, err := exchangeOverPipe(t, s, c, request{Op: opCheck, Credential: "shared", Scope: forwardScopeOf()}); err != nil {
		t.Errorf("check of a released forward credential = %v, want nil", err)
	}

	// A role the approval does not name stays inside the process.
	resp, err = exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "shared", Role: "key", Scope: forwardScopeOf()})
	if !errors.Is(err, vault.ErrApprovalRequired) || resp.Value != "" {
		t.Errorf("get of a role outside the released fields = %+v, %v, want ErrApprovalRequired", resp, err)
	}

	// Another connection, or the same one as it was approved without the release, is refused.
	other := forwardScopeOf()
	other.Connection, other.Forward = "wiki2", nil
	for name, scope := range map[string]*vault.Scope{
		"a connection that does not release it":       other,
		"the approved connection without the release": testScope(),
		"a scope that releases more": func() *vault.Scope {
			s := forwardScopeOf()
			s.Forward = append(s.Forward, vault.ForwardSecret{Name: "wiki-reader", Fields: []string{"token"}})
			return s
		}(),
		"a changed field list": func() *vault.Scope {
			s := forwardScopeOf()
			s.Forward[0].Fields = []string{"user", "pass", "key"}
			return s
		}(),
	} {
		resp, err := exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "shared", Role: "user", Scope: scope})
		if !errors.Is(err, vault.ErrApprovalRequired) || resp.Value != "" {
			t.Errorf("get for %s = %+v, %v, want ErrApprovalRequired", name, resp, err)
		}
		if _, err := exchangeOverPipe(t, s, c, request{Op: opCheck, Credential: "shared", Scope: scope}); !errors.Is(err, vault.ErrApprovalRequired) {
			t.Errorf("check for %s = %v, want ErrApprovalRequired", name, err)
		}
	}

	// A credential no scope releases is never handed out, whatever the approval.
	resp, err = exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "unlisted", Role: "user", Scope: forwardScopeOf()})
	if err != nil || resp.Value != "" {
		t.Errorf("get of an unheld credential = %+v, %v", resp, err)
	}
}

// A process of protocol 5 and a client of protocol 6 refuse each other, in both directions, with ErrVersion.
func TestProtocolVersionMismatchIsReported(t *testing.T) {
	if Version != 6 {
		t.Fatalf("Version = %d, want 6", Version)
	}
	const previous = Version - 1

	// An old client speaks to this server.
	s := forwardServer()
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()
	go s.serveConn(serverEnd)
	go func() { _ = writeMessage(clientEnd, hello{V: previous}) }()
	var resp response
	if err := readMessage(clientEnd, &resp); err != nil {
		t.Fatalf("readMessage() = %v", err)
	}
	if resp.Error != codeVersion || resp.Value != "" || !errors.Is(answerError(resp.Error), ErrVersion) {
		t.Errorf("answer to protocol %d = %+v, want the version error", previous, resp)
	}

	// This client speaks to an old process.
	conn, _, _ := fakeServer(t, func(challenge []byte) response {
		proof, _ := solveChallenge(testKey, challenge)
		return response{V: previous, Proof: proof}
	})
	c := testClient()
	ctx := context.Background()
	_, err := c.exchange(ctx, conn, c.deadline(ctx), request{Op: opGet, Credential: "shared", Role: "user", Scope: forwardScopeOf()})
	_ = conn.Close()
	if !errors.Is(err, ErrVersion) {
		t.Errorf("exchange with a protocol %d process = %v, want ErrVersion", previous, err)
	}
}
