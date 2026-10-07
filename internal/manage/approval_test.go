package manage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const approvalPassphrase = "synthetic-passphrase-3c9e"

// approvalWorld is an encrypted, unlocked vault holding one secret per vault connection a, b, c and d, with
// all four approved as base configures them.
type approvalWorld struct {
	v    *vault.Vault
	base *config.Config
}

func newApprovalWorld(t *testing.T) approvalWorld {
	t.Helper()
	cfg := config.New()
	v := vault.New(t.TempDir())
	for i, name := range []string{"a", "b", "c", "d"} {
		cfg.Services["s-"+name] = config.Service{Provider: "bookstack", BaseURL: "https://" + name + ".example.test"}
		cfg.Credentials["c-"+name] = config.Credential{Type: config.CredentialTypeVault}
		cfg.Connections[name] = config.Connection{Service: "s-" + name, Credential: "c-" + name,
			Permissions: []config.Permission{config.PermissionRead}}
		offer := vault.PassphraseFunc(nil)
		if i == 0 {
			offer = func(string) (string, error) { return approvalPassphrase, nil }
		}
		if err := v.Set("c-"+name, "token", "synthetic-"+name, offer); err != nil {
			t.Fatalf("Set() = %v", err)
		}
	}
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	return approvalWorld{v: v, base: cfg}
}

// widen gives connection name one more permission, which opens it for a field its own form shows.
func widen(cfg *config.Config, name string) {
	conn := cfg.Connections[name]
	conn.Permissions = append(slicesClone(conn.Permissions), config.PermissionCreate)
	cfg.Connections[name] = conn
}

func slicesClone(p []config.Permission) []config.Permission {
	return append([]config.Permission(nil), p...)
}

func (w approvalWorld) open(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	report, err := approval.Pending(cfg, w.v)
	if err != nil {
		t.Fatalf("Pending() = %v", err)
	}
	var names []string
	for _, c := range report.Open {
		names = append(names, c.Connection)
	}
	return names
}

func TestApproveAfterChangePolicyMatrix(t *testing.T) {
	// d is already open before the change and stays so; the change opens a (saved directly) and b.
	scenario := func(t *testing.T) (approvalWorld, ApprovalSnapshot, *config.Config) {
		w := newApprovalWorld(t)
		beforeCfg := w.base.Clone()
		widen(beforeCfg, "d")
		before := SnapshotApprovals(w.v, beforeCfg)
		if !before.Valid() {
			t.Fatal("SnapshotApprovals() is not valid for an unlocked vault")
		}
		after := beforeCfg.Clone()
		widen(after, "a")
		widen(after, "b")
		return w, before, after
	}
	tests := []struct {
		name         string
		policy       ApprovalPolicy
		direct       string
		wantApproved []string
		wantOpen     []string
	}{
		{"sweep approves the direct and the newly opened, not the one already open", ApprovalSweepNewlyOpened, "a",
			[]string{"a", "b"}, []string{"d"}},
		{"sweep without a direct connection approves only the newly opened", ApprovalSweepNewlyOpened, "",
			[]string{"a", "b"}, []string{"d"}},
		{"sweep with a direct connection that was already open for a form field approves it", ApprovalSweepNewlyOpened, "d",
			[]string{"a", "b", "d"}, nil},
		{"direct only approves just the direct connection", ApprovalDirectOnly, "a",
			[]string{"a"}, []string{"b", "d"}},
		{"direct only without a direct connection approves nothing", ApprovalDirectOnly, "", nil, []string{"a", "b", "d"}},
		{"none approves nothing", ApprovalNone, "a", nil, []string{"a", "b", "d"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, before, after := scenario(t)
			res := ApproveAfterChange(context.Background(), w.v, before, after, tt.direct, tt.policy)
			if res.CheckErr != nil || res.ApproveErr != nil || res.Warning != "" || res.StayedOpen != "" {
				t.Fatalf("ApproveAfterChange() = %+v, want a clean outcome", res)
			}
			if !reflect.DeepEqual(res.Approved, tt.wantApproved) {
				t.Errorf("Approved = %v, want %v", res.Approved, tt.wantApproved)
			}
			if got := w.open(t, after); !reflect.DeepEqual(got, tt.wantOpen) {
				t.Errorf("still open = %v, want %v", got, tt.wantOpen)
			}
		})
	}
}

