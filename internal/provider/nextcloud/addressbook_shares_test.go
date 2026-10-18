package nextcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var bookShareTools = []string{addressbooksList.ID, addressbooksShare.ID, addressbooksUnshare.ID}

func bookShareCall(t *testing.T, targets []string, operation, args string, confirm bool) (json.RawMessage, error) {
	t.Helper()
	return eventWrite(t, bookShareTools, targets, operation, args, confirm)
}

func TestAddressbooksShareSendsOneGoldenPostAfterTheOwnerCheck(t *testing.T) {
	for name, tc := range map[string]struct{ op, args, body string }{
		"user read-write": {"share", `{"addressbook":"clients","type":"users","id":"bob","access":"read-write"}`,
			shareHead + `<o:set><d:href>principal:principals/users/bob</d:href><o:read-write/></o:set>` + shareTail},
		"group read": {"share", `{"addressbook":"clients","type":"groups","id":"Sales Team","access":"read"}`,
			shareHead + `<o:set><d:href>principal:principals/groups/Sales Team</d:href><o:read/></o:set>` + shareTail},
		"unshare user": {"unshare", `{"addressbook":"clients","type":"users","id":"bob"}`,
			shareHead + `<o:remove><d:href>principal:principals/users/bob</d:href></o:remove>` + shareTail},
		"unshare group": {"unshare", `{"addressbook":"clients","type":"groups","id":"staff"}`,
			shareHead + `<o:remove><d:href>principal:principals/groups/staff</d:href></o:remove>` + shareTail},
	} {
		calls := bookFixture(t, aliceUser, func() (*http.Response, error) { return status(http.StatusOK), nil })
		result, err := bookShareCall(t, []string{"addressbook/clients"}, "nextcloud.contacts.address-books."+tc.op, tc.args, true)
		var got AddressbookShareResult
		if err != nil || json.Unmarshal(result, &got) != nil || !got.Accepted || got.Addressbook != "clients" {
			t.Fatalf("%s: %s, %v", name, result, err)
		}
		if eventMethods(calls) != "PROPFIND POST" || (*calls)[1].url.Path != manageBookPath ||
			(*calls)[1].body != tc.body {
			t.Errorf("%s: calls = %+v", name, *calls)
		}
	}
}

