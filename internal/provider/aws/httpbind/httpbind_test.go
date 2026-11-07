package httpbind

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseURI(t *testing.T) {
	good := map[string]int{"/": 0, "/a": 1, "/v2/email/identities/{EmailIdentity}": 4, "/a/{K+}": 2, "/x?x-id=Get&k": 1}
	for uri, segs := range good {
		p, err := parseURI(uri)
		if err != nil || len(p.segs) != segs {
			t.Errorf("%s: segs = %d, err = %v", uri, len(p.segs), err)
		}
	}
	for _, uri := range []string{"", "a", "//", "/a//b", "/a/", "/./a", "/a/..", "/{}", "/{A+}/b", "/{A}{B}", "/a{B}", "/{1A}", "/a b", "/a?x=1&x=2", "/a?=1", "/%2e"} {
		if _, err := parseURI(uri); err == nil {
			t.Errorf("%q was accepted", uri)
		}
	}
}

func TestEscape(t *testing.T) {
	for in, want := range map[string]string{
		"abcXYZ019-._~": "abcXYZ019-._~", "a b": "a%20b", "a/b": "a%2Fb", "a+b": "a%2Bb", "é": "%C3%A9",
		"a=b&c": "a%3Db%26c", "%41": "%2541", "'()*!": "%27%28%29%2A%21", "": "",
	} {
		if got := Escape(in); got != want {
			t.Errorf("Escape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListQuotingRoundTrips(t *testing.T) {
	for _, items := range [][]string{{"a"}, {"a", "b"}, {"a,b", "c"}, {`q"r`, `\`}, {" lead", "trail "}, {"", "x"}, {"Mon, 06 May 2024 07:08:09 GMT"}} {
		quoted := make([]string, len(items))
		for i, item := range items {
			quoted[i] = quoteListValue(item)
		}
		got, ok := splitList(strings.Join(quoted, ","))
		if !ok || !reflect.DeepEqual(got, items) {
			t.Errorf("%q -> %q -> %q (%v)", items, quoted, got, ok)
		}
	}
	for _, bad := range []string{`"open`, `"a"b`, `a"b`, `"a" "b"`} {
		if _, ok := splitList(bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
	if got, ok := splitList("  "); !ok || len(got) != 0 {
		t.Errorf("an empty line is an empty list, got %q", got)
	}
}

func TestTimestampText(t *testing.T) {
	at := time.Date(2024, 5, 6, 7, 8, 9, 120_000_000, time.UTC)
	whole := at.Truncate(time.Second)
	for _, c := range []struct {
		t    time.Time
		f    Format
		want string
		ok   bool
	}{
		{at, FormatEpochSeconds, "1714979289.12", true},
		{whole, FormatEpochSeconds, "1714979289", true},
		{at, FormatDateTime, "2024-05-06T07:08:09.12Z", true},
		{whole, FormatDateTime, "2024-05-06T07:08:09Z", true},
		{whole, FormatHTTPDate, "Mon, 06 May 2024 07:08:09 GMT", true},
		{at, FormatHTTPDate, "", false},
		{time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC), FormatEpochSeconds, "", false},
		{at, FormatDefault, "", false},
	} {
		got, ok := FormatTimestamp(c.t, c.f)
		if got != c.want || ok != c.ok {
			t.Errorf("FormatTimestamp(%v, %d) = %q, %v; want %q, %v", c.t, c.f, got, ok, c.want, c.ok)
		}
		if !c.ok {
			continue
		}
		parsed, cut, err := ParseText(&Shape{Kind: KindTimestamp, Format: c.f}, got, FormatDefault)
		if err != nil || cut || parsed != c.t.UTC().Format(time.RFC3339Nano) {
			t.Errorf("ParseText(%q) = %v, %v, %v", got, parsed, cut, err)
		}
	}
	for _, text := range []string{"", "1e", "-1", "+1", "01", "1.1234567890", "1.-5", "99999999999999", "x", "1 "} {
		if _, _, err := ParseText(&Shape{Kind: KindTimestamp, Format: FormatEpochSeconds}, text, 0); err == nil {
			t.Errorf("epoch %q was accepted", text)
		}
	}
	// A date-time with an offset is normalized to UTC; http-date also reads the obsolete forms.
	if v, _, err := ParseText(&Shape{Kind: KindTimestamp}, "2024-05-06T09:08:09+02:00", FormatDateTime); err != nil || v != "2024-05-06T07:08:09Z" {
		t.Errorf("offset: %v %v", v, err)
	}
	if v, _, err := ParseText(&Shape{Kind: KindTimestamp}, "Monday, 06-May-24 07:08:09 GMT", FormatHTTPDate); err != nil || v != "2024-05-06T07:08:09Z" {
		t.Errorf("rfc850: %v %v", v, err)
	}
}

func TestParseTextRefusesWhatDoesNotParse(t *testing.T) {
	for kind, texts := range map[Kind][]string{
		KindInteger: {"", "1.5", "x", "99999999999999999999"}, KindFloat: {"", "NaN", "Infinity", "-Infinity", "x"},
		KindBoolean: {"", "True", "1"}, KindBlob: {"not base64!", "a"},
	} {
		for _, text := range texts {
			if _, _, err := ParseText(&Shape{Kind: kind}, text, 0); err == nil {
				t.Errorf("kind %d accepted %q", kind, text)
			}
		}
	}
	if _, _, err := ParseText(&Shape{Kind: KindString}, "\xff", 0); err == nil {
		t.Error("invalid UTF-8 was accepted")
	}
}

func TestPrefixedHeadersAreCapped(t *testing.T) {
	op := &Operation{Method: "GET", URI: "/x", Output: &Shape{Kind: KindStruct, Fields: []Field{
		{Key: "m", Name: "X-M-", Loc: LocPrefixHeaders, Shape: &Shape{Kind: KindMap, Elem: &Shape{Kind: KindString}, MaxItems: 2}}}}}
	h := map[string][]string{"X-M-A": {"1"}, "X-M-B": {"2"}, "X-M-C": {"3"}}
	if _, _, err := op.Decode(nil, 200, h, nil); err == nil {
		t.Error("more prefixed headers than the cap were accepted")
	}
}
