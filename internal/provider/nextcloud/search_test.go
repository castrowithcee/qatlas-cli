package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const searchPrefix = `<?xml version="1.0" encoding="UTF-8"?><d:searchrequest xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:basicsearch>` +
	`<d:select><d:prop><d:displayname/><d:resourcetype/><d:getcontenttype/><d:getcontentlength/><d:getlastmodified/><d:getetag/><oc:fileid/><oc:size/><oc:permissions/></d:prop></d:select>` +
	`<d:from><d:scope><d:href>/files/alice/Reports</d:href><d:depth>infinity</d:depth></d:scope></d:from><d:where>`

func searchBodyWith(where string, limit string) string {
	return searchPrefix + where + `</d:where><d:orderby/><d:limit><d:nresults>` + limit + `</d:nresults></d:limit></d:basicsearch></d:searchrequest>`
}

func invokeSearch(args string) (any, error) {
	red := &redact.Redactor{}
	return invokeFilesSearch(context.Background(), reportsConnection(), resolver(red), red, json.RawMessage(args))
}

func emptyMultistatus() *http.Response { return xmlResponse(http.StatusMultiStatus, multistatus()) }

// Golden bodies of every filter. A literal passes through XML escaping, and the LIKE wildcards and the
// backslash escape character of Nextcloud's d:like are masked with a backslash.
func TestSearchBodyPerFilter(t *testing.T) {
	cases := []struct{ name, args, where, limit string }{
		{"name", `{"name_contains":"re_port"}`,
			`<d:like><d:prop><d:displayname/></d:prop><d:literal>%re\_port%</d:literal></d:like>`, "50"},
		{"name specials", `{"name_contains":"100%_\\<a&b>\"'","limit":7}`,
			`<d:like><d:prop><d:displayname/></d:prop><d:literal>%100\%\_\\&lt;a&amp;b&gt;&#34;&#39;%</d:literal></d:like>`, "7"},
		{"content type", `{"content_type_prefix":"image/"}`,
			`<d:like><d:prop><d:getcontenttype/></d:prop><d:literal>image/%</d:literal></d:like>`, "50"},
		{"modified", `{"modified_after":"2026-01-02T03:04:05+01:00","modified_before":"2026-02-01T00:00:00Z"}`,
			`<d:and><d:gt><d:prop><d:getlastmodified/></d:prop><d:literal>Fri, 02 Jan 2026 02:04:05 GMT</d:literal></d:gt>` +
				`<d:lt><d:prop><d:getlastmodified/></d:prop><d:literal>Sun, 01 Feb 2026 00:00:00 GMT</d:literal></d:lt></d:and>`, "50"},
		{"size", `{"size_min":0,"size_max":2048}`,
			`<d:and><d:gte><d:prop><oc:size/></d:prop><d:literal>0</d:literal></d:gte>` +
				`<d:lte><d:prop><oc:size/></d:prop><d:literal>2048</d:literal></d:lte></d:and>`, "50"},
		{"folder", `{"type":"folder"}`, `<d:is-collection/>`, "50"},
		{"file", `{"type":"file","limit":200}`, `<d:not><d:is-collection/></d:not>`, "200"},
		{"favorite", `{"favorite":true}`, `<d:eq><d:prop><oc:favorite/></d:prop><d:literal>1</d:literal></d:eq>`, "50"},
		{"not favorite", `{"favorite":false}`, `<d:eq><d:prop><oc:favorite/></d:prop><d:literal>0</d:literal></d:eq>`, "50"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := serve(t, func(*http.Request) (*http.Response, error) { return emptyMultistatus(), nil })
			if _, err := invokeSearch(c.args); err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 {
				t.Fatalf("calls = %d", len(*calls))
			}
			got := (*calls)[0]
			if got.method != "SEARCH" || got.url.Path != "/remote.php/dav/" || got.auth != basicAuth(aliceUser, aliceToken) {
				t.Errorf("request = %s %s", got.method, got.url)
			}
			if want := searchBodyWith(c.where, c.limit); got.body != want {
				t.Errorf("body =\n%s\nwant\n%s", got.body, want)
			}
		})
	}
}

func TestSearchScopeFollowsRootAndInstallPath(t *testing.T) {
	red := &redact.Redactor{}
	conn := resolvedConnection("shared", "cloud-reader", aliceUserEnv, aliceTokenEnv, partnerInstance, "Team A/Q&A")
	calls := serve(t, func(*http.Request) (*http.Response, error) { return emptyMultistatus(), nil })
	if _, err := invokeFilesSearch(context.Background(), conn, resolver(red), red, json.RawMessage(`{"type":"file"}`)); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.url.Path != "/nextcloud/remote.php/dav/" || !strings.Contains(c.body, `<d:href>/files/alice/Team%20A/Q&amp;A</d:href>`) {
		t.Errorf("url = %s, body = %s", c.url, c.body)
	}
}

