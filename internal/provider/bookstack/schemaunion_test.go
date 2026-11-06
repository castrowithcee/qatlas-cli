package bookstack

import (
	"encoding/json"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

// default_template_id takes a template ID or null; the application validator must accept both and refuse a string.
func TestDefaultTemplateIDAcceptsNumberAndNullOnly(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	byID := map[string]capability.Descriptor{}
	for _, d := range reg.Provider(Provider) {
		byID[d.ID] = d
	}
	for _, id := range []string{Provider + ".books.update", Provider + ".chapters.update"} {
		d, ok := byID[id]
		if !ok {
			t.Fatalf("no descriptor %s", id)
		}
		for value, wantOK := range map[string]bool{`5`: true, `null`: true, `"5"`: false, `0`: false, `true`: false} {
			args := json.RawMessage(`{"id":3,"default_template_id":` + value + `}`)
			err := application.ValidateJSON(d.InputSchema, args)
			if (err == nil) != wantOK {
				t.Errorf("%s default_template_id=%s: err = %v, want accepted = %v", id, value, err, wantOK)
			}
		}
	}
}
