package nextcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const manageBookPath = contactHome + "clients/"

var bookManageTools = []string{addressbooksList.ID, addressbooksCreate.ID, addressbooksUpdate.ID, addressbooksDelete.ID}

func manageBooks(t *testing.T, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	return eventWrite(t, bookManageTools, targets, operation, args, confirm)
}

// bookOwnerAnswer is the pre-check answer of the address book clients, owned by owner; an empty owner reports none.
func bookOwnerAnswer(owner string) *http.Response {
	props := `<d:resourcetype><d:collection/><card:addressbook/></d:resourcetype>`
	if owner != "" {
		props += `<oc:owner-principal xmlns:oc="http://owncloud.org/ns"><d:href>` + principalBase + owner +
			`/</d:href></oc:owner-principal>`
	}
	return xmlResponse(http.StatusMultiStatus, bookMultistatus(`<d:response><d:href>`+manageBookPath+`</d:href>`+
		`<d:propstat><d:prop>`+props+`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`))
}

func bookPatchAnswer(name, description string) *http.Response {
	pick := func(state, element string) string {
		code := map[string]string{"ok": "200 OK", "denied": "403 Forbidden"}[state]
		if code == "" {
			return ""
		}
		return `<d:propstat><d:prop>` + element + `</d:prop><d:status>HTTP/1.1 ` + code + `</d:status></d:propstat>`
	}
	return xmlResponse(http.StatusMultiStatus, bookMultistatus(`<d:response><d:href>`+manageBookPath+`</d:href>`+
		pick(name, `<d:displayname/>`)+pick(description, `<card:addressbook-description/>`)+`</d:response>`))
}

// bookFixture answers the pre-check with owner and the mutation with mutate.
func bookFixture(t *testing.T, owner string, mutate func() (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == methodPropfind {
			return bookOwnerAnswer(owner), nil
		}
		return mutate()
	})
}

func TestAddressbooksCreateSendsOneGeneratedExtendedMkcol(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return status(http.StatusCreated), nil })
	result, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.create", `{"name":"Team <A> & \"B\""}`, true)
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	var got AddressbookWriteResult
	if err := json.Unmarshal(result, &got); err != nil || !got.Created ||
		!regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`).MatchString(got.ID) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(*calls) != 1 || (*calls)[0].method != methodMkcol || (*calls)[0].url.Path != contactHome+got.ID+"/" {
		t.Fatalf("calls = %+v", *calls)
	}
	const golden = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:mkcol xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:set><d:prop>` +
		`<d:resourcetype><d:collection/><card:addressbook/></d:resourcetype>` +
		`<d:displayname>Team &lt;A&gt; &amp; &#34;B&#34;</d:displayname></d:prop></d:set></d:mkcol>`
	if (*calls)[0].body != golden {
		t.Errorf("body = %s", (*calls)[0].body)
	}
}

func TestAddressbooksCreateNeedsTheGeneralTargetAndValidInput(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	for _, targets := range [][]string{{"addressbook/clients"}, {"folder/Reports"}, {"calendar"}} {
		_, err := manageBooks(t, targets, "nextcloud.addressbooks.create", `{"name":"X"}`, true)
		if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "clients") {
			t.Errorf("%v: err = %v", targets, err)
		}
	}
	for name, args := range map[string]string{
		"blank name":   `{"name":"   "}`,
		"control":      "{\"name\":\"a\\u0007b\"}",
		"long name":    `{"name":"` + strings.Repeat("n", 129) + `"}`,
		"uri argument": `{"name":"X","addressbook":"mine"}`,
		"description":  `{"name":"X","description":"d"}`,
		"free field":   `{"name":"X","url":"https://evil.example.invalid"}`,
	} {
		if _, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.create", args, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.create", `{"name":"X"}`, false); err == nil {
		t.Error("unconfirmed create: accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestAddressbooksCreateFailuresAreClear(t *testing.T) {
	for code, want := range map[int]string{405: "already exists", 409: "cannot create", 403: "may not"} {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return status(code), nil })
		_, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.create", `{"name":"X"}`, true)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), bodyCanary) ||
			strings.Contains(err.Error(), eventHint) || len(*calls) != 1 {
			t.Errorf("%d: err = %v, calls = %d", code, err, len(*calls))
		}
	}
}

