package nextcloud

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	contactBookPath = contactHome + "contacts/"
	contactItemPath = contactBookPath + "ada.vcf"
	contactHint     = "may have been applied"

	contactCreateArgs = `{"addressbook":"contacts","name":"Ada Example","structured_name":{"family":"Example","given":"Ada"},` +
		`"emails":[{"value":"ada@example.org","type":"work"},{"value":"a2@example.org"}],` +
		`"phones":[{"value":"+1 (2) 3.4-5/6","type":"cell"}],` +
		`"addresses":[{"type":"home","street":"Street 1","locality":"Town"}],"organization":"Org","title":"Dev",` +
		`"birthday":"1990-02-03","note":"a\nb, c; d","urls":["https://example.org/a"]}`
	contactUpdateArgs = `{"addressbook":"contacts","id":"ada.vcf","etag":"e1","name":"Ada Renamed"}`
	contactDeleteArgs = `{"addressbook":"contacts","id":"ada.vcf","etag":"e1"}`

	// contactGolden is the card of contactCreateArgs with the generated UID replaced by a marker.
	contactGolden = "BEGIN:VCARD\r\nVERSION:3.0\r\nADR;TYPE=home:;;Street 1;Town;;;\r\n" +
		"BDAY:1990-02-03\r\nEMAIL;TYPE=work:ada@example.org\r\nEMAIL:a2@example.org\r\nFN:Ada Example\r\n" +
		"N:Example;Ada;;;\r\nNOTE:a\\nb\\, c; d\r\nORG:Org\r\nTEL;TYPE=cell:+1 (2) 3.4-5/6\r\nTITLE:Dev\r\n" +
		"UID:UID-MARKER\r\nURL:https://example.org/a\r\nEND:VCARD\r\n"
)

var contactTools = []string{addressbooksList.ID, contactsList.ID, contactsGet.ID, contactsCreate.ID, contactsUpdate.ID,
	contactsDelete.ID}

func contactWrite(t *testing.T, tools, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	return eventWrite(t, tools, targets, operation, args, confirm)
}

