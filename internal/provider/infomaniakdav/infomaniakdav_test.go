package infomaniakdav

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The canaries stand for a user name, an application password, and provider text. No test reaches
// Infomaniak: every request is answered by the package's own transport seam.
const (
	userCanary = "AB12345"
	passCanary = "canary-infomaniak-dav-app-password-4e1c"
	bodyCanary = "provider-body-canary-infomaniak-dav-9a27"
	nameCanary = "name-canary-infomaniak-dav-5d03"
	userEnv    = "TEST_INFOMANIAKDAV_USER"
	passEnv    = "TEST_INFOMANIAKDAV_PASSWORD"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type call struct {
	method, path, depth, auth, body string
	header                          http.Header
}

// serve replaces the package transport for one test and records every request.
func serve(t *testing.T, handler func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	calls := &[]call{}
	previous := transport
	transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		*calls = append(*calls, call{r.Method, r.URL.Host + r.URL.EscapedPath(), r.Header.Get("Depth"),
			r.Header.Get("Authorization"), string(raw), r.Header.Clone()})
		return handler(r)
	})
	t.Cleanup(func() { transport = previous })
	t.Cleanup(limiters.Replace(userCanary+"\x00"+passCanary,
		ratelimit.New(0, time.Now, func(context.Context, time.Duration) error { return nil })))
	return calls
}

func xmlResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/xml"}},
		Body: io.NopCloser(strings.NewReader(body))}
}

func multistatus(entries ...string) string {
	return `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" ` +
		`xmlns:card="urn:ietf:params:xml:ns:carddav" xmlns:a="http://apple.com/ns/ical/">` +
		strings.Join(entries, "") + `</d:multistatus>`
}

