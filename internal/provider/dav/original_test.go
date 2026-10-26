package dav

import (
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestErrorOriginal(t *testing.T) {
	sabre := `<?xml version="1.0" encoding="utf-8"?>
<d:error xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns">
  <s:sabredav-version>4.6.0</s:sabredav-version>
  <s:exception>Sabre\DAV\Exception\NotFound</s:exception>
  <s:message>File with name x could not be located</s:message>
  <s:file>/var/www/secret.php</s:file><s:line>12</s:line>
</d:error>`
	cases := []struct {
		name string
		body string
		want provider.Original
	}{
		{"rfc4918", `<D:error xmlns:D="DAV:"><D:lock-token-submitted><D:href>/x</D:href></D:lock-token-submitted>` +
			`<D:responsedescription> locked </D:responsedescription></D:error>`,
			provider.Original{Status: 423, Entries: []provider.OriginalEntry{
				{Type: "lock-token-submitted"}, {Message: "locked"}}}},
		{"sabredav", sabre, provider.Original{Status: 423, Entries: []provider.OriginalEntry{
			{Type: `Sabre\DAV\Exception\NotFound`, Message: "File with name x could not be located"}}}},
		{"other prefix", `<x:error xmlns:x="DAV:" xmlns:q="http://sabredav.org/ns"><q:exception>E</q:exception></x:error>`,
			provider.Original{Status: 423, Entries: []provider.OriginalEntry{{Type: "E"}}}},
		{"text", " gateway down \n", provider.Original{Status: 423, Entries: []provider.OriginalEntry{
			{Message: "gateway down"}}}},
		{"empty", "", provider.Original{Status: 423}},
		{"other root", `<html><body>oops</body></html>`, provider.Original{Status: 423,
			Entries: []provider.OriginalEntry{{Message: `<html><body>oops</body></html>`}}}},
		{"wrong namespace", `<error xmlns="urn:x"><a/></error>`, provider.Original{Status: 423,
			Entries: []provider.OriginalEntry{{Message: `<error xmlns="urn:x"><a/></error>`}}}},
		{"empty error", `<d:error xmlns:d="DAV:"/>`, provider.Original{Status: 423}},
	}
	for _, c := range cases {
		got := ErrorOriginal(423, strings.NewReader(c.body))
		if !reflect.DeepEqual(*got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, *got, c.want)
		}
	}
}

func TestErrorOriginalLimit(t *testing.T) {
	long := "<d:error xmlns:d=\"DAV:\"><d:responsedescription>" + strings.Repeat("a", 10000)
	got := ErrorOriginal(500, strings.NewReader(long))
	if got.Status != 500 || len(got.Entries) != 0 {
		t.Fatalf("truncated DAV:error: %+v", got)
	}
	text := ErrorOriginal(500, strings.NewReader(strings.Repeat("b", 10000)))
	if len(text.Entries) != 1 || len(text.Entries[0].Message) != provider.ErrorBodyLimit {
		t.Fatalf("long text not limited: %d entries", len(text.Entries))
	}
	if ErrorOriginal(500, nil).Status != 500 {
		t.Fatal("nil body")
	}
}

func TestErrorOriginalNoEntities(t *testing.T) {
	body := `<!DOCTYPE d [<!ENTITY x SYSTEM "file:///etc/passwd">]><d:error xmlns:d="DAV:">` +
		`<d:responsedescription>&x;</d:responsedescription></d:error>`
	for _, e := range ErrorOriginal(400, strings.NewReader(body)).Entries {
		if strings.Contains(e.Message, "root:") {
			t.Fatalf("entity resolved: %q", e.Message)
		}
	}
}
