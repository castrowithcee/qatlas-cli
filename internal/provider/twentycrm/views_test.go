package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	viewID       = "11111111-0000-4000-8000-0000000000a1"
	viewUnlisted = "11111111-0000-4000-8000-0000000000a2"
	viewMissing  = "11111111-0000-4000-8000-0000000000a3"
	viewWidget   = "11111111-0000-4000-8000-0000000000a4"
	viewFilterID = "22222222-0000-4000-8000-0000000000b1"
	viewGroupID  = "33333333-0000-4000-8000-0000000000c1"
	viewFGroupID = "44444444-0000-4000-8000-0000000000d1"
	viewUnknown  = "55555555-0000-4000-8000-0000000000e1"
	creatorCn    = "canary-creator-9911"
	appCn        = "canary-application-9922"
)

func viewJSON(id, visibility, kind string) string {
	return `{"id":"` + id + `","name":"All deals","objectMetadataId":"` + metaObjID + `","type":"` + kind + `","key":"INDEX",` +
		`"icon":"IconList","position":2,"isCompact":false,"isCustom":true,"isActive":true,"visibility":"` + visibility + `",` +
		`"createdByUserWorkspaceId":"` + creatorCn + `","applicationId":"` + appCn + `","anyFieldFilterValue":"acme",` +
		`"mainGroupByFieldMetadataId":"` + metaFieldID + `",` +
		`"viewFields":[{"fieldMetadataId":"` + metaFieldID + `","isVisible":true,"size":120,"position":0,"applicationId":"` + appCn + `"},` +
		`{"fieldMetadataId":"` + viewUnknown + `","isVisible":false,"size":80,"position":1},` +
		`{"fieldMetadataId":"` + metaOtherID + `","deletedAt":"2026-01-01T00:00:00Z"}],` +
		`"viewFilters":[{"id":"` + viewFilterID + `","fieldMetadataId":"` + metaFieldID + `","operand":"CONTAINS",` +
		`"value":"Ada\u0007 Lovelace","viewFilterGroupId":"` + viewFGroupID + `","positionInViewFilterGroup":0},` +
		`{"id":"` + viewFilterID + `","fieldMetadataId":"` + metaFieldID + `","operand":"EVIL","value":["a","b"]}],` +
		`"viewFilterGroups":[{"id":"` + viewFGroupID + `","logicalOperator":"AND","positionInViewFilterGroup":0}],` +
		`"viewSorts":[{"id":"` + viewFilterID + `","fieldMetadataId":"` + metaFieldID + `","direction":"DESC"}],` +
		`"viewGroups":[{"id":"` + viewGroupID + `","fieldValue":"OPEN","isVisible":true,"position":0}]}`
}

const viewsPageInfo = `"pageInfo":{"hasNextPage":true,"endCursor":"` + metaOtherID + `"},"totalCount":4`

func serveViews(t *testing.T, list string) *[]wfCall {
	t.Helper()
	calls := &[]wfCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, wfCall{request.URL.Path, request.URL.RawQuery})
		switch request.URL.Path {
		case viewsPath:
			return jsonResponse(http.StatusOK, list), nil
		case viewsPath + "/" + viewID:
			return jsonResponse(http.StatusOK, viewJSON(viewID, "WORKSPACE", "TABLE")), nil
		case viewsPath + "/" + viewUnlisted:
			return jsonResponse(http.StatusOK, viewJSON(viewUnlisted, "UNLISTED", "TABLE")), nil
		case viewsPath + "/" + viewWidget:
			return jsonResponse(http.StatusOK, viewJSON(viewWidget, "WORKSPACE", "TABLE_WIDGET")), nil
		case viewsPath + "/" + viewMissing:
			return jsonResponse(http.StatusNotFound, `{"message":"provider-text-canary"}`), nil
		case metadataObjectsPath:
			return jsonResponse(http.StatusOK, `{"data":[`+metaObjectJSON+`],"pageInfo":{"hasNextPage":false}}`), nil
		case metadataObjectsPath + "/" + metaObjID:
			return jsonResponse(http.StatusOK, metaObjectJSON), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func standardViewList() string {
	return `{"data":[` + viewJSON(viewID, "WORKSPACE", "TABLE") + `,` + viewJSON(viewUnlisted, "UNLISTED", "TABLE") + `,` +
		viewJSON(viewWidget, "WORKSPACE", "KANBAN_WIDGET") + `],` + viewsPageInfo + `}`
}

func invoke(t *testing.T, tool string, args string, targets ...string) (string, error) {
	t.Helper()
	red := &redact.Redactor{}
	var result any
	var err error
	conn := targetConnection(targets...)
	if tool == "list" {
		result, err = invokeViewsList(context.Background(), conn, resolver(red), red, json.RawMessage(args))
	} else {
		result, err = invokeViewsGet(context.Background(), conn, resolver(red), red, json.RawMessage(args))
	}
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(result)
	for _, canary := range []string{creatorCn, appCn, "createdBy", "applicationId"} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	return string(out), nil
}

func isInvalidRequest(err error) bool {
	var invalid *provider.InvalidRequestError
	return errors.As(err, &invalid)
}

func TestViewsListShowsOnlyWorkspaceViewsWithObjectNames(t *testing.T) {
	calls := serveViews(t, standardViewList())
	out, err := invoke(t, "list", `{"limit":5}`)
	if err != nil {
		t.Fatal(err)
	}
	var page ViewList
	if json.Unmarshal([]byte(out), &page) != nil || len(page.Views) != 1 || page.Views[0].ID != viewID ||
		page.Views[0].Object != "deal" || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page = %s", out)
	}
	if strings.Contains(out, viewUnlisted) || strings.Contains(out, viewWidget) || strings.Contains(out, metaOtherID) {
		t.Errorf("hidden view or raw cursor in %s", out)
	}
	var viewCall wfCall
	for _, call := range *calls {
		if call.Path == viewsPath {
			viewCall = call
		}
	}
	if viewCall.Query != "limit=5" {
		t.Errorf("view request = %+v", viewCall)
	}

	// The next page continues after the Twenty cursor and keeps the object filter out when none was given.
	if _, err := invoke(t, "list", `{"limit":5,"cursor":"`+page.NextCursor+`"}`); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	query, _ := url.ParseQuery(last.Query)
	if last.Path != viewsPath || query.Get("starting_after") != metaOtherID || query.Get("limit") != "5" || len(query) != 2 {
		t.Errorf("second request = %+v", last)
	}
	// A cursor does not carry over to another object filter.
	if _, err := invoke(t, "list", `{"object":"deal","cursor":"`+page.NextCursor+`"}`); classOf(err) != "" || err == nil {
		t.Errorf("foreign cursor err = %v", err)
	}
}