func TestAddressbooksUpdateSendsOneProppatchAfterTheOwnerCheck(t *testing.T) {
	calls := bookFixture(t, aliceUser, func() (*http.Response, error) { return bookPatchAnswer("ok", "ok"), nil })
	result, err := manageBooks(t, []string{"addressbook/clients"}, "nextcloud.addressbooks.update",
		`{"addressbook":"clients","name":"Clients <2026>","description":"All & more"}`, true)
	var got AddressbookWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil || got != (AddressbookWriteResult{ID: "clients", Updated: true}) {
		t.Fatalf("update = %s, %v", result, err)
	}
	if eventMethods(calls) != "PROPFIND PROPPATCH" || (*calls)[0].url.Path != manageBookPath ||
		(*calls)[0].depth != depthSelf || (*calls)[1].url.Path != manageBookPath {
		t.Fatalf("calls = %+v", *calls)
	}
	const golden = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propertyupdate xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:set><d:prop>` +
		`<d:displayname>Clients &lt;2026&gt;</d:displayname>` +
		`<card:addressbook-description>All &amp; more</card:addressbook-description>` +
		`</d:prop></d:set></d:propertyupdate>`
	if (*calls)[1].body != golden {
		t.Errorf("body = %s", (*calls)[1].body)
	}
}

func TestAddressbooksUpdateNeedsOneOfNameAndDescription(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("no request expected") })
	for _, args := range []string{`{"addressbook":"clients"}`, `{"addressbook":"clients","description":"\u0007"}`} {
		if _, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.update", args, true); err == nil {
			t.Errorf("%s: accepted", args)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d", len(*calls))
	}
	bookFixture(t, aliceUser, func() (*http.Response, error) { return bookPatchAnswer("none", "ok"), nil })
	if _, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.update",
		`{"addressbook":"clients","description":"d"}`, true); err != nil {
		t.Errorf("description only: %v", err)
	}
}

func TestAddressbooksUpdateReportsPartialAndRefusedProperties(t *testing.T) {
	both := `{"addressbook":"clients","name":"N","description":"d"}`
	for name, tc := range map[string]struct{ name, description, want string }{
		"description refused": {"ok", "denied", "changed the name but refused the change of description"},
		"name refused":        {"denied", "ok", "changed the description but refused the change of name"},
		"all refused":         {"denied", "denied", "refused the change of name and description, nothing was changed"},
		"missing":             {"ok", "none", "refused the change of description"},
	} {
		bookFixture(t, aliceUser, func() (*http.Response, error) { return bookPatchAnswer(tc.name, tc.description), nil })
		_, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.update", both, true)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	bookFixture(t, aliceUser, func() (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, "<not-xml"), nil
	})
	if _, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.update", both, true); err == nil ||
		!strings.Contains(err.Error(), eventHint) {
		t.Errorf("unreadable answer: err = %v", err)
	}
}

func TestAddressbookMutationsRefuseForeignAndUnconfirmedOwnerBeforeAnyChange(t *testing.T) {
	for op, args := range map[string]string{
		"nextcloud.addressbooks.update": `{"addressbook":"clients","name":"N"}`,
		"nextcloud.addressbooks.delete": `{"addressbook":"clients"}`,
	} {
		for _, owner := range []string{"bob", "alice2", ""} {
			calls := bookFixture(t, owner, func() (*http.Response, error) {
				t.Errorf("%s: a mutation was sent", op)
				return status(http.StatusNoContent), nil
			})
			_, err := manageBooks(t, []string{"addressbook"}, op, args, true)
			if classOf(err) != provider.ClassPermission || len(*calls) != 1 || strings.Contains(err.Error(), "bob") {
				t.Errorf("%s owner %q: err = %v, calls = %d", op, owner, err, len(*calls))
			}
		}
	}
}

func TestAddressbookMutationsRefuseUnboundAndSystemBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	for _, op := range []string{"update", "delete"} {
		extra := ""
		if op == "update" {
			extra = `,"name":"N"`
		}
		for name, tc := range map[string]struct {
			targets []string
			book    string
		}{
			"other book":   {[]string{"addressbook/work"}, "other-secret"},
			"no target":    {[]string{"folder/Reports"}, "clients"},
			"system":       {[]string{"addressbook/" + systemAddressbook}, systemAddressbook},
			"system, kind": {[]string{"addressbook"}, systemAddressbook},
			"traversal":    {[]string{"addressbook"}, "../bobby"},
		} {
			_, err := manageBooks(t, tc.targets, "nextcloud.addressbooks."+op, `{"addressbook":"`+tc.book+`"`+extra+`}`, true)
			if classOf(err) != provider.ClassPermission {
				t.Errorf("%s %s: err = %v", op, name, err)
				continue
			}
			for _, leak := range []string{"other-secret", "work", "bobby", "Reports"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%s %s: the refusal names %q", op, name, leak)
				}
			}
		}
		if _, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks."+op, `{"addressbook":"clients"`+extra+`}`, false); err == nil {
			t.Errorf("unconfirmed %s: accepted", op)
		}
	}
	if _, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.delete", `{"addressbook":"clients","name":"x"}`, true); err == nil {
		t.Error("delete with extra field: accepted")
	}
	// Without a tools list naming it the delete is not offered.
	if _, err := eventWrite(t, nil, []string{"addressbook"}, "nextcloud.addressbooks.delete", `{"addressbook":"clients"}`, true); err == nil {
		t.Error("delete without a tools list: accepted")
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestAddressbookMutationsRefuseANodeThatIsNoAddressbook(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != methodPropfind {
			t.Errorf("a mutation was sent: %s", r.Method)
		}
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(bookNode(manageBookPath, "Plain", false))), nil
	})
	if _, err := manageBooks(t, []string{"addressbook"}, "nextcloud.addressbooks.delete", `{"addressbook":"clients"}`, true); err == nil ||
		len(*calls) != 1 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestAddressbooksDeleteSendsOneDeleteAfterTheOwnerCheck(t *testing.T) {
	calls := bookFixture(t, aliceUser, noContent)
	result, err := manageBooks(t, []string{"addressbook/clients"}, "nextcloud.addressbooks.delete", `{"addressbook":"clients"}`, true)
	var got AddressbookWriteResult
	if err != nil || json.Unmarshal(result, &got) != nil || got != (AddressbookWriteResult{ID: "clients", Deleted: true}) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if eventMethods(calls) != "PROPFIND DELETE" || (*calls)[1].url.Path != manageBookPath || (*calls)[1].body != "" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestAddressbookManagementUnclearOutcomeIsReportedAndNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"accepted":   func() (*http.Response, error) { return status(http.StatusAccepted), nil },
	}
	for _, code := range []int{500, 502, 503, 504} {
		answers[strconv.Itoa(code)] = func() (*http.Response, error) { return status(code), nil }
	}
	for op, args := range map[string]string{
		"nextcloud.addressbooks.create": `{"name":"X"}`,
		"nextcloud.addressbooks.update": `{"addressbook":"clients","name":"N"}`,
		"nextcloud.addressbooks.delete": `{"addressbook":"clients"}`,
	} {
		for name, answer := range answers {
			calls := bookFixture(t, aliceUser, answer)
			_, err := manageBooks(t, []string{"addressbook"}, op, args, true)
			want := 2
			if strings.HasSuffix(op, "create") {
				want = 1
			}
			if err == nil || !strings.Contains(err.Error(), eventHint) || !strings.Contains(err.Error(), "before repeating") ||
				strings.Contains(err.Error(), bodyCanary) || len(*calls) != want {
				t.Errorf("%s %s: err = %v, calls = %d", op, name, err, len(*calls))
			}
		}
	}
}

func TestAddressbookManagementRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	redirect := func(code int) *http.Response {
		response := status(code)
		response.Header.Set("Location", "https://evil.example.invalid/redirect-canary-4e2a")
		return response
	}
	for op, args := range map[string]string{
		"nextcloud.addressbooks.create": `{"name":"X"}`,
		"nextcloud.addressbooks.update": `{"addressbook":"clients","name":"N"}`,
		"nextcloud.addressbooks.delete": `{"addressbook":"clients"}`,
	} {
		for _, code := range []int{301, 302, 307, 308} {
			calls := bookFixture(t, aliceUser, func() (*http.Response, error) { return redirect(code), nil })
			_, err := manageBooks(t, []string{"addressbook"}, op, args, true)
			if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "redirect") ||
				strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), eventHint) {
				t.Errorf("%s %d: err = %v, calls = %d", op, code, err, len(*calls))
			}
			if strings.HasSuffix(op, "create") {
				continue
			}
			// On the pre-check, which then sends no mutation.
			calls = serve(t, func(*http.Request) (*http.Response, error) { return redirect(code), nil })
			_, err = manageBooks(t, []string{"addressbook"}, op, args, true)
			if !strings.Contains(fmtErr(err), "redirect") || strings.Contains(fmtErr(err), "canary") || len(*calls) != 1 {
				t.Errorf("%s %d pre-check: err = %v, calls = %d", op, code, err, len(*calls))
			}
		}
	}
}

func TestAddressbookManagementToolsContract(t *testing.T) {
	reg := registry(t)
	for _, tc := range []struct {
		id          string
		effect      capability.Effect
		idempotency capability.Idempotency
		allowList   bool
	}{
		{addressbooksCreate.ID, capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		{addressbooksUpdate.ID, capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		{addressbooksDelete.ID, capability.EffectDelete, capability.IdempotencyIdempotent, true},
	} {
		d, _, ok := reg.Lookup(tc.id)
		r := d.Risk
		if !ok || d.Group != groupContacts || r.Effect != tc.effect || r.Idempotency != tc.idempotency ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != contactsSensitivity ||
			d.RequiresToolAllowList != tc.allowList {
			t.Errorf("%s = %+v", tc.id, d)
		}
	}
	d, _, _ := reg.Lookup(addressbooksDelete.ID)
	if !strings.Contains(d.Description, "all its contacts") {
		t.Errorf("delete description = %q", d.Description)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.HasPrefix(id, "nextcloud.addressbooks.") && id != addressbooksList.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
}