// A direct connection that is open for a reason its form never shows stays open, whatever the policy, and
// the result says why.
func TestApproveAfterChangeKeepsHiddenReasonsOpen(t *testing.T) {
	for _, policy := range []ApprovalPolicy{ApprovalSweepNewlyOpened, ApprovalDirectOnly} {
		w := newApprovalWorld(t)
		moved := w.base.Clone()
		svc := moved.Services["s-a"]
		svc.BaseURL = "https://moved.example.test"
		moved.Services["s-a"] = svc
		// a was already open for its origin before the change, which only saves its form.
		before := SnapshotApprovals(w.v, moved)
		after := moved.Clone()
		widen(after, "b")

		res := ApproveAfterChange(context.Background(), w.v, before, after, "a", policy)
		if res.StayedOpen != "a" || res.ForwardChanged || !strings.Contains(res.StayReason, approval.FieldOrigin) {
			t.Errorf("policy %v: result = %+v, want a staying open for its origin", policy, res)
		}
		want := []string(nil)
		if policy == ApprovalSweepNewlyOpened {
			want = []string{"b"}
		}
		if !reflect.DeepEqual(res.Approved, want) {
			t.Errorf("policy %v: Approved = %v, want %v", policy, res.Approved, want)
		}
		if got := w.open(t, after); !slicesContain(got, "a") {
			t.Errorf("policy %v: open = %v, want a still open", policy, got)
		}
	}
}

func slicesContain(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// Without anything to compare with, the sweep approves nothing; an unencrypted vault binds no connection.
func TestApproveAfterChangeNeedsASnapshotToSweep(t *testing.T) {
	w := newApprovalWorld(t)
	after := w.base.Clone()
	widen(after, "b")
	res := ApproveAfterChange(context.Background(), w.v, ApprovalSnapshot{}, after, "b", ApprovalSweepNewlyOpened)
	if len(res.Approved) != 0 || res.CheckErr != nil || res.ApproveErr != nil {
		t.Errorf("ApproveAfterChange() = %+v, want nothing", res)
	}
	if snap := SnapshotApprovals(nil, after); snap.Valid() {
		t.Error("SnapshotApprovals(nil vault) is valid")
	}
	if snap := SnapshotApprovals(vault.New(t.TempDir()), after); !snap.Valid() {
		t.Error("SnapshotApprovals(absent vault) is not valid, want a starting point of nothing open")
	}
	if res := ApproveAfterChange(context.Background(), nil, ApprovalSnapshot{}, after, "b", ApprovalDirectOnly); len(res.Approved) != 0 {
		t.Errorf("ApproveAfterChange(nil vault) = %+v, want nothing", res)
	}
}

func TestApproveOnEncrypt(t *testing.T) {
	w := newApprovalWorld(t)
	cfg := w.base.Clone()
	widen(cfg, "a")
	widen(cfg, "c")
	res := ApproveOnEncrypt(context.Background(), w.v, func() (*config.Config, error) { return cfg, nil })
	if res.ConfigErr != nil || res.ApproveErr != nil || !reflect.DeepEqual(res.Approved, []string{"a", "c"}) {
		t.Errorf("ApproveOnEncrypt() = %+v, want a and c approved", res)
	}

	broken := errors.New("cannot parse")
	res = ApproveOnEncrypt(context.Background(), w.v, func() (*config.Config, error) { return nil, broken })
	if !errors.Is(res.ConfigErr, broken) || len(res.Approved) != 0 {
		t.Errorf("ApproveOnEncrypt() = %+v, want the config error reported and nothing approved", res)
	}

	res = ApproveOnEncrypt(context.Background(), w.v, func() (*config.Config, error) {
		return nil, &config.NotFoundError{Path: filepath.Join(os.TempDir(), "missing")}
	})
	if res.ConfigErr != nil || len(res.Approved) != 0 {
		t.Errorf("ApproveOnEncrypt() = %+v, want a missing configuration to be silent", res)
	}
}
