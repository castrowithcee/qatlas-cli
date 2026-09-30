package capability

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegisterValidatesImpliedFields(t *testing.T) {
	base := Descriptor{
		ID: "faketracker.issues.list", Version: 1, Description: "List issues", Risk: testRisk, Provider: "faketracker",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"state":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"issues":{"type":"array",` +
			`"items":{"type":"object","properties":{"state":{"type":"string"}}}}}}`),
	}
	ok := ImpliedField{List: "issues", Argument: "state", Values: []string{"open"}, Field: "state"}
	tests := []struct {
		name    string
		implied ImpliedField
		wantIn  string
	}{
		{"valid", ok, ""},
		{"unknown argument", ImpliedField{List: "issues", Argument: "nope", Values: ok.Values, Field: "state"}, "unknown argument"},
		{"unknown field", ImpliedField{List: "issues", Argument: "state", Values: ok.Values, Field: "nope"}, "not a member"},
		{"unknown list", ImpliedField{List: "nope", Argument: "state", Values: ok.Values, Field: "state"}, "unknown list"},
		{"no values", ImpliedField{List: "issues", Argument: "state", Field: "state"}, "no value"},
	}
	for _, tt := range tests {
		d := base
		d.ImpliedFields = []ImpliedField{tt.implied}
		err := NewRegistry().Register("faketracker", operation(d))
		if tt.wantIn == "" && err != nil || tt.wantIn != "" && (err == nil || !strings.Contains(err.Error(), tt.wantIn)) {
			t.Errorf("%s: Register = %v, want %q", tt.name, err, tt.wantIn)
		}
	}
}