func TestSearchFiltersAndPathsAreRefusedBeforeIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	bad := []string{
		`{}`, `{"limit":50}`, `{"name_contains":"a\u0001"}`, `{"content_type_prefix":"a b"}`,
		`{"modified_after":"yesterday"}`, `{"modified_after":"2026-02-01T00:00:00Z","modified_before":"2026-01-01T00:00:00Z"}`,
		`{"size_min":-1}`, `{"size_min":5,"size_max":4}`, `{"type":"link"}`, `{"type":"file","limit":201}`, `{"type":"file","limit":-1}`,
	}
	for _, args := range bad {
		if _, err := invokeFilesSearch(context.Background(), reportsConnection(), res, &redact.Redactor{}, json.RawMessage(args)); err == nil {
			t.Errorf("%s accepted", args)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func searchAnswer(entries ...string) *http.Response {
	return xmlResponse(http.StatusMultiStatus, multistatus(entries...))
}

func TestSearchReportsOnlyHitsBelowTheRoot(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return searchAnswer(
			folderXML(aliceRoot+"/", "Reports", "1", "9"),
			fileXML(aliceRoot+"/z.pdf", "z.pdf", "2", "10"),
			fileXML(aliceRoot+"/a/b.pdf", "b.pdf", "3", "11"),
			fileXML("/remote.php/dav/files/alice/Other/x.pdf", "x.pdf", "4", "12"),
			fileXML("/remote.php/dav/files/alice/Reportsx/y.pdf", "y.pdf", "5", "12"),
			fileXML("/remote.php/dav/files/bobby/Reports/y.pdf", "y.pdf", "6", "12"),
			fileXML("https://evil.example.invalid"+aliceRoot+"/e.pdf", "e.pdf", "7", "12"),
			fileXML(aliceRoot+"/%2e%2e/f.pdf", "f.pdf", "8", "12"),
		), nil
	})
	got, err := invokeSearch(`{"name_contains":"pdf"}`)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(*entryListResult)
	if result.Count != 2 || result.Entries[0].Path != "a/b.pdf" || result.Entries[1].Path != "z.pdf" || result.Truncated {
		t.Errorf("result = %+v", result)
	}
}

func TestSearchTruncatesAtTheLimit(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return searchAnswer(fileXML(aliceRoot+"/a.pdf", "a", "2", "1"), fileXML(aliceRoot+"/b.pdf", "b", "3", "1")), nil
	})
	got, err := invokeSearch(`{"type":"file","limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if r := got.(*entryListResult); r.Count != 2 || !r.Truncated {
		t.Errorf("result = %+v", r)
	}
	// A server that ignores the limit is cut locally.
	got, err = invokeSearch(`{"type":"file","limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if r := got.(*entryListResult); r.Count != 1 || !r.Truncated {
		t.Errorf("result = %+v", r)
	}
	got, err = invokeSearch(`{"type":"file","limit":3}`)
	if err != nil {
		t.Fatal(err)
	}
	if r := got.(*entryListResult); r.Count != 2 || r.Truncated {
		t.Errorf("result = %+v", r)
	}
}

