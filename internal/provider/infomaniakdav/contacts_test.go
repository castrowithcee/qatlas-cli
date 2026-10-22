package infomaniakdav

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
)

const mainPath = bookHome + "main/"

func vcardOf(lines ...string) string {
	return "BEGIN:VCARD\r\nVERSION:3.0\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCARD\r\n"
}

func contactEntry(href, etag, card string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:getetag>"` + etag +
		`"</d:getetag><card:address-data><![CDATA[` + card + `]]></card:address-data></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func contactsFake(t *testing.T, report string, get func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == "REPORT" && r.URL.Path == mainPath:
			return xmlResponse(207, report), nil
		case r.Method == "GET" && get != nil:
			return get(r)
		case r.Method == "PROPFIND":
			return davFake(t)(r)
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		return xmlResponse(404, bodyCanary), nil
	}
}

func listContactsOf(args string) (any, error) {
	return invokeContactsList(context.Background(), resolved("addressbook/main"), resolver(nil), nil, json.RawMessage(args))
}

func getContactOf(args string) (any, error) {
	return invokeContactsGet(context.Background(), resolved("addressbook/main"), resolver(nil), nil, json.RawMessage(args))
}

func TestContactsListReportsSortedContacts(t *testing.T) {
	report := multistatus(
		contactEntry(mainPath+"b.vcf", "e2", vcardOf("UID:u-b", "FN:Zed", "EMAIL;TYPE=work:zed@example.org", "NOTE:private")),
		contactEntry(mainPath+"bad.vcf", "e3", "not a vcard"),
		contactEntry(mainPath+"a.vcf", "e1", vcardOf("UID:u-a", "FN:Ada", "N:Lovelace;Ada;;;", "ORG:Analytical;Engine",
			"EMAIL:ada@example.org", "EMAIL:second@example.org")))
	calls := serve(t, contactsFake(t, report, nil))
	out, err := listContactsOf(`{"addressbook":"main"}`)
	if err != nil {
		t.Fatalf("list = %v", err)
	}
	page := out.(*ContactsPage)
	if page.Count != 2 || page.Truncated || page.Contacts[0].ID != "a.vcf" || page.Contacts[1].ID != "b.vcf" {
		t.Fatalf("page = %+v", page)
	}
	a, b := page.Contacts[0], page.Contacts[1]
	if a.Name != "Ada" || a.UID != "u-a" || a.Email != "ada@example.org" || a.Organization != "Analytical, Engine" ||
		a.ETag != "e1" || len(a.Emails) != 2 || a.StructuredName == nil || b.Note != "private" {
		t.Errorf("a = %+v, b = %+v", a, b)
	}
	last := (*calls)[len(*calls)-1]
	if last.method != "REPORT" || last.depth != "1" || !strings.Contains(last.body, "addressbook-query") {
		t.Errorf("last call = %+v", last)
	}
}

func TestContactsListCutsAtLimit(t *testing.T) {
	var entries []string
	for i := 0; i < 5; i++ {
		entries = append(entries, contactEntry(fmt.Sprintf("%sc%d.vcf", mainPath, i), "e",
			vcardOf(fmt.Sprintf("FN:N%d", i))))
	}
	serve(t, contactsFake(t, multistatus(entries...), nil))
	out, err := listContactsOf(`{"addressbook":"main","limit":2}`)
	page, _ := out.(*ContactsPage)
	if err != nil || page.Count != 2 || !page.Truncated || page.Contacts[0].ID != "c0.vcf" {
		t.Errorf("page = %+v, err = %v", page, err)
	}
}

func TestContactsRefuseOutsideAllowListAndInvalidIDWithoutRequest(t *testing.T) {
	calls := serve(t, davFake(t))
	for _, args := range []string{`{"addressbook":"hidden"}`, `{"addressbook":"../main"}`} {
		if _, err := listContactsOf(args); classOf(err) != provider.ClassPermission ||
			strings.Contains(err.Error(), "hidden") {
			t.Errorf("list %s = %v", args, err)
		}
	}
	if _, err := getContactOf(`{"addressbook":"main","id":"a/b.vcf"}`); classOf(err) != provider.ClassProviderError {
		t.Errorf("get invalid id = %v", err)
	}
	if _, err := getContactOf(`{"addressbook":"hidden","id":"a.vcf"}`); classOf(err) != provider.ClassPermission {
		t.Errorf("get hidden = %v", err)
	}
	if _, err := listContactsOf(`{"addressbook":"main","limit":201}`); err == nil {
		t.Error("limit above the maximum was accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v, want none", *calls)
	}
}

func TestContactsListRefusesForeignHrefAndLimits(t *testing.T) {
	for name, report := range map[string]string{
		"foreign":    multistatus(contactEntry(bookHome+"hidden/a.vcf", "e", vcardOf("FN:X"))),
		"nested":     multistatus(contactEntry(mainPath+"sub/a.vcf", "e", vcardOf("FN:X"))),
		"other host": multistatus(contactEntry("https://evil.example"+mainPath+"a.vcf", "e", vcardOf("FN:X"))),
		"oversize":   multistatus(contactEntry(mainPath+"a.vcf", "e", vcardOf("NOTE:"+strings.Repeat("x", 70<<10)))),
		"too many":   multistatus(strings.Repeat(contactEntry(mainPath+"a.vcf", "e", vcardOf("FN:X")), 501)),
	} {
		serve(t, contactsFake(t, report, nil))
		if _, err := listContactsOf(`{"addressbook":"main"}`); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s = %v", name, err)
		}
	}
}

func TestContactsGetReadsStructuredFieldsAndCapsThem(t *testing.T) {
	var phones, urls []string
	for i := 0; i < 25; i++ {
		phones = append(phones, fmt.Sprintf("TEL;TYPE=cell:phone-%02d", i))
		urls = append(urls, fmt.Sprintf("URL:https://example.org/%d", i))
	}
	lines := append([]string{"UID:u-a", "FN:Ada Lovelace", "N:Lovelace;Ada;Augusta;Countess;", "ORG:Engine;Dept",
		"TITLE:Programmer", "BDAY:1815-12-10", "NOTE:" + strings.Repeat("n", 5000), "PHOTO:" + strings.Repeat("A", 100),
		"EMAIL;TYPE=home,pref:ada@example.org", "ADR;TYPE=home:;;1 Main St;Zurich;ZH;8000;CH"}, phones...)
	card := vcardOf(append(lines, urls...)...)
	calls := serve(t, contactsFake(t, "", func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != mainPath+"a.vcf" {
			t.Errorf("GET %s", r.URL.Path)
		}
		resp := xmlResponse(200, card)
		resp.Header.Set("ETag", `"v7"`)
		return resp, nil
	}))
	out, err := getContactOf(`{"addressbook":"main","id":"a.vcf"}`)
	if err != nil {
		t.Fatalf("get = %v", err)
	}
	c := out.(*ContactResult).Contact
	if c.ID != "a.vcf" || c.ETag != "v7" || c.UID != "u-a" || c.Name != "Ada Lovelace" || c.Title != "Programmer" ||
		c.Birthday != "1815-12-10" || c.Organization != "Engine, Dept" || c.StructuredName.Family != "Lovelace" ||
		c.StructuredName.Given != "Ada" || len(c.Emails) != 1 || c.Emails[0].Value != "ada@example.org" ||
		len(c.Addresses) != 1 || c.Addresses[0].Street != "1 Main St" || c.Addresses[0].Locality != "Zurich" {
		t.Errorf("contact = %+v", c)
	}
	if len(c.Phones) != 20 || len(c.URLs) != 20 || len(c.Note) != 4096 ||
		strings.Join(c.Truncated, ",") != "note,phones,urls" {
		t.Errorf("caps: phones %d urls %d note %d truncated %v", len(c.Phones), len(c.URLs), len(c.Note), c.Truncated)
	}
	if strings.Contains(fmt.Sprintf("%+v", c), "AAAA") {
		t.Error("photo data was reported")
	}
	if got := (*calls)[len(*calls)-1]; got.method != "GET" || got.auth != basic() {
		t.Errorf("last call = %+v", got)
	}
}

func TestContactsErrorsCarryNoProviderText(t *testing.T) {
	for status, class := range map[int]provider.Class{401: provider.ClassAuth, 403: provider.ClassPermission,
		404: provider.ClassNotFound, 500: provider.ClassProviderError} {
		serve(t, contactsFake(t, "", func(*http.Request) (*http.Response, error) { return xmlResponse(status, bodyCanary), nil }))
		_, err := getContactOf(`{"addressbook":"main","id":"a.vcf"}`)
		if classOf(err) != class || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("status %d = %v", status, err)
		}
	}
	serve(t, contactsFake(t, "", func(*http.Request) (*http.Response, error) {
		return xmlResponse(200, "not a vcard "+bodyCanary), nil
	}))
	_, err := getContactOf(`{"addressbook":"main","id":"a.vcf"}`)
	if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("invalid card = %v", err)
	}
}

func TestContactsListWithoutNewArgumentsKeepsBody(t *testing.T) {
	calls := serve(t, contactsFake(t, multistatus(), nil))
	if _, err := listContactsOf(`{"addressbook":"main"}`); err != nil {
		t.Fatal(err)
	}
	if got := lastReport(calls).body; got != dav.AddressbookQueryBody {
		t.Errorf("body = %s", got)
	}
}

func TestContactsListSendsFiltersEscaped(t *testing.T) {
	calls := serve(t, contactsFake(t, multistatus(), nil))
	args := `{"addressbook":"main","test":"allof","property_filters":[` +
		`{"property":"FN","text":"R&D <x>","match_type":"ends-with","negate":true,"collation":"i;octet"},` +
		`{"property":"TEL","parameter":{"name":"TYPE","text":"work"}},` +
		`{"property":"NOTE","defined":false}]}`
	if _, err := listContactsOf(args); err != nil {
		t.Fatal(err)
	}
	body := lastReport(calls).body
	for _, want := range []string{
		`<card:filter test="allof">`,
		`<card:text-match collation="i;octet" match-type="ends-with" negate-condition="yes">R&amp;D &lt;x&gt;</card:text-match>`,
		`<card:param-filter name="TYPE"><card:text-match>work</card:text-match></card:param-filter>`,
		`<card:prop-filter name="NOTE"><card:is-not-defined/></card:prop-filter>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
}