func linkXML(href, property, target string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><` + property + `><d:href>` + target +
		`</d:href></` + property + `></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func collectionXML(href, kind, name string) string {
	extra := `<c:calendar-description>desc ` + name + `</c:calendar-description><a:calendar-color>#FF8800FF</a:calendar-color>`
	if kind == "card:addressbook" {
		extra = `<card:addressbook-description>desc ` + name + `</card:addressbook-description>`
	}
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:displayname>` + name +
		`</d:displayname><d:resourcetype><d:collection/><` + kind + `/></d:resourcetype>` + extra +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

const (
	principal = "/principals/" + userCanary + "/"
	calHome   = "/calendars/" + userCanary + "/"
	bookHome  = "/addressbooks/" + userCanary + "/"
)

// davFake answers the whole discovery chain of one identity.
func davFake(t *testing.T) func(*http.Request) (*http.Response, error) {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/":
			return xmlResponse(207, multistatus(linkXML("/", "d:current-user-principal", principal))), nil
		case principal:
			return xmlResponse(207, multistatus(
				linkXML(principal, "c:calendar-home-set", calHome)+
					linkXML(principal, "card:addressbook-home-set", bookHome))), nil
		case calHome:
			return xmlResponse(207, multistatus(
				collectionXML(calHome, "d:collection", "Home"),
				collectionXML(calHome+"work/", "c:calendar", "Work"),
				collectionXML(calHome+"private/", "c:calendar", "Private "+nameCanary),
				collectionXML(calHome+"other/", "c:calendar", "Other"),
				collectionXML(calHome+"books/", "card:addressbook", "Not a calendar"))), nil
		case bookHome:
			return xmlResponse(207, multistatus(
				collectionXML(bookHome, "d:collection", "Home"),
				collectionXML(bookHome+"main/", "card:addressbook", "Main"),
				collectionXML(bookHome+"hidden/", "card:addressbook", "Hidden"))), nil
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		return xmlResponse(404, bodyCanary), nil
	}
}

func resolved(targets ...string) *config.Resolved {
	return &config.Resolved{
		Name: "dav", Provider: Provider, BaseURL: origin, Credential: "dav", Targets: targets,
		Secrets: config.Credential{Type: config.CredentialTypeEnv,
			Values: map[string]string{roleUserID: userEnv, roleAppPassword: passEnv}},
	}
}

func resolver(red *redact.Redactor) *secret.Resolver {
	return secret.NewWith(func(name string) string {
		switch name {
		case userEnv:
			return userCanary
		case passEnv:
			return passCanary
		}
		return ""
	}, nil, nil, red)
}

func open(t *testing.T, targets ...string) (*Client, *redact.Redactor) {
	t.Helper()
	red := &redact.Redactor{}
	c, err := Open(context.Background(), resolved(targets...), resolver(red), red)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	return c, red
}

func ids(page *CollectionsPage) []string {
	out := []string{}
	for _, c := range page.Collections {
		out = append(out, c.ID)
	}
	sort.Strings(out)
	return out
}

func classOf(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return ""
}

func TestDiscoveryChainListsOnlyAllowlistedCalendars(t *testing.T) {
	calls := serve(t, davFake(t))
	c, _ := open(t, "calendar/work", "calendar/private", "calendar/missing", "addressbook/main")
	page, err := c.list(context.Background(), true)
	if err != nil {
		t.Fatalf("list() = %v", err)
	}
	if got := ids(page); !reflect.DeepEqual(got, []string{"private", "work"}) {
		t.Errorf("ids = %v, want only the allow-listed calendars (not other, not the address book)", got)
	}
	var work Collection
	for _, col := range page.Collections {
		if col.ID == "work" {
			work = col
		}
	}
	if work.Name != "Work" || work.Description != "desc Work" || work.Color != "#FF8800FF" {
		t.Errorf("work = %+v", work)
	}
	want := []string{"/", principal, calHome}
	var paths []string
	for _, call := range *calls {
		paths = append(paths, strings.TrimPrefix(call.path, "sync.infomaniak.com"))
		if call.method != "PROPFIND" || call.auth != basic() {
			t.Errorf("call = %+v", call)
		}
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want the discovery chain %v", paths, want)
	}
	if (*calls)[2].depth != "1" || (*calls)[0].depth != "0" || (*calls)[1].depth != "0" {
		t.Errorf("depths = %+v", *calls)
	}
}

func basic() string {
	return "Basic QUIxMjM0NTpjYW5hcnktaW5mb21hbmlhay1kYXYtYXBwLXBhc3N3b3JkLTRlMWM="
}

func TestAddressbooksListOnlyAllowlisted(t *testing.T) {
	serve(t, davFake(t))
	c, _ := open(t, "calendar/work", "addressbook/main")
	page, err := c.list(context.Background(), false)
	if err != nil {
		t.Fatalf("list() = %v", err)
	}
	if got := ids(page); !reflect.DeepEqual(got, []string{"main"}) || page.Collections[0].Color != "" {
		t.Errorf("page = %+v", page)
	}
}

func TestMissingKindListsEmptyWithoutAnyRequest(t *testing.T) {
	calls := serve(t, davFake(t))
	out, err := invokeCalendarsList(context.Background(), resolved("addressbook/main"), nil, nil, nil)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	page := out.(*CollectionsPage)
	if page.Count != 0 || page.Collections == nil || len(*calls) != 0 {
		t.Errorf("page = %+v, calls = %v", page, *calls)
	}
}

func TestHrefOutsideHomeSetIsRejected(t *testing.T) {
	for name, href := range map[string]string{
		"sibling path":    "/elsewhere/work/",
		"foreign host":    "https://evil.example" + calHome + "work/",
		"other scheme":    "http://sync.infomaniak.com" + calHome + "work/",
		"dot dot":         calHome + "../x/",
		"encoded dot dot": calHome + "%2e%2e/",
		"encoded slash":   calHome + "a%2Fb/",
		"nested":          calHome + "work/sub/",
		"query":           calHome + "work/?x=1",
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == calHome {
					return xmlResponse(207, multistatus(collectionXML(href, "c:calendar", "Work"))), nil
				}
				return davFake(t)(r)
			})
			c, _ := open(t, "calendar/work")
			if _, err := c.list(context.Background(), true); classOf(err) != provider.ClassInvalidResponse {
				t.Errorf("err = %v, want an invalid response", err)
			}
		})
	}
}

func TestDiscoveryLocationOnForeignHostIsRejected(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		return xmlResponse(207, multistatus(linkXML("/", "d:current-user-principal", "https://evil.example/p/"))), nil
	})
	c, _ := open(t, "calendar/work")
	if _, err := c.list(context.Background(), true); classOf(err) != provider.ClassInvalidResponse || len(*calls) != 1 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		resp := xmlResponse(301, bodyCanary)
		resp.Header.Set("Location", "https://evil.example/")
		return resp, nil
	})
	c, _ := open(t, "calendar/work")
	_, err := c.list(context.Background(), true)
	if classOf(err) != provider.ClassProviderError || len(*calls) != 1 || strings.Contains(err.Error(), "evil") {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestUnauthorizedIsAuthWithoutProviderText(t *testing.T) {
	serve(t, func(r *http.Request) (*http.Response, error) { return xmlResponse(401, bodyCanary), nil })
	c, red := open(t, "calendar/work")
	_, err := c.list(context.Background(), true)
	if classOf(err) != provider.ClassAuth || strings.Contains(err.Error(), bodyCanary) ||
		strings.Contains(red.Apply("x "+passCanary+" "+basic()), passCanary) {
		t.Errorf("err = %v", err)
	}
	class, err := TestConnection(context.Background(), resolved("calendar/work"), resolver(nil), red)
	if err != nil || class != provider.ClassAuth {
		t.Errorf("class = %v, err = %v", class, err)
	}
}

func TestResponseSizeIsCapped(t *testing.T) {
	serve(t, func(r *http.Request) (*http.Response, error) {
		return xmlResponse(207, `<d:multistatus xmlns:d="DAV:">`+strings.Repeat(" ", maxResponseBytes+10)), nil
	})
	c, _ := open(t, "calendar/work")
	if _, err := c.list(context.Background(), true); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestEntryCountAndDepthAreCapped(t *testing.T) {
	many := make([]string, maxEntries+1)
	for i := range many {
		many[i] = `<d:response><d:href>/x/</d:href></d:response>`
	}
	if _, err := parseMultiStatus("t", []byte(multistatus(many...))); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("entries err = %v", err)
	}
	deep := `<d:multistatus xmlns:d="DAV:">` + strings.Repeat("<d:x>", 40) + strings.Repeat("</d:x>", 40) + `</d:multistatus>`
	if _, err := parseMultiStatus("t", []byte(deep)); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("depth err = %v", err)
	}
}

func TestProviderStringsAreBounded(t *testing.T) {
	long := strings.Repeat("ä", 1000)
	serve(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == calHome {
			return xmlResponse(207, multistatus(collectionXML(calHome+"work/", "c:calendar", long+"\u0085"))), nil
		}
		return davFake(t)(r)
	})
	c, _ := open(t, "calendar/work")
	page, err := c.list(context.Background(), true)
	if err != nil || len(page.Collections) != 1 {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	if got := page.Collections[0]; len(got.Name) > maxNameLength || len(got.Description) > maxDescriptionLength ||
		strings.ContainsRune(got.Name, 0x85) {
		t.Errorf("collection = %d/%d bytes", len(got.Name), len(got.Description))
	}
}

func TestParseTargets(t *testing.T) {
	for _, bad := range []string{"", "calendar/", "calendar/a/b", "calendar/..", "calendar/a%2Fb", "calendar/x\x01",
		"folder/x", "addressbook/" + strings.Repeat("a", 129)} {
		if validateTarget(bad) == nil {
			t.Errorf("validateTarget(%q) accepted", bad)
		}
	}
	for _, good := range []string{"calendar/work", "addressbook/default-1"} {
		if err := validateTarget(good); err != nil {
			t.Errorf("validateTarget(%q) = %v", good, err)
		}
	}
	if _, err := parseScope(nil); err == nil {
		t.Error("an empty target list was accepted")
	}
	if _, err := parseScope([]string{"calendar/a", "calendar/a"}); err == nil {
		t.Error("a duplicate was accepted")
	}
	if _, err := parseScope([]string{"calendar/a", "addressbook/a"}); err != nil {
		t.Errorf("same id in both kinds = %v", err)
	}
}

func TestBaseURLIsFixed(t *testing.T) {
	for raw, ok := range map[string]bool{origin: true, origin + "/": true, "https://evil.example": false,
		"http://sync.infomaniak.com": false, "https://sync.infomaniak.com.evil.example": false} {
		if (validBaseURL(raw) == nil) != ok {
			t.Errorf("validBaseURL(%q) = %v", raw, validBaseURL(raw))
		}
	}
}

func TestRegisterPublishesFixedToolIDs(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	var got []string
	for _, d := range reg.Provider(Provider) {
		got = append(got, d.ID)
		writes := d.ID == eventsCreate.ID || d.ID == eventsUpdate.ID || d.ID == eventsDelete.ID
		if writes {
			if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
				d.Risk.Idempotency == "" || d.Risk.Idempotency == capability.IdempotencyUnknown ||
				d.Risk.DataSensitivity != eventsSensitivity || d.RequiresToolAllowList != (d.ID == eventsDelete.ID) {
				t.Errorf("%s = %+v", d.ID, d.Risk)
			}
		} else if strings.HasPrefix(d.ID, Provider+".contacts.") {
			if d.Risk.DataSensitivity != contactsSensitivity || !d.Risk.OpenWorld || d.Risk.Effect != capability.EffectRead {
				t.Errorf("%s = %+v", d.ID, d.Risk)
			}
		} else if d.Risk.Effect != capability.EffectRead || d.Risk.Confirmation != capability.ConfirmationNone {
			t.Errorf("%s = %+v", d.ID, d.Risk)
		}
	}
	sort.Strings(got)
	if want := []string{"infomaniakdav.addressbooks.list", "infomaniakdav.calendars.list",
		"infomaniakdav.contacts.get", "infomaniakdav.contacts.list",
		"infomaniakdav.events.create", "infomaniakdav.events.delete", "infomaniakdav.events.get",
		"infomaniakdav.events.list", "infomaniakdav.events.update"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tool IDs = %v, want %v", got, want)
	}
	metadata, ok := reg.ProviderMetadata(Provider)
	if !ok || !metadata.Target.Required || len(metadata.Target.Kinds) != 2 || len(metadata.Profiles) != 2 ||
		!metadata.Profiles[0].Recommended || metadata.Profiles[1].Recommended || len(metadata.Profiles[0].Tools) != 6 ||
		slices.Contains(metadata.Profiles[1].Tools, eventsDelete.ID) || len(metadata.SecretRoles) != 2 ||
		metadata.SecretRoles[0].Name != "user-id" || metadata.SecretRoles[1].Name != "app-password" {
		t.Errorf("metadata = %+v", metadata)
	}
}

// Through the application core, the result carries no secret and the invoke path needs no arguments.
func TestInvokeThroughCore(t *testing.T) {
	serve(t, davFake(t))
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"dav": {Provider: Provider}},
		Credentials: map[string]config.Credential{"dav": resolved().Secrets},
		Connections: map[string]config.Connection{"main": {Service: "dav", Credential: "dav",
			Permissions: config.Permissions(), Targets: []string{"calendar/work", "addressbook/main"}}},
	}
	red := &redact.Redactor{}
	core := application.New(reg, cfg, resolver(red), red)
	resp, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "infomaniakdav.calendars.list", Connection: "main", Arguments: json.RawMessage(`{}`)})
	if err != nil || !strings.Contains(string(resp.Result), `"id":"work"`) ||
		strings.Contains(string(resp.Result), passCanary) {
		t.Errorf("result = %s, err = %v", resp.Result, err)
	}
	if _, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "infomaniakdav.calendars.list", Connection: "main",
		Arguments: json.RawMessage(`{"url":"https://evil.example"}`)}); err == nil {
		t.Error("a free argument was accepted")
	}
}
