package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const renameEntryConfig = `version: 2
services:
  svc:
    provider: acme.chat
    base_url: https://example.invalid
  spare:
    provider: acme.chat
    base_url: https://spare.example.invalid
credentials:
  cred:
    type: env
    values:
      token: ACME_TOKEN
connections:
  conn:
    service: svc
    credential: cred
    permissions: [read]
  other:
    service: svc
    credential: cred
    permissions: [read]
  spare:
    service: spare
    credential: cred
    permissions: [read]
defaults:
  connections:
    acme.chat: conn
    chat: conn
    spare: spare
`

func TestRenameEntriesRewriteEveryReferenceAndKeepTheID(t *testing.T) {
	cfg, err := Decode(strings.NewReader(renameEntryConfig), renameProviders)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AssignIDs()
	serviceID, connID := cfg.Services["svc"].ID, cfg.Connections["conn"].ID

	refs, err := cfg.RenameService("svc", "svc-eu")
	if err != nil || !reflect.DeepEqual(refs, []string{"connections.conn.service", "connections.other.service"}) {
		t.Fatalf("RenameService() = %v, %v", refs, err)
	}
	refs, err = cfg.RenameConnection("conn", "main")
	if err != nil || !reflect.DeepEqual(refs, []string{"defaults.connections.acme.chat", "defaults.connections.chat"}) {
		t.Fatalf("RenameConnection() = %v, %v", refs, err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	if _, ok := cfg.Services["svc"]; ok || cfg.Services["svc-eu"].ID != serviceID {
		t.Errorf("services = %v", cfg.Services)
	}
	if _, ok := cfg.Connections["conn"]; ok || cfg.Connections["main"].ID != connID || cfg.Connections["main"].Service != "svc-eu" {
		t.Errorf("connections = %v", cfg.Connections)
	}
	if cfg.Defaults.Connections["spare"] != "spare" || cfg.Connections["spare"].Service != "spare" {
		t.Error("an unrelated reference changed")
	}
}

func TestRenameEntriesRefuse(t *testing.T) {
	cfg, err := Decode(strings.NewReader(renameEntryConfig), renameProviders)
	if err != nil {
		t.Fatal(err)
	}
	before := cfg.Clone()
	var taken *TakenError
	if _, err := cfg.RenameConnection("conn", "other"); !errors.As(err, &taken) {
		t.Errorf("onto another connection = %v, want a TakenError", err)
	}
	if _, err := cfg.RenameService("svc", "spare"); !errors.As(err, &taken) {
		t.Errorf("onto another service = %v, want a TakenError", err)
	}
	var missing *NotThereError
	if _, err := cfg.RenameConnection("nothing", "x"); !errors.As(err, &missing) {
		t.Errorf("a missing connection = %v, want a NotThereError", err)
	}
	for _, name := range []string{"", "-x", "a b", "conn"} {
		if _, err := cfg.RenameConnection("conn", name); err == nil {
			t.Errorf("RenameConnection(%q) = nil, want an error", name)
		}
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Error("a refused rename changed the configuration")
	}
}
