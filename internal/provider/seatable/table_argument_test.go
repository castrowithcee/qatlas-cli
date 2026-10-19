package seatable

import (
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

func TestTableArgumentsShareOneDescription(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, d := range reg.Provider(Provider) {
		for _, a := range d.Arguments {
			if a.Name != "table" || a.Required {
				continue
			}
			seen++
			if a.Description != tableArgumentDescription {
				t.Errorf("%s table description = %q", d.ID, a.Description)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no table arguments found")
	}
}