func TestAddressbookShareRefusesForeignRecipientsBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	for name, args := range map[string]string{
		"e-mail type":      `{"addressbook":"clients","type":"emails","id":"x","access":"read"}`,
		"mailto id":        `{"addressbook":"clients","type":"users","id":"mailto:bob@example.com","access":"read"}`,
		"circle type":      `{"addressbook":"clients","type":"circles","id":"team","access":"read"}`,
		"circle principal": `{"addressbook":"clients","type":"users","id":"principals/circles/team","access":"read"}`,
		"remote principal": `{"addressbook":"clients","type":"users","id":"bob@example.org/x","access":"read"}`,
		"separator":        `{"addressbook":"clients","type":"users","id":"a/b","access":"read"}`,
		"traversal":        `{"addressbook":"clients","type":"users","id":"..","access":"read"}`,
		"control":          "{\"addressbook\":\"clients\",\"type\":\"users\",\"id\":\"a\\nb\",\"access\":\"read\"}",
		"xml":              `{"addressbook":"clients","type":"groups","id":"<x>","access":"read"}`,
		"leading space":    `{"addressbook":"clients","type":"groups","id":" staff","access":"read"}`,
		"user with space":  `{"addressbook":"clients","type":"users","id":"a b","access":"read"}`,
		"long":             `{"addressbook":"clients","type":"users","id":"` + strings.Repeat("a", 65) + `","access":"read"}`,
		"empty":            `{"addressbook":"clients","type":"users","id":"","access":"read"}`,
		"no access":        `{"addressbook":"clients","type":"users","id":"bob"}`,
		"bad access":       `{"addressbook":"clients","type":"users","id":"bob","access":"admin"}`,
		"free field":       `{"addressbook":"clients","type":"users","id":"bob","access":"read","body":"<x/>"}`,
		"second recipient": `{"addressbook":"clients","type":"users","id":"bob,carol","access":"read"}`,
		"recipient list":   `{"addressbook":"clients","type":"users","id":["bob"],"access":"read"}`,
		"publish":          `{"addressbook":"clients","type":"public","id":"x","access":"read"}`,
	} {
		if _, err := bookShareCall(t, []string{"addressbook"}, "nextcloud.contacts.address-books.share", args, true); err == nil {
			t.Errorf("share %s: accepted", name)
		}
	}
	for name, args := range map[string]string{
		"e-mail type": `{"addressbook":"clients","type":"emails","id":"x"}`,
		"mailto id":   `{"addressbook":"clients","type":"groups","id":"mailto:x"}`,
		"with access": `{"addressbook":"clients","type":"users","id":"bob","access":"read"}`,
	} {
		if _, err := bookShareCall(t, []string{"addressbook"}, "nextcloud.contacts.address-books.unshare", args, true); err == nil {
			t.Errorf("unshare %s: accepted", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestAddressbookShareRefusesForeignAndUnconfirmedOwnerAfterTheCheckOnly(t *testing.T) {
	for op, args := range map[string]string{
		"nextcloud.contacts.address-books.share":   `{"addressbook":"clients","type":"users","id":"carol","access":"read"}`,
		"nextcloud.contacts.address-books.unshare": `{"addressbook":"clients","type":"users","id":"carol"}`,
	} {
		for _, owner := range []string{"bob", "alice2", ""} {
			calls := bookFixture(t, owner, func() (*http.Response, error) {
				t.Errorf("%s: a change was sent", op)
				return status(http.StatusOK), nil
			})
			_, err := bookShareCall(t, []string{"addressbook"}, op, args, true)
			if classOf(err) != provider.ClassPermission || eventMethods(calls) != "PROPFIND" ||
				strings.Contains(err.Error(), "bob") || strings.Contains(err.Error(), "carol") {
				t.Errorf("%s owner %q: err = %v, calls = %d", op, owner, err, len(*calls))
			}
		}
	}
}

func TestAddressbookShareRefusesUnboundCalendarsBeforeAnyRequest(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("the provider was contacted: %s %s", r.Method, r.URL.Path)
		return nil, errors.New("no request expected")
	})
	for _, op := range []string{"share", "unshare"} {
		extra := ""
		if op == "share" {
			extra = `,"access":"read"`
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
			_, err := bookShareCall(t, tc.targets, "nextcloud.contacts.address-books."+op,
				`{"addressbook":"`+tc.book+`","type":"users","id":"bob"`+extra+`}`, true)
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
		if _, err := bookShareCall(t, []string{"addressbook"}, "nextcloud.contacts.address-books."+op,
			`{"addressbook":"clients","type":"users","id":"bob"`+extra+`}`, false); err == nil {
			t.Errorf("unconfirmed %s: accepted", op)
		}
		// Without a tools list naming it the tool is not offered.
		if _, err := eventWrite(t, nil, []string{"addressbook"}, "nextcloud.contacts.address-books."+op,
			`{"addressbook":"clients","type":"users","id":"bob"`+extra+`}`, true); err == nil {
			t.Errorf("%s without a tools list: accepted", op)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestAddressbookShareUnclearOutcomeIsReportedAndNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"timeout":    func() (*http.Response, error) { return nil, timeoutError{} },
		"connection": func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"accepted":   func() (*http.Response, error) { return status(http.StatusAccepted), nil },
		"no content": func() (*http.Response, error) { return status(http.StatusNoContent), nil },
	}
	for _, code := range []int{500, 502, 503, 504} {
		answers[strconv.Itoa(code)] = func() (*http.Response, error) { return status(code), nil }
	}
	for op, args := range map[string]string{
		"nextcloud.contacts.address-books.share":   `{"addressbook":"clients","type":"users","id":"bob","access":"read"}`,
		"nextcloud.contacts.address-books.unshare": `{"addressbook":"clients","type":"users","id":"bob"}`,
	} {
		for name, answer := range answers {
			calls := bookFixture(t, aliceUser, answer)
			_, err := bookShareCall(t, []string{"addressbook"}, op, args, true)
			if err == nil || !strings.Contains(err.Error(), eventHint) || !strings.Contains(err.Error(), "before repeating") ||
				strings.Contains(err.Error(), bodyCanary) || eventMethods(calls) != "PROPFIND POST" {
				t.Errorf("%s %s: err = %v, calls = %d", op, name, err, len(*calls))
			}
		}
		// A refusal is clear: no hint.
		calls := bookFixture(t, aliceUser, func() (*http.Response, error) { return status(http.StatusForbidden), nil })
		if _, err := bookShareCall(t, []string{"addressbook"}, op, args, true); err == nil ||
			strings.Contains(err.Error(), eventHint) || len(*calls) != 2 {
			t.Errorf("%s 403: err = %v, calls = %d", op, err, len(*calls))
		}
	}
}

func TestAddressbookShareRedirectsAreClearFailuresWithoutATarget(t *testing.T) {
	redirect := func(code int) *http.Response {
		response := status(code)
		response.Header.Set("Location", "https://evil.example.invalid/redirect-canary-4e2a")
		return response
	}
	args := `{"addressbook":"clients","type":"users","id":"bob","access":"read"}`
	for _, code := range []int{301, 302, 307, 308} {
		calls := bookFixture(t, aliceUser, func() (*http.Response, error) { return redirect(code), nil })
		_, err := bookShareCall(t, []string{"addressbook"}, "nextcloud.contacts.address-books.share", args, true)
		if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "redirect") ||
			strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), eventHint) || len(*calls) != 2 {
			t.Errorf("%d: err = %v, calls = %d", code, err, len(*calls))
		}
		calls = serve(t, func(*http.Request) (*http.Response, error) { return redirect(code), nil })
		_, err = bookShareCall(t, []string{"addressbook"}, "nextcloud.contacts.address-books.share", args, true)
		if !strings.Contains(fmtErr(err), "redirect") || strings.Contains(fmtErr(err), "canary") || len(*calls) != 1 {
			t.Errorf("%d pre-check: err = %v, calls = %d", code, err, len(*calls))
		}
	}
}

