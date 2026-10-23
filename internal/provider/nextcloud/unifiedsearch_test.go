package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const searchProvidersJSON = `[{"id":"files","appId":"files","name":"Files","icon":"/apps/files/img/app.svg","order":-2,"triggers":["files","f"],` +
	`"filters":{"term":"string","since":"datetime","until":"datetime","person":"person","min-size":"int","max-size":"int","mime":"string","type":"string"},` +
	`"inAppSearch":false},{"id":"bad/id","name":"x"},{"id":"mail","name":"Mail"}]`

func searchServer(t *testing.T, result func() *http.Response) *[]call {
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/search/providers") {
			return ocsResponse(200, ocsData(searchProvidersJSON)), nil
		}
		return result(), nil
	})
}

func TestSearchRefusedWithoutAccountOrWithBadArgumentsBeforeSecretAccess(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	_, err := accountBound(invokeSearchQuery)(capability.WithConfirmed(context.Background()), reportsConnection(), res,
		&redact.Redactor{}, json.RawMessage(`{"provider":"files","term":"a"}`))
	if err == nil {
		t.Error("no error without the account target")
	}
	_, err = accountBound(invokeSearchProviders)(context.Background(), reportsConnection(), res, &redact.Redactor{}, json.RawMessage(`{}`))
	if err == nil {
		t.Error("providers: no error without the account target")
	}
	for _, args := range []string{
		`{"provider":"a/b","term":"a"}`, `{"provider":"a%2f","term":"a"}`, `{"provider":"..","term":"a"}`, `{"provider":"A","term":"a"}`,
		`{"provider":"a\\b","term":"a"}`, `{"provider":"","term":"a"}`, `{"provider":"files","term":""}`, `{"provider":"files","term":"  "}`,
		`{"provider":"files","term":"a\u0001"}`, `{"provider":"files","term":"` + strings.Repeat("a", 257) + `"}`,
		`{"provider":"files","term":"a","limit":26}`, `{"provider":"files","term":"a","limit":0}`,
		`{"provider":"files","term":"a","cursor":"a/b"}`, `{"provider":"files","term":"a","cursor":"a b"}`,
		`{"provider":"files","term":"a","cursor":"` + strings.Repeat("1", 193) + `"}`,
	} {
		if _, err := runAccount(invokeSearchQuery, args); err == nil {
			t.Errorf("%s: accepted", args)
		}
	}
	if touched {
		t.Error("secrets were read before local refusal")
	}
}

func TestSearchRefusesUnofferedProviderWithoutSearch(t *testing.T) {
	calls := searchServer(t, func() *http.Response { return ocsResponse(200, ocsData(`{}`)) })
	_, err := runAccount(invokeSearchQuery, `{"provider":"calendar","term":"a"}`)
	if err == nil || strings.Contains(err.Error(), "calendar") {
		t.Errorf("err = %v", err)
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0].url.Path, "/search/providers") {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestSearchProvidersReportsEveryDocumentedField(t *testing.T) {
	searchServer(t, nil)
	got, err := runAccount(invokeSearchProviders, `{}`)
	list := got.(*SearchProviderList)
	if err != nil || list.Count != 2 || list.Providers[0].ID != "files" || list.Providers[1].ID != "mail" {
		t.Fatalf("list = %+v, err = %v", got, err)
	}
	out := marshal(got)
	want := `{"id":"files","name":"Files","app_id":"files","icon":"/apps/files/img/app.svg","order":-2,"triggers":["files","f"],` +
		`"filters":{"max-size":"int","mime":"string","min-size":"int","person":"person","since":"datetime","term":"string",` +
		`"type":"string","until":"datetime"},"in_app_search":false}`
	if !strings.Contains(out, want) || strings.Contains(out, `"mail","name":"Mail","app_id"`) {
		t.Errorf("providers = %s", out)
	}
	checkDeclared(t, searchProvidersTool, []byte(out), searchProvidersTool.OutputSchema)
}

func TestSearchProvidersOmitTooManyFilters(t *testing.T) {
	var filters []string
	for i := 0; i < 25; i++ {
		filters = append(filters, `"f`+itoa(i)+`":"string"`)
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`[{"id":"files","name":"Files","filters":{`+strings.Join(filters, ",")+`}}]`)), nil
	})
	got, err := runAccount(invokeSearchProviders, `{}`)
	if err != nil || marshal(got) != `{"providers":[{"id":"files","name":"Files","omitted_fields":["filters"]}],"count":1}` {
		t.Errorf("got = %s, err = %v", marshal(got), err)
	}
}