func TestViewsListFiltersByObjectNameOrIDOnly(t *testing.T) {
	calls := serveViews(t, `{"data":[],"pageInfo":{"hasNextPage":false}}`)
	for _, args := range []string{`{"object":"deal"}`, `{"object_id":"` + metaObjID + `"}`} {
		if _, err := invoke(t, "list", args); err != nil {
			t.Fatal(err)
		}
		last := (*calls)[len(*calls)-1]
		query, _ := url.ParseQuery(last.Query)
		if last.Path != viewsPath || query.Get("objectMetadataId") != metaObjID {
			t.Errorf("%s -> %+v", args, last)
		}
	}
	for _, args := range []string{`{"object":"nothing"}`, `{"object":"deal","object_id":"` + metaObjID + `"}`,
		`{"object":"deal\"x"}`, `{"object_id":"nope"}`} {
		if _, err := invoke(t, "list", args); err == nil {
			t.Errorf("%s accepted", args)
		} else if !isInvalidRequest(err) {
			t.Errorf("%s err = %v", args, err)
		}
	}
}

func TestViewsGetResolvesNamesAndKeepsUnknownIdentifiers(t *testing.T) {
	calls := serveViews(t, "")
	out, err := invoke(t, "get", `{"id":"`+viewID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var view View
	if json.Unmarshal([]byte(out), &view) != nil {
		t.Fatal(out)
	}
	if view.Object != "deal" || view.MainGroupByField != "stage" || view.AnyFieldFilter != "acme" ||
		len(view.Fields) != 2 || view.Fields[0].Field != "stage" || view.Fields[0].FieldID != metaFieldID ||
		view.Fields[1].Field != "" || view.Fields[1].FieldID != viewUnknown {
		t.Errorf("view = %s", out)
	}
	if len(view.Filters) != 2 || view.Filters[0].Field != "stage" || view.Filters[0].Value != "Ada Lovelace" ||
		view.Filters[0].Operand != "CONTAINS" || view.Filters[1].Operand != "unknown" || view.Filters[1].Value != `["a","b"]` ||
		len(view.FilterGroups) != 1 || view.FilterGroups[0].LogicalOperator != "AND" ||
		len(view.Sorts) != 1 || view.Sorts[0].Direction != "DESC" || view.Sorts[0].Field != "stage" ||
		len(view.Groups) != 1 || view.Groups[0].FieldValue != "OPEN" {
		t.Errorf("layout = %s", out)
	}
	if (*calls)[0].Path != viewsPath+"/"+viewID || (*calls)[0].Query != "" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestViewsGetAnswersUnlistedAndWidgetLikeAnUnknownView(t *testing.T) {
	serveViews(t, "")
	_, unknown := invoke(t, "get", `{"id":"`+viewMissing+`"}`)
	for _, id := range []string{viewUnlisted, viewWidget} {
		_, err := invoke(t, "get", `{"id":"`+id+`"}`)
		if err == nil || unknown == nil || classOf(err) != provider.ClassNotFound || err.Error() != unknown.Error() {
			t.Errorf("%s err = %v, unknown = %v", id, err, unknown)
		}
		if strings.Contains(err.Error(), "UNLISTED") || strings.Contains(err.Error(), "provider-text-canary") {
			t.Errorf("err leaks: %v", err)
		}
	}
}

func TestViewsGetRejectsAForeignViewAndBadLayouts(t *testing.T) {
	for name, body := range map[string]string{
		"foreign id":  viewJSON(viewUnlisted, "WORKSPACE", "TABLE"),
		"bad filter":  strings.Replace(viewJSON(viewID, "WORKSPACE", "TABLE"), `"id":"`+viewFilterID+`","fieldMetadataId":"`+metaFieldID+`","operand":"CONTAINS"`, `"id":"x","fieldMetadataId":"`+metaFieldID+`","operand":"CONTAINS"`, 1),
		"bad columns": strings.Replace(viewJSON(viewID, "WORKSPACE", "TABLE"), `"viewFields":[`, `"viewFields":{"a":[`, 1),
	} {
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == metadataObjectsPath+"/"+metaObjID {
				return jsonResponse(http.StatusOK, metaObjectJSON), nil
			}
			return jsonResponse(http.StatusOK, body), nil
		})
		stubLimiter(t, cloudKey)
		if _, err := invoke(t, "get", `{"id":"`+viewID+`"}`); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s err = %v", name, err)
		}
	}
}