func bookInviteListing(invites string) string {
	return bookMultistatus(`<d:response><d:href>` + contactHome + `contacts/</d:href><d:propstat><d:prop>` +
		`<d:displayname>Contacts</d:displayname><d:resourcetype><d:collection/><card:addressbook/></d:resourcetype>` +
		`<oc:invite xmlns:oc="http://owncloud.org/ns">` + invites + `</oc:invite>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
}

func TestAddressbooksListReportsTheSharesFromInvite(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookInviteListing(
			`<oc:organizer><d:href>principal:principals/users/alice</d:href><oc:common-name>Alice</oc:common-name></oc:organizer>`+
				inviteUser("principal:principals/users/bob", "Bob &lt;b&gt;", "read-write", `<oc:invite-accepted/>`)+
				inviteUser("principal:principals/groups/Sales Team", "Sales", "read", `<oc:invite-noresponse/>`)+
				inviteUser("mailto:guest@example.com", "guest@example.com", "read", `<oc:invite-declined/>`)+
				inviteUser("principal:principals/circles/team", "Circle", "read", ``)+
				inviteUser("principal:principals/users/mail", "mail@example.com", "read", ``)+
				inviteUser("principal:principals/users/a%2Fb", "x", "read", ``))), nil
	})
	result, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.address-books.list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var listed AddressbooksResult
	if err := json.Unmarshal(result, &listed); err != nil || listed.Count != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	want := []CollectionShare{
		{Type: "users", ID: "bob", Name: "Bob <b>", Access: "read-write", Status: "accepted"},
		{Type: "groups", ID: "Sales Team", Name: "Sales", Access: "read", Status: "pending"},
		{Type: "other", Access: "read", Status: "declined"},
		{Type: "other", Access: "read"},
		{Type: "users", ID: "mail", Access: "read"},
		{Type: "other", Access: "read"},
	}
	got := listed.Addressbooks[0].Shares
	if len(got) != len(want) || listed.Addressbooks[0].SharesTruncated {
		t.Fatalf("shares = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("share %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if strings.Contains(string(result), "example.com") || strings.Contains(string(result), "alice") ||
		!strings.Contains((*calls)[0].body, "<oc:invite/>") {
		t.Errorf("result = %s, body = %s", result, (*calls)[0].body)
	}
}

func TestAddressbooksListCapsAndMarksTheShares(t *testing.T) {
	var invites strings.Builder
	for i := 0; i < 60; i++ {
		invites.WriteString(inviteUser("principal:principals/users/u"+strconv.Itoa(i), "U", "read", ``))
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookInviteListing(invites.String())), nil
	})
	result, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.address-books.list", `{}`)
	var listed AddressbooksResult
	if err != nil || json.Unmarshal(result, &listed) != nil || len(listed.Addressbooks[0].Shares) != 50 ||
		!listed.Addressbooks[0].SharesTruncated || listed.Addressbooks[0].Shares[49].ID != "u49" {
		t.Fatalf("shares = %d, %s, %v", len(listed.Addressbooks[0].Shares), result, err)
	}
	// An address book without invites reports none.
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookListing()), nil
	})
	result, err = bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.address-books.list", `{}`)
	if err != nil || strings.Contains(string(result), "shares") {
		t.Errorf("result = %s, %v", result, err)
	}
}

func TestAddressbookShareToolsContract(t *testing.T) {
	reg := registry(t)
	for _, tc := range []struct {
		id     string
		effect capability.Effect
	}{
		{addressbooksShare.ID, capability.EffectUpdate},
		{addressbooksUnshare.ID, capability.EffectDelete},
	} {
		d, _, ok := reg.Lookup(tc.id)
		r := d.Risk
		if !ok || d.Group != groupContacts || r.Effect != tc.effect || r.Idempotency != capability.IdempotencyIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity != contactsSensitivity ||
			!d.RequiresToolAllowList {
			t.Errorf("%s = %+v", tc.id, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if id == addressbooksShare.ID || id == addressbooksUnshare.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
	}
}