func TestContactsListFilterRefusalsBeforeAnyIO(t *testing.T) {
	calls := serve(t, contactsFake(t, multistatus(), nil))
	long := strings.Repeat("a", 257)
	var nine []string
	for i := 0; i < 9; i++ {
		nine = append(nine, `{"property":"FN","text":"a"}`)
	}
	for name, filters := range map[string]string{
		"unknown property":  `"property_filters":[{"property":"X-FOO","text":"a"}]`,
		"unknown parameter": `"property_filters":[{"property":"TEL","parameter":{"name":"X","text":"a"}}]`,
		"unknown collation": `"property_filters":[{"property":"FN","text":"a","collation":"i;foo"}]`,
		"unknown match":     `"property_filters":[{"property":"FN","text":"a","match_type":"regex"}]`,
		"text too long":     `"property_filters":[{"property":"FN","text":"` + long + `"}]`,
		"empty text":        `"property_filters":[{"property":"FN","text":""}]`,
		"nine entries":      `"property_filters":[` + strings.Join(nine, ",") + `]`,
		"two conditions":    `"property_filters":[{"property":"FN","text":"a","defined":false}]`,
		"defined true":      `"property_filters":[{"property":"FN","defined":true}]`,
		"unknown test":      `"property_filters":[{"property":"FN","text":"a"}],"test":"none"`,
	} {
		t.Run(name, func(t *testing.T) {
			// A nil secret resolver proves the check precedes any secret access.
			_, err := invokeContactsList(context.Background(), resolved("addressbook/main"), nil, nil,
				json.RawMessage(`{"addressbook":"main",`+filters+`}`))
			if err == nil {
				t.Error("want refusal")
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v, want none", *calls)
	}
}

func TestContactsListCollationRefusalIsRequestError(t *testing.T) {
	serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "REPORT" {
			return xmlResponse(403, bodyCanary), nil
		}
		return davFake(t)(r)
	})
	_, err := listContactsOf(`{"addressbook":"main","property_filters":[{"property":"FN","text":"a","collation":"i;octet"}]}`)
	if classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), "canary") {
		t.Errorf("err = %v", err)
	}
	_, err = listContactsOf(`{"addressbook":"main"}`)
	if classOf(err) != provider.ClassPermission {
		t.Errorf("without collation err = %v, want permission", err)
	}
}

func TestContactsGetRefusesFilterArguments(t *testing.T) {
	calls := serve(t, davFake(t))
	_, err := invokeContactsGet(context.Background(), resolved("addressbook/main"), nil, nil,
		json.RawMessage(`{"addressbook":"main","id":"a.vcf","test":"allof"}`))
	if err == nil || len(*calls) != 0 {
		t.Errorf("err = %v, calls = %v", err, *calls)
	}
}