func TestFavoritesListSendsOneReportOnTheRoot(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return searchAnswer(
			folderXML(aliceRoot+"/", "Reports", "1", "9"),
			fileXML(aliceRoot+"/fav.pdf", "fav.pdf", "2", "10"),
			fileXML("/remote.php/dav/files/alice/Other/x.pdf", "x.pdf", "4", "12"),
		), nil
	})
	red := &redact.Redactor{}
	got, err := invokeFavoritesList(context.Background(), reportsConnection(), resolver(red), red, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if r := got.(*entryListResult); r.Count != 1 || r.Entries[0].Path != "fav.pdf" {
		t.Errorf("result = %+v", r)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?><oc:filter-files xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><oc:filter-rules>` +
		`<oc:favorite>1</oc:favorite></oc:filter-rules><d:prop><d:displayname/><d:resourcetype/><d:getcontenttype/><d:getcontentlength/>` +
		`<d:getlastmodified/><d:getetag/><oc:fileid/><oc:size/><oc:permissions/></d:prop></oc:filter-files>`
	c := (*calls)[0]
	if len(*calls) != 1 || c.method != "REPORT" || c.url.Path != aliceRoot || c.body != want {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestFavoritesListTruncatesAtFiveHundred(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		var entries []string
		for i := 0; i < maxEntries+3; i++ {
			entries = append(entries, fileXML(aliceRoot+"/f"+strconv.Itoa(i)+".txt", "n", "2", "1"))
		}
		return searchAnswer(entries...), nil
	})
	red := &redact.Redactor{}
	got, err := invokeFavoritesList(context.Background(), reportsConnection(), resolver(red), red, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if r := got.(*entryListResult); r.Count != maxEntries || !r.Truncated {
		t.Errorf("count = %d, truncated = %v", r.Count, r.Truncated)
	}
}

func favorite(args string) error {
	red := &redact.Redactor{}
	_, err := invokeFilesFavorite(capability.WithConfirmed(context.Background()), reportsConnection(), resolver(red), red, json.RawMessage(args))
	return err
}

func proppatchAnswer(status string) *http.Response {
	return xmlResponse(http.StatusMultiStatus, multistatus(`<d:response><d:href>`+aliceRoot+`/2026/q1.pdf</d:href><d:propstat><d:prop><oc:favorite/></d:prop>`+
		`<d:status>`+status+`</d:status></d:propstat></d:response>`))
}

func TestFavoriteSendsOneProppatch(t *testing.T) {
	for value, flag := range map[string]string{"true": "1", "false": "0"} {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return proppatchAnswer("HTTP/1.1 200 OK"), nil })
		if err := favorite(`{"path":"2026/q1.pdf","favorite":` + value + `}`); err != nil {
			t.Fatal(err)
		}
		want := `<?xml version="1.0" encoding="UTF-8"?><d:propertyupdate xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:set><d:prop>` +
			`<oc:favorite>` + flag + `</oc:favorite></d:prop></d:set></d:propertyupdate>`
		c := (*calls)[0]
		if len(*calls) != 1 || c.method != "PROPPATCH" || c.url.Path != aliceRoot+"/2026/q1.pdf" || c.body != want {
			t.Errorf("%s: calls = %+v", value, *calls)
		}
	}
}

func TestFavoriteRefusesBadInputBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, args := range []string{`{"path":"","favorite":true}`, `{"path":"../x","favorite":true}`, `{"path":"/x","favorite":true}`,
		`{"path":"a%2e","favorite":true}`, `{"path":"x"}`, `{"favorite":true}`} {
		if _, err := invokeFilesFavorite(capability.WithConfirmed(context.Background()), reportsConnection(), res, &redact.Redactor{}, json.RawMessage(args)); err == nil {
			t.Errorf("%s accepted", args)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestFavoritePropertyErrorIsClear(t *testing.T) {
	for _, status := range []string{"HTTP/1.1 403 Forbidden", "HTTP/1.1 424 Failed Dependency", "HTTP/1.1 500 Internal Server Error"} {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return proppatchAnswer(status), nil })
		err := favorite(`{"path":"2026/q1.pdf","favorite":true}`)
		var perr *provider.Error
		if !errors.As(err, &perr) || perr.Message != messageFavoriteRefused || len(*calls) != 1 {
			t.Errorf("%s: err = %v", status, err)
		}
	}
}

func TestFavoriteUnclearOutcomeIsNeverRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"503":     func() (*http.Response, error) { return status(503), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbage": func() (*http.Response, error) { return xmlResponse(207, "<not-xml"), nil },
		"body": func() (*http.Response, error) {
			return &http.Response{StatusCode: 207, Header: http.Header{}, Body: failingBody{}}, nil
		},
		"foreign": func() (*http.Response, error) {
			return xmlResponse(207, multistatus(`<d:response><d:href>/elsewhere</d:href><d:propstat><d:prop/><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)), nil
		},
	}
	for name, answer := range answers {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return answer() })
		err := favorite(`{"path":"2026/q1.pdf","favorite":true}`)
		if err == nil || !strings.Contains(err.Error(), "may have been changed") || len(*calls) != 1 || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v, calls = %d", name, err, len(*calls))
		}
	}
	for _, code := range []int{301, 307, 403, 404, 409} {
		calls := serve(t, func(*http.Request) (*http.Response, error) { return status(code), nil })
		err := favorite(`{"path":"2026/q1.pdf","favorite":true}`)
		if err == nil || strings.Contains(err.Error(), "may have been changed") || len(*calls) != 1 || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%d: err = %v", code, err)
		}
	}
}

func TestSearchAndFavoritesRefuseRedirects(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		r := status(302)
		r.Header.Set("Location", redirectTarget)
		return r, nil
	})
	red := &redact.Redactor{}
	_, err1 := invokeSearch(`{"type":"file"}`)
	_, err2 := invokeFavoritesList(context.Background(), reportsConnection(), resolver(red), red, json.RawMessage(`{}`))
	for _, err := range []error{err1, err2} {
		if err == nil || !strings.Contains(err.Error(), messageRedirect) || strings.Contains(err.Error(), "outside-canary") {
			t.Errorf("err = %v", err)
		}
	}
}

func TestSearchToolsRisksAndProfiles(t *testing.T) {
	reg := registry(t)
	for _, d := range reg.Provider(Provider) {
		switch d.ID {
		case "nextcloud.files.search", "nextcloud.favorites.list":
			if d.Risk != nextcloudReadRisk || d.Group != "files" || d.RequiresToolAllowList {
				t.Errorf("%s = %+v", d.ID, d)
			}
		case "nextcloud.files.favorite":
			r := d.Risk
			if r.Effect != capability.EffectUpdate || r.Idempotency != capability.IdempotencyIdempotent || !r.OpenWorld ||
				r.Confirmation != capability.ConfirmationRequired || r.DataSensitivity != dataSensitivity ||
				d.Group != "files" || d.RequiresToolAllowList {
				t.Errorf("%s = %+v", d.ID, d)
			}
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		if p.ID == "talk-read" {
			continue
		}
		got := strings.Join(p.Tools, " ")
		if !strings.Contains(got, "nextcloud.files.search") || !strings.Contains(got, "nextcloud.favorites.list") ||
			strings.Contains(got, "nextcloud.files.favorite") {
			t.Errorf("profile %s = %s", p.ID, got)
		}
	}
}
