package matomo

import (
	"net/http"
	"strings"
	"testing"
)

func TestTrackingContentFormBodies(t *testing.T) {
	for _, tc := range []struct {
		name, id, arguments string
		want                map[string]string
		absent              []string
	}{
		{"impression", "matomo.tracking.record-content-impression",
			`{"idSite":1,"url":"https://example.org/","contentName":"Summer banner","contentPiece":"/img/s.png","contentTarget":"https://example.org/sale"}`,
			map[string]string{"c_n": "Summer banner", "c_p": "/img/s.png", "c_t": "https://example.org/sale"}, []string{"c_i"}},
		{"minimal impression", "matomo.tracking.record-content-impression",
			`{"idSite":1,"url":"https://example.org/","contentName":"n"}`,
			map[string]string{"c_n": "n"}, []string{"c_i", "c_p", "c_t"}},
		{"interaction", "matomo.tracking.record-content-interaction",
			`{"idSite":1,"url":"https://example.org/","interaction":"click","contentName":"n","contentPiece":"p","contentTarget":"t"}`,
			map[string]string{"c_i": "click", "c_n": "n", "c_p": "p", "c_t": "t"}, nil},
		{"minimal interaction", "matomo.tracking.record-content-interaction",
			`{"idSite":1,"url":"https://example.org/","interaction":"click","contentName":"n"}`,
			map[string]string{"c_i": "click", "c_n": "n"}, []string{"c_p", "c_t"}},
	} {
		var calls []call
		env := newTrackingEnvironment(t, &calls, noContent)
		if _, err := env.invoke(tc.id, "trk", tc.arguments); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatalf("%s without confirm: %v", tc.name, err)
		}
		if _, err := env.change(tc.id, "trk", tc.arguments); err != nil || len(calls) != 1 {
			t.Fatalf("%s: err = %v, calls = %d", tc.name, err, len(calls))
		}
		c := calls[0]
		if c.method != http.MethodPost || c.rawQuery != "" || c.auth != "" || c.path != "/matomo/matomo.php" {
			t.Errorf("%s: request = %+v", tc.name, c)
		}
		for name, value := range tc.want {
			if !c.form.Has(name) || c.form.Get(name) != value {
				t.Errorf("%s: %s = %q, want %q", tc.name, name, c.form.Get(name), value)
			}
		}
		for _, name := range append(tc.absent, "token_auth", "cip", "cdt") {
			if c.form.Has(name) {
				t.Errorf("%s sent %s", tc.name, name)
			}
		}
		if strings.Contains(c.path+c.rawQuery+c.form.Encode(), tokenValue) || *env.reads != 0 {
			t.Errorf("%s leaked or read the token", tc.name)
		}
	}
}

func TestTrackingContentRefusesInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newTrackingEnvironment(t, &calls, noContent)
	const base = `"idSite":1,"url":"https://example.org/x"`
	for id, cases := range map[string]map[string]string{
		"matomo.tracking.record-content-impression": {
			"missing name": `{` + base + `}`,
			"blank name":   `{` + base + `,"contentName":"  "}`,
			"long name":    `{` + base + `,"contentName":"` + strings.Repeat("a", 513) + `"}`,
			"blank piece":  `{` + base + `,"contentName":"n","contentPiece":" "}`,
			"long piece":   `{` + base + `,"contentName":"n","contentPiece":"` + strings.Repeat("a", 513) + `"}`,
			"blank target": `{` + base + `,"contentName":"n","contentTarget":""}`,
			"long target":  `{` + base + `,"contentName":"n","contentTarget":"` + strings.Repeat("a", 2049) + `"}`,
			"interaction":  `{` + base + `,"contentName":"n","interaction":"click"}`,
			"token":        `{` + base + `,"contentName":"n","token_auth":"x"}`,
		},
		"matomo.tracking.record-content-interaction": {
			"missing interaction": `{` + base + `,"contentName":"n"}`,
			"blank interaction":   `{` + base + `,"contentName":"n","interaction":" "}`,
			"long interaction":    `{` + base + `,"contentName":"n","interaction":"` + strings.Repeat("a", 513) + `"}`,
			"missing name":        `{` + base + `,"interaction":"click"}`,
			"blank name":          `{` + base + `,"interaction":"click","contentName":""}`,
			"long target":         `{` + base + `,"interaction":"click","contentName":"n","contentTarget":"` + strings.Repeat("a", 2049) + `"}`,
		},
	} {
		for name, arguments := range cases {
			if _, err := env.change(id, "trk", arguments); err == nil {
				t.Errorf("%s %s was accepted", id, name)
			}
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, reads = %d", len(calls), *env.reads)
	}
}
