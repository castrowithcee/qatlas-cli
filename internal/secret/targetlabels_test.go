package secret

import (
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// A target label is display text: changing it changes neither the scope nor the fingerprint an approval
// is bound to.
func TestTargetLabelsLeaveTheScopeAndFingerprintAlone(t *testing.T) {
	cfg := config.New()
	cfg.Services["svc"] = config.Service{Provider: "wiki", BaseURL: "https://wiki.example.invalid"}
	cfg.Credentials["cred"] = config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{"token": "T"}}
	cfg.Connections["conn"] = config.Connection{ID: "id-1", Service: "svc", Credential: "cred", Targets: []string{"a", "b"}}
	resolve := func() *config.Resolved {
		resolved, err := cfg.Resolve("conn", "")
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	before := ScopeOf(resolve())
	conn := cfg.Connections["conn"]
	conn.TargetLabels = map[string]string{"a": "Projects", "b": "Archive"}
	cfg.Connections["conn"] = conn
	withLabels := ScopeOf(resolve())
	conn.TargetLabels = map[string]string{"a": "Renamed"}
	cfg.Connections["conn"] = conn
	changed := ScopeOf(resolve())
	if !reflect.DeepEqual(before, withLabels) || !reflect.DeepEqual(before, changed) {
		t.Errorf("a label changed the scope: %+v, %+v, %+v", before, withLabels, changed)
	}
	if vault.Fingerprint(before, "id-1") != vault.Fingerprint(changed, "id-1") {
		t.Error("a label changed the fingerprint")
	}
}
