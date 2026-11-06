package matomo

import (
	"encoding/json"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
)

// The input schema requires at least one of idFunnel and idGoal; a call with both passes the schema and
// is refused by the handler, so exactly one of them reaches Matomo.
func TestFunnelsGetTakesExactlyOneOfIDFunnelAndIDGoal(t *testing.T) {
	d := funnelsGet
	for args, wantOK := range map[string]bool{
		`{"idSite":1,"idFunnel":7}`:            true,
		`{"idSite":1,"idGoal":0}`:              true,
		`{"idSite":1}`:                         false,
		`{"idSite":1,"idFunnel":7,"idGoal":0}`: true,
	} {
		if err := application.ValidateJSON(d.InputSchema, json.RawMessage(args)); (err == nil) != wantOK {
			t.Errorf("schema, %s: err = %v, want accepted = %v", args, err, wantOK)
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, funnelAnswers(nil).handler(t))
	for _, args := range []string{`{"idSite":1}`, `{"idSite":1,"idFunnel":7,"idGoal":0}`} {
		if _, err := env.invoke("matomo.funnels.get", "two", args); err == nil {
			t.Errorf("invoke accepted %s", args)
		}
	}
	for _, args := range []string{`{"idSite":1,"idFunnel":7}`, `{"idSite":1,"idGoal":4}`} {
		if _, err := env.invoke("matomo.funnels.get", "two", args); err != nil {
			t.Errorf("invoke refused %s: %v", args, err)
		}
	}
}