func TestSearchPassesCursorAndReportsHitMembersAsText(t *testing.T) {
	long := strings.Repeat("t", 600)
	calls := searchServer(t, func() *http.Response {
		return ocsResponse(200, ocsData(`{"name":"Files","isPaginated":true,"cursor":25,"entries":[`+
			`{"thumbnailUrl":"`+linkCanary+`/thumb","title":"`+long+`","subline":"in docs","resourceUrl":"`+linkCanary+`/r",`+
			`"icon":"`+shareCanary+`","rounded":false,"attributes":{"fileId":"1","path":"/docs/a.txt","accessToken":"tok-canary","share":"`+shareCanary+`"}},`+
			`{"title":"two","subline":"","resourceUrl":"/f/2","icon":"icon-folder","rounded":true},{"title":"three"}]}`))
	})
	got, err := runAccount(invokeSearchQuery, `{"provider":"files","term":"a b","limit":2,"cursor":"10"}`)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(*SearchResult)
	no, yes := false, true
	if result.Count != 2 || result.NextCursor != "25" || !result.IsPaginated || !result.Truncated ||
		len(result.Entries[0].Title) != 512 || !result.Entries[0].Truncated || result.Entries[0].Link != linkCanary+"/r" ||
		result.Entries[0].ThumbnailURL != linkCanary+"/thumb" || result.Entries[0].Icon != "" || !reflect.DeepEqual(result.Entries[0].Rounded, &no) ||
		!reflect.DeepEqual(result.Entries[0].Attributes, map[string]string{"fileId": "1", "path": "/docs/a.txt"}) ||
		result.Entries[1].Icon != "icon-folder" || !reflect.DeepEqual(result.Entries[1].Rounded, &yes) {
		t.Fatalf("result = %+v", result)
	}
	out := marshal(got)
	if strings.Contains(out, "tok-canary") {
		t.Errorf("output carries an access value: %s", out)
	}
	checkDeclared(t, searchQueryTool, []byte(out), searchQueryTool.OutputSchema)
	q := (*calls)[1].url.Query()
	if len(*calls) != 2 || q.Get("cursor") != "10" || q.Get("limit") != "2" || q.Get("term") != "a b" || q.Has("since") || q.Has("person") ||
		!strings.HasSuffix((*calls)[1].url.Path, "/search/providers/files/search") {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestSearchOmitsTooManyAttributes(t *testing.T) {
	var attributes []string
	for i := 0; i < 21; i++ {
		attributes = append(attributes, `"a`+itoa(i)+`":"v"`)
	}
	searchServer(t, func() *http.Response {
		return ocsResponse(200, ocsData(`{"entries":[{"title":"x","attributes":{`+strings.Join(attributes, ",")+`}}]}`))
	})
	got, err := runAccount(invokeSearchQuery, `{"provider":"files","term":"a"}`)
	if err != nil || !strings.Contains(marshal(got), `"entries":[{"title":"x","omitted_fields":["attributes"]}]`) {
		t.Errorf("got = %s, err = %v", marshal(got), err)
	}
}

func TestSearchDropsUnusableResponseCursorAndDefaultsLimit(t *testing.T) {
	calls := searchServer(t, func() *http.Response {
		return ocsResponse(200, ocsData(`{"isPaginated":true,"cursor":"a/b?x","entries":[]}`))
	})
	got, err := runAccount(invokeSearchQuery, `{"provider":"mail","term":"a"}`)
	if err != nil || got.(*SearchResult).NextCursor != "" || (*calls)[1].url.Query().Get("limit") != "10" {
		t.Errorf("got = %+v, err = %v", got, err)
	}
}

func TestSearchRedirectIsClear(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return status(302), nil })
	for name, h := range map[string]tool{"providers": invokeSearchProviders, "query": invokeSearchQuery} {
		if _, err := runAccount(h, `{"provider":"files","term":"a"}`); err == nil || !strings.Contains(err.Error(), "redirect") ||
			strings.Contains(err.Error(), redirectTarget) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSearchFiltersAreSentOnlyWhenTheProviderDeclaresThem(t *testing.T) {
	calls := searchServer(t, func() *http.Response { return ocsResponse(200, ocsData(`{"entries":[]}`)) })
	_, err := runAccount(invokeSearchQuery, `{"provider":"files","term":"a","since":"2026-01-02T03:04:05+01:00","until":"2026-02-01T00:00:00Z",`+
		`"person":"bob","min_size":0,"max_size":100,"mime":"image/","type":"file"}`)
	if err != nil {
		t.Fatal(err)
	}
	q := (*calls)[1].url.Query()
	want := map[string]string{"since": "2026-01-02T02:04:05Z", "until": "2026-02-01T00:00:00Z", "person": "bob", "min-size": "0",
		"max-size": "100", "mime": "image/", "type": "file"}
	for name, value := range want {
		if q.Get(name) != value {
			t.Errorf("%s = %q, want %q", name, q.Get(name), value)
		}
	}
	*calls = nil
	for _, args := range []string{`{"provider":"mail","term":"a","since":"2026-01-02T03:04:05Z"}`, `{"provider":"mail","term":"a","min_size":1}`,
		`{"provider":"mail","term":"a","type":"x"}`} {
		if _, err := runAccount(invokeSearchQuery, args); err == nil || !strings.Contains(err.Error(), "does not offer the filter") {
			t.Errorf("%s: err = %v", args, err)
		}
	}
	for _, c := range *calls {
		if !strings.HasSuffix(c.url.Path, "/search/providers") {
			t.Errorf("a search was sent: %s", c.url.Path)
		}
	}
}

func TestSearchRefusesUnusableFiltersBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for _, bad := range []string{
		`"since":"yesterday"`, `"until":"2026-01-01"`, `"person":"a/b"`, `"person":"` + strings.Repeat("a", 65) + `"`, `"min_size":-1`, `"max_size":-1`,
		`"min_size":5,"max_size":4`, `"mime":"a b"`, `"mime":"` + strings.Repeat("a", 128) + `"`, `"type":"A"`, `"type":"a/b"`,
		`"cursor":"~abc"`,
	} {
		if _, err := runAccount(invokeSearchQuery, `{"provider":"files","term":"a",`+bad+`}`); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestSearchCursorBindsTheFilters(t *testing.T) {
	calls := searchServer(t, func() *http.Response {
		return ocsResponse(200, ocsData(`{"isPaginated":true,"cursor":"c-9","entries":[]}`))
	})
	first, err := runAccount(invokeSearchQuery, `{"provider":"files","term":"a","mime":"image/"}`)
	if err != nil {
		t.Fatal(err)
	}
	cursor := first.(*SearchResult).NextCursor
	if !strings.HasPrefix(cursor, "~") || len(cursor) > 192 {
		t.Fatalf("cursor = %q", cursor)
	}
	if _, err := runAccount(invokeSearchQuery, `{"provider":"files","term":"a","mime":"image/","cursor":"`+cursor+`"}`); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[len(*calls)-1].url.Query().Get("cursor"); got != "c-9" {
		t.Errorf("cursor sent = %q", got)
	}
	before := len(*calls)
	for _, args := range []string{
		`{"provider":"files","term":"a","mime":"text/","cursor":"` + cursor + `"}`, `{"provider":"files","term":"a","cursor":"` + cursor + `"}`,
		`{"provider":"mail","term":"a","mime":"image/","cursor":"` + cursor + `"}`, `{"provider":"files","term":"a","mime":"image/","cursor":"c-9"}`,
	} {
		if _, err := runAccount(invokeSearchQuery, args); err == nil {
			t.Errorf("%s accepted", args)
		}
	}
	if len(*calls) != before {
		t.Errorf("a refused cursor sent %d requests", len(*calls)-before)
	}
	plain, err := runAccount(invokeSearchQuery, `{"provider":"mail","term":"a"}`)
	if err != nil || plain.(*SearchResult).NextCursor != "c-9" {
		t.Errorf("plain = %+v, %v", plain, err)
	}
}
