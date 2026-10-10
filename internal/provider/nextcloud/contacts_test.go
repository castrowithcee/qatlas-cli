package nextcloud

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
)

const (
	contactHome    = "/remote.php/dav/addressbooks/users/" + aliceUser + "/"
	contactForeign = "foreign-book-canary-9e41"
	contactPhoto   = "UEhPVE8tQ0FOQVJZ"
)

// contactMail builds an address from parts, so no test literal looks like a real one.
func contactMail(local string) string { return local + "@" + "example.invalid" }

func bookMultistatus(entries ...string) string {
	return `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">` +
		strings.Join(entries, "") + `</d:multistatus>`
}

func bookNode(href, name string, book bool) string {
	kind := ""
	if book {
		kind = `<card:addressbook/>`
	}
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:displayname>` + name +
		`</d:displayname><d:resourcetype><d:collection/>` + kind + `</d:resourcetype>` +
		`<card:addressbook-description>Shared people</card:addressbook-description></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func cardNode(href, etag, vcard string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:getetag>&quot;` + etag +
		`&quot;</d:getetag><card:address-data>` + xmlEscape(vcard) + `</card:address-data></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func xmlEscape(value string) string { return escapeXMLText(value) }

func vcardOf(uid, name, extra string) string {
	return "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:" + uid + "\r\nFN:" + name + "\r\nN:" + name + ";;;;\r\n" + extra +
		"END:VCARD\r\n"
}

func bookInvoke(t *testing.T, targets []string, operation, args string) (json.RawMessage, error) {
	t.Helper()
	response, err := accountClient(t, targets...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args),
	})
	if err != nil {
		return nil, err
	}
	return response.Result, nil
}

func bookListing() string {
	return bookMultistatus(
		bookNode(contactHome, "Home", false),
		bookNode(contactHome+"contacts/", "Contacts", true),
		bookNode(contactHome+"team/", "Team", true),
		bookNode(contactHome+systemAddressbook+"/", "System", true),
		bookNode(contactHome+"plain/", "Plain folder", false),
		bookNode("https://evil.example.invalid"+contactHome+"hostile/", contactForeign, true),
		bookNode(contactHome+"contacts/deeper/", contactForeign, true),
		bookNode("/remote.php/dav/addressbooks/users/bobby/theirs/", contactForeign, true),
		bookNode("/remote.php/dav/files/alice/", contactForeign, true),
	)
}

func TestAddressbooksListReportsOnlyBoundBooksOfTheHome(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookListing()), nil
	})
	result, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.addressbooks.list", `{}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var listed AddressbooksResult
	if err := json.Unmarshal(result, &listed); err != nil || listed.Count != 2 ||
		listed.Addressbooks[0].ID != "contacts" || listed.Addressbooks[1].ID != "team" ||
		listed.Addressbooks[0].ReadOnly || listed.Addressbooks[0].Description != "Shared people" {
		t.Errorf("result = %s, %v", result, err)
	}
	for _, canary := range []string{contactForeign, systemAddressbook, "Plain folder"} {
		if strings.Contains(string(result), canary) {
			t.Errorf("the result carries %q: %s", canary, result)
		}
	}
	request := (*calls)[0]
	if len(*calls) != 1 || request.method != "PROPFIND" || request.depth != depthChildren ||
		request.url.Path != contactHome || request.auth == "" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestSystemAddressbookNeedsExplicitBindingAndIsReadOnly(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookListing()), nil
	})
	result, err := bookInvoke(t, []string{"addressbook/" + systemAddressbook}, "nextcloud.addressbooks.list", `{}`)
	var listed AddressbooksResult
	if err != nil || json.Unmarshal(result, &listed) != nil || listed.Count != 1 ||
		listed.Addressbooks[0].ID != systemAddressbook || !listed.Addressbooks[0].ReadOnly {
		t.Fatalf("result = %s, %v", result, err)
	}

	// The kind alone never reaches it, however the address book is addressed.
	refuse(t)
	for operation, args := range map[string]string{
		"nextcloud.contacts.list": `{"addressbook":"` + systemAddressbook + `"}`,
		"nextcloud.contacts.get":  `{"addressbook":"` + systemAddressbook + `","id":"x.vcf"}`,
	} {
		_, err := bookInvoke(t, []string{"addressbook"}, operation, args)
		if err == nil || !strings.Contains(err.Error(), messageAddressbookNone) {
			t.Errorf("%s err = %v, want the refusal", operation, err)
		}
	}
}

