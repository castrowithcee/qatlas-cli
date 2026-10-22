package dav

import (
	"strings"
	"testing"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }

func TestContactQueryBodyWithoutFilterIsUnchanged(t *testing.T) {
	body, err := ContactQueryBody("op", ContactFilter{})
	if err != nil || body != AddressbookQueryBody {
		t.Errorf("body = %q, err = %v", body, err)
	}
}

func TestContactQueryBodyForms(t *testing.T) {
	f := ContactFilter{Test: "allof", Properties: []ContactPropertyFilter{
		{Property: "FN", Text: strp("R&D <x>"), MatchType: "starts-with", Negate: true, Collation: "i;octet"},
		{Property: "EMAIL", Text: strp("example.org"), Parameter: &ContactParamFilter{Name: "TYPE", Text: strp("work")}},
		{Property: "NOTE", Defined: boolp(false)},
		{Property: "TEL", Parameter: &ContactParamFilter{Name: "PREF", Defined: boolp(false)}},
	}}
	body, err := ContactQueryBody("op", f)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<card:filter test="allof">`,
		`<card:prop-filter name="FN"><card:text-match collation="i;octet" match-type="starts-with" negate-condition="yes">R&amp;D &lt;x&gt;</card:text-match></card:prop-filter>`,
		`<card:prop-filter name="EMAIL"><card:text-match>example.org</card:text-match><card:param-filter name="TYPE"><card:text-match>work</card:text-match></card:param-filter></card:prop-filter>`,
		`<card:prop-filter name="NOTE"><card:is-not-defined/></card:prop-filter>`,
		`<card:prop-filter name="TEL"><card:param-filter name="PREF"><card:is-not-defined/></card:param-filter></card:prop-filter>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
	if strings.Contains(body, "nresults") || strings.Contains(body, "limit") {
		t.Errorf("body limits the server answer: %s", body)
	}
	anyof, _ := ContactQueryBody("op", ContactFilter{Test: "anyof", Properties: f.Properties[:1]})
	if !strings.Contains(anyof, `<card:filter>`) {
		t.Errorf("anyof body = %s", anyof)
	}
}

func TestContactQueryBodyRefusals(t *testing.T) {
	var nine []ContactPropertyFilter
	for i := 0; i < 9; i++ {
		nine = append(nine, ContactPropertyFilter{Property: "FN", Text: strp("a")})
	}
	one := func(p ContactPropertyFilter) ContactFilter {
		return ContactFilter{Properties: []ContactPropertyFilter{p}}
	}
	for name, f := range map[string]ContactFilter{
		"unknown property":   one(ContactPropertyFilter{Property: "X-FOO", Text: strp("a")}),
		"unknown parameter":  one(ContactPropertyFilter{Property: "TEL", Parameter: &ContactParamFilter{Name: "X", Text: strp("a")}}),
		"unknown collation":  one(ContactPropertyFilter{Property: "FN", Text: strp("a"), Collation: "i;foo"}),
		"unknown match":      one(ContactPropertyFilter{Property: "FN", Text: strp("a"), MatchType: "regex"}),
		"text too long":      one(ContactPropertyFilter{Property: "FN", Text: strp(strings.Repeat("a", 257))}),
		"empty text":         one(ContactPropertyFilter{Property: "FN", Text: strp("")}),
		"nine entries":       {Properties: nine},
		"two conditions":     one(ContactPropertyFilter{Property: "FN", Text: strp("a"), Defined: boolp(false)}),
		"defined true":       one(ContactPropertyFilter{Property: "FN", Defined: boolp(true)}),
		"unknown test":       {Test: "none", Properties: nine[:1]},
		"test alone":         {Test: "allof"},
		"param two":          one(ContactPropertyFilter{Property: "TEL", Parameter: &ContactParamFilter{Name: "TYPE", Text: strp("a"), Defined: boolp(false)}}),
		"param defined true": one(ContactPropertyFilter{Property: "TEL", Parameter: &ContactParamFilter{Name: "TYPE", Defined: boolp(true)}}),
	} {
		if _, err := ContactQueryBody("op", f); err == nil {
			t.Errorf("%s: want refusal", name)
		}
	}
}
