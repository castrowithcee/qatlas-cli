package restjson_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/restjson"
)

// pageOnce runs one request of a list the way a generated tool does: read page_size and cursor, bind the
// paging members into the input, send, and ask the page for the next cursor.
func pageOnce(t *testing.T, e *exchange, o *httpbind.Operation, p httpbind.Pager, args map[string]any, size int, cursor string) (map[string]any, string, error) {
	t.Helper()
	page, err := p.Start(awsquery.Binding("aws.test", args), size, cursor)
	if err != nil {
		return nil, "", err
	}
	bound, err := httpbind.ApplyPage(o.Input, args, page)
	if err != nil {
		t.Fatal(err)
	}
	out, _, failure := call(t, e, o, bound)
	if failure != nil {
		return nil, "", failure
	}
	next, failure := page.Next("aws.test", out)
	if failure != nil {
		return out, "", failure
	}
	return out, next, nil
}

func TestNextTokenInTheQueryOverThreePages(t *testing.T) {
	pages := []string{
		`{"EmailIdentities":[{"IdentityName":"a"}],"NextToken":"t1"}`,
		`{"EmailIdentities":[{"IdentityName":"b"}],"NextToken":"t2"}`,
		`{"EmailIdentities":[{"IdentityName":"c"}]}`,
	}
	args := map[string]any{}
	cursor := ""
	var seen []string
	for i, body := range pages {
		e := &exchange{body: body}
		out, next, err := pageOnce(t, e, listEmailIdentities, listEmailIdentitiesPager, args, 25, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		q, _ := url.ParseQuery(e.got[0].rawQuery)
		wantToken := []string{"", "t1", "t2"}[i]
		if q.Get("NextToken") != wantToken || q.Get("PageSize") != "25" {
			t.Errorf("page %d query = %s", i, e.got[0].rawQuery)
		}
		seen = append(seen, out["identities"].([]any)[0].(map[string]any)["name"].(string))
		if (next == "") != (i == len(pages)-1) {
			t.Fatalf("page %d: next = %q", i, next)
		}
		cursor = next
	}
	if strings.Join(seen, "") != "abc" {
		t.Errorf("seen = %v", seen)
	}
}

func TestNextTokenInTheBodyOverThreePages(t *testing.T) {
	args := map[string]any{"account_id": "123456789012"}
	cursor := ""
	for i, body := range []string{`{"Regions":[],"NextToken":"r1"}`, `{"Regions":[{"RegionName":"eu-west-1"}],"NextToken":"r2"}`, `{"Regions":[{"RegionName":"us-east-1"}],"NextToken":""}`} {
		e := &exchange{body: body}
		_, next, err := pageOnce(t, e, listRegions, listRegionsPager, args, 10, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		var sent map[string]any
		if err := json.Unmarshal([]byte(e.got[0].body), &sent); err != nil {
			t.Fatal(err)
		}
		wantToken := []any{nil, "r1", "r2"}[i]
		if sent["NextToken"] != wantToken || sent["MaxResults"] != float64(10) || sent["AccountId"] != "123456789012" {
			t.Errorf("page %d body = %s", i, e.got[0].body)
		}
		// An empty NextToken is how a service says it has no more.
		if (next == "") != (i == 2) {
			t.Fatalf("page %d: next = %q", i, next)
		}
		cursor = next
	}
}

func TestEmptyListEndsThePaging(t *testing.T) {
	out, next, err := pageOnce(t, &exchange{body: `{"EmailIdentities":[]}`}, listEmailIdentities, listEmailIdentitiesPager, map[string]any{}, 0, "")
	if err != nil || next != "" || len(out["identities"].([]any)) != 0 {
		t.Errorf("out = %v, next = %q, err = %v", out, next, err)
	}
	// Nothing at all is an empty list too, and without PageSize none is sent.
	e := &exchange{body: `{}`}
	if _, next, err = pageOnce(t, e, listEmailIdentities, listEmailIdentitiesPager, map[string]any{}, 0, ""); err != nil || next != "" || e.got[0].rawQuery != "" {
		t.Errorf("next = %q, err = %v, query = %q", next, err, e.got[0].rawQuery)
	}
}

func TestManipulatedAndForeignCursorsAreRefused(t *testing.T) {
	_, next, err := pageOnce(t, &exchange{body: `{"NextToken":"t1"}`}, listEmailIdentities, listEmailIdentitiesPager, map[string]any{}, 0, "")
	if err != nil || next == "" {
		t.Fatalf("next = %q, err = %v", next, err)
	}
	e := &exchange{body: `{}`}
	for name, cursor := range map[string]string{
		"altered":   next[:len(next)-1] + "A",
		"garbage":   "!!!",
		"truncated": next[:10],
		"appended":  next + "AA",
	} {
		if _, _, err := pageOnce(t, e, listEmailIdentities, listEmailIdentitiesPager, map[string]any{}, 0, cursor); err == nil {
			t.Errorf("%s cursor was accepted", name)
		} else if _, ok := err.(*provider.InvalidRequestError); !ok {
			t.Errorf("%s: error %T, want InvalidRequestError", name, err)
		}
	}
	// A cursor of another list (other arguments) is refused as well.
	if _, _, err := pageOnce(t, e, listEmailIdentities, listEmailIdentitiesPager, map[string]any{"other": 1}, 0, next); err == nil {
		t.Error("a cursor of other arguments was accepted")
	}
	if len(e.got) != 0 {
		t.Errorf("a refused cursor still sent %d requests", len(e.got))
	}
	// The same arguments continue.
	if _, _, err := pageOnce(t, &exchange{body: `{}`}, listEmailIdentities, listEmailIdentitiesPager, map[string]any{}, 7, next); err != nil {
		t.Errorf("the cursor of this list was refused: %v", err)
	}
}

func TestPagingEdgeCases(t *testing.T) {
	// A page size without a size parameter, and a size above the maximum, are refused before a request.
	noSize := httpbind.Pager{TokenIn: "NextToken", TokenOut: "next_token"}
	if _, _, err := pageOnce(t, &exchange{}, listEmailIdentities, noSize, map[string]any{}, 5, ""); err == nil {
		t.Error("page_size was accepted without a size parameter")
	}
	if _, _, err := pageOnce(t, &exchange{}, listEmailIdentities, listEmailIdentitiesPager, map[string]any{}, 5000, ""); err == nil {
		t.Error("an oversized page_size was accepted")
	}
	// An answer that repeats the token it was asked with would loop, so it is invalid.
	_, next, err := pageOnce(t, &exchange{body: `{"NextToken":"t1"}`}, listEmailIdentities, listEmailIdentitiesPager, map[string]any{}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pageOnce(t, &exchange{body: `{"NextToken":"t1"}`}, listEmailIdentities, listEmailIdentitiesPager, map[string]any{}, 0, next); err == nil {
		t.Error("a repeated token was accepted")
	}
	// The input must have the paging members, and the caller must not set them.
	page, _ := listEmailIdentitiesPager.Start(nil, 5, "")
	if _, err := httpbind.ApplyPage(getEmailIdentity.Input, nil, page); err == nil {
		t.Error("ApplyPage accepted an input without paging members")
	}
	if _, err := httpbind.ApplyPage(listEmailIdentities.Input, map[string]any{"~PageSize": 1}, page); err == nil {
		t.Error("ApplyPage accepted a colliding argument")
	}
	_ = restjson.Codec{}
}