func TestUnboundAddressbookIsRefusedBeforeAnyRequestWithoutNamingIt(t *testing.T) {
	refuse(t)
	cases := []struct{ targets []string }{{[]string{"addressbook/contacts"}}, {[]string{"account"}}}
	for _, tc := range cases {
		for _, call := range []struct{ operation, args string }{
			{"nextcloud.contacts.list", `{"addressbook":"` + contactForeign + `"}`},
			{"nextcloud.contacts.get", `{"addressbook":"` + contactForeign + `","id":"x.vcf"}`},
			{"nextcloud.addressbooks.list", `{}`},
		} {
			if call.operation == "nextcloud.addressbooks.list" && tc.targets[0] == "addressbook/contacts" {
				continue
			}
			_, err := bookInvoke(t, tc.targets, call.operation, call.args)
			if err == nil || strings.Contains(err.Error(), contactForeign) {
				t.Errorf("%v %s err = %v, want a refusal that does not name the address book", tc.targets, call.operation, err)
			}
		}
	}
}

func TestContactsListEscapesTheSearchTextAndNeverAsksForPhotos(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(
			cardNode(contactHome+"contacts/ada.vcf", "e1", vcardOf("u-1", "Ada Lovelace",
				"EMAIL;TYPE=work:"+contactMail("ada")+"\r\nPHOTO;ENCODING=b;TYPE=JPEG:"+contactPhoto+"\r\n")),
		)), nil
	})
	result, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.list",
		`{"addressbook":"contacts","query":"a<b & \"c\""}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	request := (*calls)[0]
	if request.method != "REPORT" || request.depth != depthChildren || request.url.Path != contactHome+"contacts/" {
		t.Errorf("request = %+v", request)
	}
	if strings.Contains(request.body, "a<b") || !strings.Contains(request.body, "a&lt;b &amp; &#34;c&#34;") {
		t.Errorf("the search text is not escaped: %s", request.body)
	}
	if strings.Contains(request.body, "PHOTO") || strings.Count(request.body, "<card:text-match") != 2 ||
		!strings.Contains(request.body, `name="FN"`) || !strings.Contains(request.body, `name="EMAIL"`) {
		t.Errorf("body = %s", request.body)
	}
	decoder := xml.NewDecoder(strings.NewReader(request.body))
	for {
		if _, err := decoder.Token(); err != nil {
			if err != io.EOF {
				t.Errorf("the request body is not well-formed XML: %v", err)
			}
			break
		}
	}
	var page ContactsPage
	if err := json.Unmarshal(result, &page); err != nil || page.Count != 1 || page.Contacts[0].ID != "ada.vcf" ||
		page.Contacts[0].Name != "Ada Lovelace" || page.Contacts[0].Email != contactMail("ada") ||
		page.Contacts[0].ETag == "" {
		t.Errorf("result = %s, %v", result, err)
	}
	if strings.Contains(string(result), contactPhoto) || strings.Contains(strings.ToLower(string(result)), "photo") {
		t.Errorf("a photo was reported: %s", result)
	}
}

func TestContactsListWithoutQuerySendsNoFilterAndRejectsBadQueries(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus()), nil
	})
	if _, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.list", `{"addressbook":"contacts"}`); err != nil {
		t.Fatalf("invoke = %v", err)
	}
	if strings.Contains((*calls)[0].body, "filter") {
		t.Errorf("body = %s", (*calls)[0].body)
	}
	for _, query := range []string{`"  "`, `"a\u0000b"`, `"` + strings.Repeat("x", maxContactQuery+1) + `"`} {
		before := len(*calls)
		_, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.list",
			`{"addressbook":"contacts","query":`+query+`}`)
		if err == nil || len(*calls) != before {
			t.Errorf("query %.20s err = %v, calls = %d", query, err, len(*calls)-before)
		}
	}
}

func TestContactsListDropsForeignHrefsAndCapsTheResult(t *testing.T) {
	card := func(uid, name string) string { return vcardOf(uid, name, "") }
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(
			cardNode(contactHome+"contacts/b.vcf", "e2", card("u-b", "Bea")),
			cardNode(contactHome+"contacts/a.vcf", "e1", card("u-a", "Abe")),
			cardNode(contactHome+"contacts/c.vcf", "e3", card("u-c", "Cy")),
			cardNode(contactHome+"contacts/c.vcf", "e3", card("u-c", "Cy")),
			cardNode("https://evil.example.invalid"+contactHome+"contacts/h.vcf", "e4", card("u-h", contactForeign)),
			cardNode(contactHome+"contacts/sub/deep.vcf", "e5", card("u-d", contactForeign)),
			cardNode(contactHome+"team/o.vcf", "e6", card("u-o", contactForeign)),
			cardNode("/remote.php/dav/addressbooks/users/bobby/contacts/p.vcf", "e7", card("u-p", contactForeign)),
			cardNode(contactHome+"contacts/bad.vcf", "e8", "not a card"),
		)), nil
	})
	result, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.list", `{"addressbook":"contacts","limit":2}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var page ContactsPage
	if err := json.Unmarshal(result, &page); err != nil || page.Count != 2 || !page.Truncated ||
		page.Contacts[0].Name != "Abe" || page.Contacts[1].Name != "Bea" {
		t.Errorf("result = %s, %v", result, err)
	}
	if strings.Contains(string(result), contactForeign) {
		t.Errorf("a foreign node was reported: %s", result)
	}
	if _, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.list",
		`{"addressbook":"contacts","limit":201}`); err == nil {
		t.Error("a limit above the maximum was accepted")
	}
}

