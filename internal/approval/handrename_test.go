package approval

import (
	"context"
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// handFixture is the fixture's configuration with ids, after wiki-read was renamed to read-main by hand.
func handFixture(t *testing.T) *config.Config {
	t.Helper()
	cfg, _ := fixture(t)
	cfg.AssignIDs()
	if _, err := cfg.RenameConnection("wiki-read", "read-main"); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHandRenamesFromNames(t *testing.T) {
	cfg := handFixture(t)
	id := cfg.Connections["read-main"].ID
	writeID := cfg.Connections["wiki-write"].ID
	cases := []struct {
		name        string
		approvals   map[string]string
		credentials map[string]string
		want        []HandRename
	}{
		{"by id", map[string]string{"wiki-read": id, "wiki-write": writeID}, nil,
			[]HandRename{{Approval: "wiki-read", Connection: "read-main"}}},
		{"rest of a rename", map[string]string{"wiki-read": id, "read-main": id}, nil, nil},
		{"id claimed twice", map[string]string{"wiki-read": id, "old-read": id}, nil,
			[]HandRename{{Approval: "old-read", Ambiguous: true}, {Approval: "wiki-read", Ambiguous: true}}},
		{"id of a removed connection", map[string]string{"wiki-read": "0123456789abcdef0123456789abcdef"}, nil, nil},
		{"without an id, by credential", map[string]string{"wiki-read": "", "wiki-write": writeID},
			map[string]string{"wiki-read": "reader"}, []HandRename{{Approval: "wiki-read", Connection: "read-main"}}},
		{"without an id, one connection for two approvals", map[string]string{"wiki-read": "", "old-read": ""},
			map[string]string{"wiki-read": "reader", "old-read": "reader"},
			[]HandRename{{Approval: "old-read", Ambiguous: true}, {Approval: "wiki-read", Ambiguous: true}}},
		{"without an id, claimed by an id", map[string]string{"wiki-read": "", "old-read": id},
			map[string]string{"wiki-read": "reader"}, []HandRename{{Approval: "old-read", Connection: "read-main"}}},
		{"without an id or a credential", map[string]string{"wiki-read": ""}, nil, nil},
		{"without an id, a keyring credential", map[string]string{"old-desk": ""}, map[string]string{"old-desk": "desk"}, nil},
		{"without an id, a credential nobody reads", map[string]string{"old": ""}, map[string]string{"old": "gone"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := HandRenames(cfg, vault.Names{Approvals: c.approvals, ApprovalCredentials: c.credentials})
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("HandRenames() = %+v, want %+v", got, c.want)
			}
		})
	}

	// Two connections without an approval read the credential an approval without an id was given for.
	twin := cfg.Clone()
	twin.Connections["read-two"] = config.Connection{Service: "wiki", Credential: "reader"}
	got := HandRenames(twin, vault.Names{Approvals: map[string]string{"wiki-read": ""},
		ApprovalCredentials: map[string]string{"wiki-read": "reader"}})
	if want := []HandRename{{Approval: "wiki-read", Ambiguous: true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("HandRenames() with two candidates = %+v, want %+v", got, want)
	}
}

// Pending tells a hand rename apart from the rest of a rename and finds what the names of the vault tell.
func TestPendingTellsAHandRename(t *testing.T) {
	cfg, v := fixture(t)
	cfg.AssignIDs()
	if _, _, err := Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatal(err)
	}
	renamed := cfg.Clone()
	if _, err := renamed.RenameConnection("wiki-read", "read-main"); err != nil {
		t.Fatal(err)
	}
	report, err := Pending(renamed, v)
	if err != nil {
		t.Fatal(err)
	}
	want := []HandRename{{Approval: "wiki-read", Connection: "read-main"}}
	if !reflect.DeepEqual(report.HandRenames, want) || len(report.Leftovers) != 0 ||
		!reflect.DeepEqual(report.Stale, []string{"wiki-read"}) {
		t.Errorf("report = %+v, want the hand rename among the stale approvals", report)
	}
	names, err := v.KnownNames()
	if err != nil {
		t.Fatal(err)
	}
	if got := HandRenames(renamed, names); !reflect.DeepEqual(got, want) {
		t.Errorf("HandRenames() from the names = %+v, want %+v", got, want)
	}

	if _, _, err := v.CarryApproval("wiki-read", "read-main", cfg.Connections["wiki-read"].ID); err != nil {
		t.Fatal(err)
	}
	report, err = Pending(renamed, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.HandRenames) != 0 || !reflect.DeepEqual(report.Leftovers,
		[]Leftover{{Approval: "wiki-read", Connection: "read-main"}}) {
		t.Errorf("report = %+v, want the rest of a rename", report)
	}
}