func TestViewsAreBoundedAndOversizedAnswersAreInvalid(t *testing.T) {
	var many []string
	for i := 0; i < viewChildrenMax+1; i++ {
		many = append(many, `{"fieldMetadataId":"`+metaFieldID+`"}`)
	}
	body := strings.Replace(viewJSON(viewID, "WORKSPACE", "TABLE"), `"viewFields":[`, `"viewFields":[`+strings.Join(many, ",")+`,`, 1)
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	stubLimiter(t, cloudKey)
	if _, err := invoke(t, "get", `{"id":"`+viewID+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("too many columns err = %v", err)
	}

	huge := `{"id":"` + viewID + `","name":"` + strings.Repeat("x", viewResponseBytes) + `"}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, huge), nil })
	stubLimiter(t, cloudKey)
	if _, err := invoke(t, "get", `{"id":"`+viewID+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("oversized err = %v", err)
	}
	if _, err := invoke(t, "list", `{}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("oversized list err = %v", err)
	}

	long := strings.Repeat("v", 3000)
	body = strings.Replace(viewJSON(viewID, "WORKSPACE", "TABLE"), "Ada\\u0007 Lovelace", long, 1)
	serve(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasPrefix(request.URL.Path, metadataObjectsPath) {
			return jsonResponse(http.StatusOK, metaObjectJSON), nil
		}
		return jsonResponse(http.StatusOK, body), nil
	})
	stubLimiter(t, cloudKey)
	out, err := invoke(t, "get", `{"id":"`+viewID+`"}`)
	var view View
	if json.Unmarshal([]byte(out), &view); err != nil || len(view.Filters[0].Value) != viewValueMax || !view.Filters[0].ValueTruncated {
		t.Errorf("value = %d, %v", len(view.Filters[0].Value), err)
	}
}

func TestViewsListRejectsMoreViewsThanRequested(t *testing.T) {
	serveViews(t, `{"data":[`+viewJSON(viewID, "WORKSPACE", "TABLE")+`,`+viewJSON(viewID, "WORKSPACE", "TABLE")+`],"pageInfo":{"hasNextPage":false}}`)
	if _, err := invoke(t, "list", `{"limit":1}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestViewsWorkspaceGateComesBeforeSecretAndNetwork(t *testing.T) {
	refuse(t)
	for _, tool := range []string{"list", "get"} {
		args := `{}`
		if tool == "get" {
			args = `{"id":"` + viewID + `"}`
		}
		_, err := invokeNoSecret(t, tool, args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s err = %v", tool, err)
		}
	}
}

func invokeNoSecret(t *testing.T, tool, args string) (any, error) {
	t.Helper()
	red := &redact.Redactor{}
	conn := targetConnection("object/person")
	unavailable := countingResolver(t)
	if tool == "list" {
		return invokeViewsList(context.Background(), conn, unavailable, red, json.RawMessage(args))
	}
	return invokeViewsGet(context.Background(), conn, unavailable, red, json.RawMessage(args))
}

func TestViewsForbiddenNamesNoProviderText(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{"message":"provider-text-canary"}`), nil
	})
	stubLimiter(t, cloudKey)
	_, err := invoke(t, "get", `{"id":"`+viewID+`"}`)
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "provider-text-canary") {
		t.Errorf("err = %v", err)
	}
}