func TestContactsGetReadsOneCardThroughAFixedMultiget(t *testing.T) {
	long := strings.Repeat("n", dav.MaxContactNote+10)
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(
			cardNode(contactHome+"contacts/ada%20l.vcf", "e1", vcardOf("u-1", "Ada Lovelace",
				"EMAIL;TYPE=work:"+contactMail("ada")+"\r\nTEL;TYPE=cell:+100\r\nNOTE:"+long+
					"\r\nPHOTO;ENCODING=b;TYPE=JPEG:"+contactPhoto+"\r\n")),
		)), nil
	})
	result, err := bookInvoke(t, []string{"addressbook/contacts"}, "nextcloud.contacts.get",
		`{"addressbook":"contacts","id":"ada l.vcf"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got ContactResult
	if err := json.Unmarshal(result, &got); err != nil || got.Contact.ID != "ada l.vcf" ||
		len(got.Contact.Emails) != 1 || len(got.Contact.Phones) != 1 ||
		len(got.Contact.Truncated) != 1 || got.Contact.Truncated[0] != "note" || len(got.Contact.Note) > dav.MaxContactNote {
		t.Errorf("result = %s, %v", result, err)
	}
	if strings.Contains(string(result), contactPhoto) {
		t.Errorf("a photo was reported: %s", result)
	}
	request := (*calls)[0]
	if request.method != "REPORT" || request.url.Path != contactHome+"contacts/" ||
		!strings.Contains(request.body, "<d:href>"+contactHome+"contacts/ada%20l.vcf</d:href>") ||
		strings.Contains(request.body, "PHOTO") || !strings.Contains(request.body, "addressbook-multiget") {
		t.Errorf("request = %+v", request)
	}
}

func TestContactsGetRefusesUnusableIDsAndMissingCards(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(
			`<d:response><d:href>`+contactHome+`contacts/gone.vcf</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>`,
		)), nil
	})
	for _, id := range []string{"../x", "a/b", "%2e", ""} {
		_, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.get", `{"addressbook":"contacts","id":"`+id+`"}`)
		if err == nil {
			t.Errorf("id %q was accepted", id)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests for unusable ids: %+v", *calls)
	}
	_, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.get", `{"addressbook":"contacts","id":"gone.vcf"}`)
	if err == nil || !strings.Contains(err.Error(), messageNotFound) {
		t.Errorf("err = %v, want not found", err)
	}
}

func TestContactReadsTreatARedirectAsAClearErrorWithoutItsTarget(t *testing.T) {
	const canary = "https://redirect-target.example.invalid/canary-3c7d"
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		response := xmlResponse(http.StatusFound, bodyCanary)
		response.Header.Set("Location", canary)
		return response, nil
	})
	for _, call := range []struct{ operation, args string }{
		{"nextcloud.addressbooks.list", `{}`},
		{"nextcloud.contacts.list", `{"addressbook":"contacts"}`},
		{"nextcloud.contacts.get", `{"addressbook":"contacts","id":"a.vcf"}`},
	} {
		_, err := bookInvoke(t, []string{"addressbook"}, call.operation, call.args)
		if err == nil || !strings.Contains(err.Error(), messageRedirect) ||
			strings.Contains(err.Error(), "canary") {
			t.Errorf("%s err = %v", call.operation, err)
		}
	}
	if len(*calls) != 3 {
		t.Errorf("calls = %d, want no followed redirect", len(*calls))
	}
}

func TestContactsProfileHoldsExactlyTheReadTools(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if profile.ID != "contacts" {
			for _, id := range profile.Tools {
				if strings.HasPrefix(id, "nextcloud.contacts.") || id == addressbooksList.ID {
					t.Errorf("profile %s offers %s", profile.ID, id)
				}
			}
			continue
		}
		want := []string{addressbooksList.ID, contactsList.ID, contactsGet.ID}
		if strings.Join(profile.Tools, ",") != strings.Join(want, ",") {
			t.Errorf("contacts tools = %v", profile.Tools)
		}
		return
	}
	t.Error("no contacts profile")
}
