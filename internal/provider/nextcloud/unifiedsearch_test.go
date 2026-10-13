package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const searchProvidersJSON = `[{"id":"files","name":"Files","order":5},{"id":"bad/id","name":"x"},{"id":"mail","name":"Mail"}]`

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
		`{"provider":"files","term":"a","cursor":"` + strings.Repeat("1", 129) + `"}`,
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

func TestSearchProvidersSkipsUnusableIDs(t *testing.T) {
	searchServer(t, nil)
	got, err := runAccount(invokeSearchProviders, `{}`)
	list := got.(*SearchProviderList)
	if err != nil || list.Count != 2 || list.Providers[0].ID != "files" || list.Providers[1].ID != "mail" {
		t.Errorf("list = %+v, err = %v", got, err)
	}
}

func TestSearchPassesCursorAndReportsLinksAsTextOnly(t *testing.T) {
	long := strings.Repeat("t", 600)
	calls := searchServer(t, func() *http.Response {
		return ocsResponse(200, ocsData(`{"name":"Files","isPaginated":true,"cursor":25,"entries":[`+
			`{"thumbnailUrl":"`+linkCanary+`/thumb","title":"`+long+`","subline":"in docs","resourceUrl":"`+linkCanary+`/r",`+
			`"icon":"`+linkCanary+`/i","rounded":false,"attributes":{"fileId":"1","path":"`+linkCanary+`/p"}},`+
			`{"title":"two","subline":"","resourceUrl":"/f/2"},{"title":"three"}]}`))
	})
	got, err := runAccount(invokeSearchQuery, `{"provider":"files","term":"a b","limit":2,"cursor":"10"}`)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(*SearchResult)
	if result.Count != 2 || result.NextCursor != "25" || !result.IsPaginated || !result.Truncated ||
		len(result.Entries[0].Title) != 512 || !result.Entries[0].Truncated || result.Entries[0].Link != linkCanary+"/r" {
		t.Fatalf("result = %+v", result)
	}
	out := marshal(got)
	if strings.Contains(out, "thumb") || strings.Contains(out, linkCanary+"/i") || strings.Contains(out, "/p\"") || strings.Contains(out, "fileId") {
		t.Errorf("output carries more than the link text: %s", out)
	}
	q := (*calls)[1].url.Query()
	if len(*calls) != 2 || q.Get("cursor") != "10" || q.Get("limit") != "2" || q.Get("term") != "a b" ||
		!strings.HasSuffix((*calls)[1].url.Path, "/search/providers/files/search") {
		t.Errorf("calls = %+v", *calls)
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