func contactBookAnswer(privileges []string) *http.Response {
	priv := ""
	if privileges != nil {
		priv = privilegeXML(privileges...)
	}
	return xmlResponse(http.StatusMultiStatus, bookMultistatus(`<d:response><d:href>`+contactBookPath+`</d:href>`+
		`<d:propstat><d:prop><d:resourcetype><d:collection/><card:addressbook/></d:resourcetype>`+priv+`</d:prop>`+
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`))
}

func contactGetAnswer(etag, data string) *http.Response {
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/vcard"}},
		Body: io.NopCloser(strings.NewReader(data))}
	if etag != "" {
		response.Header.Set("ETag", `"`+etag+`"`)
	}
	return response
}

// contactFixture answers the pre-check of each tool with a writable address book or a plain card and the
// mutation with mutate, and records the headers of the mutation.
func contactFixture(t *testing.T, stored string, mutate func() (*http.Response, error)) (*[]call, *http.Header) {
	t.Helper()
	headers := &http.Header{}
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == methodPropfind && r.URL.Path == contactBookPath:
			return contactBookAnswer(fullRights), nil
		case r.Method == http.MethodGet && r.URL.Path == contactItemPath:
			return contactGetAnswer("e1", stored), nil
		}
		*headers = r.Header.Clone()
		return mutate()
	})
	return calls, headers
}

var plainCard = vcardOf("u-keep", "Ada", "")

func TestContactsCreatePutsOneNewCardWithIfNoneMatch(t *testing.T) {
	calls, headers := contactFixture(t, plainCard, func() (*http.Response, error) {
		return eventChanged(http.StatusCreated, "n1"), nil
	})
	result, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts.create", contactCreateArgs, true)
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	var got ContactWriteResult
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Created || got.Addressbook != "contacts" || got.ID != got.UID+".vcf" || len(got.UID) != 36 || got.ETag != "n1" {
		t.Errorf("result = %s", result)
	}
	if eventMethods(calls) != "PROPFIND PUT" {
		t.Fatalf("calls = %+v", *calls)
	}
	probe, put := (*calls)[0], (*calls)[1]
	if probe.url.Path != contactBookPath || probe.depth != depthSelf || !strings.Contains(probe.body, "current-user-privilege-set") {
		t.Errorf("pre-check = %+v", probe)
	}
	if put.url.Path != contactBookPath+got.ID || headers.Get("If-None-Match") != "*" || headers.Get("If-Match") != "" ||
		!strings.HasPrefix(headers.Get("Content-Type"), "text/vcard") {
		t.Errorf("put = %+v, headers = %v", put, *headers)
	}
	if want, have := contactGolden, strings.Replace(put.body, got.UID, "UID-MARKER", 1); want != have {
		t.Errorf("card:\n%q\nwant:\n%q", have, want)
	}
	if _, err := vcard.NewDecoder(strings.NewReader(put.body)).Decode(); err != nil {
		t.Errorf("card is no vCard: %v", err)
	}
}

func TestContactsUpdateReplacesBoundToTheETagAndKeepsTheUID(t *testing.T) {
	stored := vcardOf("u-keep", "Ada", "PRODID:-//x//EN\r\nREV:20200101T000000Z\r\nTEL:123\r\n")
	calls, headers := contactFixture(t, stored, func() (*http.Response, error) { return eventChanged(http.StatusNoContent, "e2"), nil })
	result, err := contactWrite(t, contactTools, []string{"addressbook/contacts"}, "nextcloud.contacts.contacts.update", contactUpdateArgs, true)
	var got ContactWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil ||
		got != (ContactWriteResult{Addressbook: "contacts", ID: "ada.vcf", UID: "u-keep", ETag: "e2", Updated: true}) {
		t.Fatalf("update = %s, %v", result, err)
	}
	if eventMethods(calls) != "GET PUT" || (*calls)[1].url.Path != contactItemPath || headers.Get("If-Match") != `"e1"` ||
		headers.Get("If-None-Match") != "" || !strings.Contains((*calls)[1].body, "UID:u-keep") ||
		!strings.Contains((*calls)[1].body, "FN:Ada Renamed") || strings.Contains((*calls)[1].body, "TEL") {
		t.Errorf("calls = %+v, headers = %v", *calls, *headers)
	}
}

func TestContactsDeleteSendsOneETagBoundDelete(t *testing.T) {
	calls, headers := contactFixture(t, plainCard, func() (*http.Response, error) { return eventChanged(http.StatusNoContent, ""), nil })
	result, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts.delete", contactDeleteArgs, true)
	var got ContactWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil ||
		got != (ContactWriteResult{Addressbook: "contacts", ID: "ada.vcf", Deleted: true}) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if eventMethods(calls) != "GET DELETE" || (*calls)[1].url.Path != contactItemPath || headers.Get("If-Match") != `"e1"` ||
		headers.Get("If-None-Match") != "" {
		t.Errorf("calls = %+v, headers = %v", *calls, *headers)
	}
}

func TestContactsUpdateRefusesUnmodeledPropertiesBeforeAnyPut(t *testing.T) {
	for name, extra := range map[string]string{
		"photo": "PHOTO;ENCODING=b;TYPE=JPEG:" + contactPhoto + "\r\n",
		"group": "KIND:group\r\n",
		"x":     "X-CUSTOM:value\r\n",
	} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet {
				t.Errorf("%s: a mutation was sent: %s", name, r.Method)
			}
			return contactGetAnswer("e1", vcardOf("u1", "Ada", extra)), nil
		})
		_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts.update", contactUpdateArgs, true)
		if err == nil || !strings.Contains(err.Error(), "does not write") || strings.Contains(err.Error(), contactPhoto) ||
			len(*calls) != 1 {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(*calls))
		}
	}
}

func TestContactsChangedETagIsRefusedBeforeAnyMutation(t *testing.T) {
	for _, op := range []string{"update", "delete"} {
		args := contactUpdateArgs
		if op == "delete" {
			args = contactDeleteArgs
		}
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet {
				t.Errorf("%s: a mutation was sent: %s", op, r.Method)
			}
			return contactGetAnswer("e9", plainCard), nil
		})
		_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+op, args, true)
		if err == nil || !strings.Contains(err.Error(), "precondition failed") || len(*calls) != 1 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestContactsDeleteRefusesWhatIsNoVCard(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Errorf("a mutation was sent: %s", r.Method)
		}
		return contactGetAnswer("e1", "this is no card"), nil
	})
	_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts.delete", contactDeleteArgs, true)
	if err == nil || !strings.Contains(err.Error(), "not a valid vCard") || len(*calls) != 1 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestContactsCreateRefusesAReadOnlyAddressbookWithoutMutation(t *testing.T) {
	for _, privileges := range [][]string{{"read"}, {"read", "write-content"}, nil} {
		calls := serve(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != methodPropfind {
				t.Errorf("a mutation was sent: %s", r.Method)
			}
			return contactBookAnswer(privileges), nil
		})
		_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts.create", contactCreateArgs, true)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "read-only") || len(*calls) != 1 {
			t.Errorf("%v: err = %v, calls = %d", privileges, err, len(*calls))
		}
	}
}

func TestContactsUpdateAndDeleteReportAReadOnlyAddressbookByForbidden(t *testing.T) {
	for op, args := range map[string]string{"update": contactUpdateArgs, "delete": contactDeleteArgs} {
		calls, _ := contactFixture(t, plainCard, func() (*http.Response, error) { return status(http.StatusForbidden), nil })
		_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+op, args, true)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "read-only") ||
			strings.Contains(err.Error(), contactHint) || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestContactWritePreconditionFailureIsClear(t *testing.T) {
	for op, tc := range map[string]struct{ args, want string }{
		"create": {contactCreateArgs, "already exists"},
		"update": {contactUpdateArgs, "precondition failed"},
		"delete": {contactDeleteArgs, "precondition failed"},
	} {
		calls, _ := contactFixture(t, plainCard, func() (*http.Response, error) { return status(http.StatusPreconditionFailed), nil })
		_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+op, tc.args, true)
		if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), tc.want) ||
			strings.Contains(err.Error(), contactHint) || strings.Contains(err.Error(), bodyCanary) || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestContactWriteUnclearOutcomeIsReportedAndNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"accepted":   func() (*http.Response, error) { return status(http.StatusAccepted), nil },
		"500":        func() (*http.Response, error) { return status(500), nil },
		"503":        func() (*http.Response, error) { return status(503), nil },
	}
	for op, args := range map[string]string{
		"create": contactCreateArgs, "update": contactUpdateArgs, "delete": contactDeleteArgs,
	} {
		for name, answer := range answers {
			calls, _ := contactFixture(t, plainCard, answer)
			_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+op, args, true)
			if err == nil || !strings.Contains(err.Error(), contactHint) || !strings.Contains(err.Error(), "contacts.contacts.list") ||
				strings.Contains(err.Error(), bodyCanary) || len(*calls) != 2 {
				t.Errorf("%s %s: err = %v, calls = %d", op, name, err, len(*calls))
			}
		}
	}
}

func TestContactWriteRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	redirect := func() *http.Response {
		response := status(http.StatusFound)
		response.Header.Set("Location", "https://evil.example.invalid/redirect-canary-4e2a")
		return response
	}
	for op, args := range map[string]string{
		"create": contactCreateArgs, "update": contactUpdateArgs, "delete": contactDeleteArgs,
	} {
		calls, _ := contactFixture(t, plainCard, func() (*http.Response, error) { return redirect(), nil })
		_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+op, args, true)
		if !strings.Contains(fmtErr(err), "redirect") || strings.Contains(fmtErr(err), "canary") ||
			strings.Contains(fmtErr(err), contactHint) || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d", op, err, len(*calls))
		}
		calls = serve(t, func(*http.Request) (*http.Response, error) { return redirect(), nil })
		_, err = contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+op, args, true)
		if !strings.Contains(fmtErr(err), "redirect") || strings.Contains(fmtErr(err), "canary") || len(*calls) != 1 {
			t.Errorf("%s pre-check: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestContactWritesRefuseBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	ops := map[string]string{"create": contactCreateArgs, "update": contactUpdateArgs, "delete": contactDeleteArgs}
	swap := func(args, book string) string { return strings.Replace(args, `"contacts"`, `"`+book+`"`, 1) }
	for op, args := range ops {
		for name, tc := range map[string]struct {
			targets []string
			args    string
		}{
			"system with kind": {[]string{"addressbook"}, swap(args, systemAddressbook)},
			"system explicit":  {[]string{"addressbook/" + systemAddressbook}, swap(args, systemAddressbook)},
			"no book target":   {[]string{"folder/Reports"}, args},
			"other book":       {[]string{"addressbook/team"}, swap(args, "other-secret")},
			"traversal":        {[]string{"addressbook"}, swap(args, "../bobby")},
		} {
			_, err := contactWrite(t, contactTools, tc.targets, "nextcloud.contacts.contacts."+op, tc.args, true)
			if classOf(err) != provider.ClassPermission {
				t.Errorf("%s %s: err = %v, want a permission refusal", op, name, err)
				continue
			}
			for _, leak := range []string{"other-secret", "bobby", "Reports", "team"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%s %s: the refusal names %q: %v", op, name, leak, err)
				}
			}
		}
		// Not confirmed.
		if _, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+op, args, false); err == nil {
			t.Errorf("unconfirmed %s: accepted", op)
		}
	}
	with := func(base, extra string) string { return strings.TrimSuffix(base, "}") + "," + extra + "}" }
	for name, tc := range map[string]struct{ op, args string }{
		"bad id":          {"update", strings.Replace(contactUpdateArgs, "ada.vcf", "a/b.vcf", 1)},
		"dot id":          {"delete", strings.Replace(contactDeleteArgs, "ada.vcf", "..", 1)},
		"wildcard etag":   {"delete", strings.Replace(contactDeleteArgs, `"e1"`, `"*"`, 1)},
		"spaced etag":     {"update", strings.Replace(contactUpdateArgs, `"e1"`, `"e 1"`, 1)},
		"create with id":  {"create", with(contactCreateArgs, `"id":"x.vcf"`)},
		"create with tag": {"create", with(contactCreateArgs, `"etag":"e1"`)},
		"create with uid": {"create", with(contactCreateArgs, `"uid":"x"`)},
		"control chars":   {"create", with(contactCreateArgs, `"title":"a\u0001b"`)},
		"semicolon":       {"create", with(contactCreateArgs, `"organization":"a;b"`)},
		"long name":       {"create", `{"addressbook":"contacts","name":"` + strings.Repeat("a", 300) + `"}`},
		"bad email":       {"create", with(contactCreateArgs, `"emails":[{"value":"not an address"}]`)},
		"bad url":         {"create", with(contactCreateArgs, `"urls":["ftp://example.org"]`)},
		"free header":     {"update", with(contactUpdateArgs, `"headers":{"X":"y"}`)},
		"fields on del":   {"delete", with(contactDeleteArgs, `"name":"x"`)},
	} {
		if _, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+tc.op, tc.args, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Without a tools list naming it the delete is not offered, whatever the permissions.
	if _, err := contactWrite(t, nil, []string{"addressbook"}, "nextcloud.contacts.contacts.delete", contactDeleteArgs, true); err == nil {
		t.Error("delete without a tools list: accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestContactWriteToolsContract(t *testing.T) {
	reg := registry(t)
	for _, tc := range []struct {
		id          string
		effect      capability.Effect
		idempotency capability.Idempotency
		allowList   bool
	}{
		{contactsCreate.ID, capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		{contactsUpdate.ID, capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		{contactsDelete.ID, capability.EffectDelete, capability.IdempotencyIdempotent, true},
	} {
		d, _, ok := reg.Lookup(tc.id)
		r := d.Risk
		if !ok || d.Group != groupContacts || r.Effect != tc.effect || r.Idempotency != tc.idempotency ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != contactsSensitivity ||
			d.RequiresToolAllowList != tc.allowList {
			t.Errorf("%s = %+v", tc.id, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if id == contactsCreate.ID || id == contactsUpdate.ID || id == contactsDelete.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
}
