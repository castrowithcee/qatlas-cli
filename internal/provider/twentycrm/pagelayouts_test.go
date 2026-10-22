package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	layoutID       = "66666666-0000-4000-8000-0000000000a1"
	layoutOther    = "66666666-0000-4000-8000-0000000000a2"
	layoutGone     = "66666666-0000-4000-8000-0000000000a3"
	layoutMissing  = "66666666-0000-4000-8000-0000000000a4"
	layoutTabID    = "77777777-0000-4000-8000-0000000000b1"
	layoutTabGone  = "77777777-0000-4000-8000-0000000000b2"
	layoutWidgetA  = "88888888-0000-4000-8000-0000000000c1"
	layoutWidgetB  = "88888888-0000-4000-8000-0000000000c2"
	layoutWidgetC  = "88888888-0000-4000-8000-0000000000c3"
	layoutWidgetD  = "88888888-0000-4000-8000-0000000000c4"
	layoutWidgetE  = "88888888-0000-4000-8000-0000000000c5"
	layoutWidgetF  = "88888888-0000-4000-8000-0000000000c6"
	layoutBodyText = "rich-text-body-canary-5521"
	layoutTokenCn  = "secret-token-canary-7731"
)

func layoutJSON(id, kind, deletedAt string) string {
	deleted := "null"
	if deletedAt != "" {
		deleted = `"` + deletedAt + `"`
	}
	return `{"id":"` + id + `","name":"Sales\u0007 board","type":"` + kind + `","objectMetadataId":null,"isFirstTabPinned":true,` +
		`"universalIdentifier":"` + creatorCn + `","applicationId":"` + appCn + `","createdByUserWorkspaceId":"` + creatorCn + `",` +
		`"deletedAt":` + deleted + `,"tabs":[` +
		`{"id":"` + layoutTabID + `","title":"Overview","position":0,"icon":"IconHome","layoutMode":"GRID","isActive":true,` +
		`"applicationId":"` + appCn + `","widgets":[` +
		`{"id":"` + layoutWidgetA + `","title":"Pipeline","type":"GRAPH","objectMetadataId":"` + metaObjID + `","isActive":true,` +
		`"applicationId":"` + appCn + `","position":{"layoutMode":"GRID","row":0,"column":1,"rowSpan":4,"columnSpan":6},` +
		`"configuration":{"configurationType":"BAR_CHART","aggregateFieldMetadataId":"` + metaFieldID + `","filter":{"x":"` + layoutBodyText + `"}}},` +
		`{"id":"` + layoutWidgetB + `","title":"Embed","type":"IFRAME","position":{"layoutMode":"VERTICAL_LIST","index":2},` +
		`"configuration":{"configurationType":"IFRAME","url":"https://user:pw@203.0.113.7:8443/report/x?token=` + layoutTokenCn + `#frag"}},` +
		`{"id":"` + layoutWidgetC + `","title":"Mystery","type":"HOLOGRAM","position":null,"configuration":{"configurationType":"HOLOGRAM"}},` +
		`{"id":"` + layoutWidgetD + `","title":"Notes","type":"STANDALONE_RICH_TEXT","position":{"layoutMode":"EVIL"},` +
		`"configuration":{"configurationType":"STANDALONE_RICH_TEXT","body":{"markdown":"` + layoutBodyText + `"}}},` +
		`{"id":"` + layoutWidgetE + `","title":"Bad","type":"IFRAME","configuration":{"configurationType":"IFRAME","url":"ht tp://%zz"}},` +
		`{"id":"` + layoutWidgetF + `","title":"Component","type":"FRONT_COMPONENT","configuration":{"configurationType":"FRONT_COMPONENT","frontComponentId":"` + creatorCn + `"}},` +
		`{"id":"` + layoutWidgetA + `","title":"Removed","type":"TASKS","deletedAt":"2026-01-01T00:00:00Z","configuration":{}}]},` +
		`{"id":"` + layoutTabGone + `","title":"Removed","deletedAt":"2026-01-01T00:00:00Z","widgets":[]}]}`
}

func serveLayouts(t *testing.T) *[]wfCall {
	t.Helper()
	calls := &[]wfCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, wfCall{request.URL.Path, request.URL.RawQuery})
		switch request.URL.Path {
		case pageLayoutsPath:
			return jsonResponse(http.StatusOK, `{"data":[`+layoutJSON(layoutID, "DASHBOARD", "")+`,`+
				layoutJSON(layoutOther, "RECORD_PAGE", "")+`,`+layoutJSON(layoutGone, "DASHBOARD", "2026-01-01T00:00:00Z")+
				`],"pageInfo":{"hasNextPage":true,"endCursor":"`+metaOtherID+`"},"totalCount":4}`), nil
		case pageLayoutsPath + "/" + layoutID:
			return jsonResponse(http.StatusOK, layoutJSON(layoutID, "DASHBOARD", "")), nil
		case pageLayoutsPath + "/" + layoutOther:
			return jsonResponse(http.StatusOK, layoutJSON(layoutOther, "RECORD_PAGE", "")), nil
		case pageLayoutsPath + "/" + layoutGone:
			return jsonResponse(http.StatusOK, layoutJSON(layoutGone, "DASHBOARD", "2026-01-01T00:00:00Z")), nil
		case pageLayoutsPath + "/" + layoutMissing:
			return jsonResponse(http.StatusNotFound, `{"message":"provider-text-canary"}`), nil
		case metadataObjectsPath:
			return jsonResponse(http.StatusOK, `{"data":[`+metaObjectJSON+`],"pageInfo":{"hasNextPage":false}}`), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func invokeLayout(t *testing.T, tool, args string, targets ...string) (string, error) {
	t.Helper()
	red := &redact.Redactor{}
	conn := targetConnection(targets...)
	var result any
	var err error
	if tool == "list" {
		result, err = invokePageLayoutsList(context.Background(), conn, resolver(red), red, json.RawMessage(args))
	} else {
		result, err = invokePageLayoutsGet(context.Background(), conn, resolver(red), red, json.RawMessage(args))
	}
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(result)
	for _, canary := range []string{"createdBy", layoutTokenCn, "user:pw", "/report", "EVIL"} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	return string(out), nil
}

func TestPageLayoutsListAsksForDashboardsOnlyAndShowsLiveOnes(t *testing.T) {
	calls := serveLayouts(t)
	out, err := invokeLayout(t, "list", `{"limit":5}`)
	if err != nil {
		t.Fatal(err)
	}
	var page PageLayoutList
	if json.Unmarshal([]byte(out), &page) != nil || len(page.Layouts) != 1 || page.Layouts[0].ID != layoutID ||
		page.Layouts[0].TabCount != 1 || page.Layouts[0].Name != "Sales board" || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page = %s", out)
	}
	if strings.Contains(out, layoutOther) || strings.Contains(out, layoutGone) || strings.Contains(out, metaOtherID) {
		t.Errorf("hidden layout or raw cursor in %s", out)
	}
	if len(*calls) != 1 || (*calls)[0].Path != pageLayoutsPath || !strings.Contains((*calls)[0].Query, "pageLayoutType=DASHBOARD") ||
		!strings.Contains((*calls)[0].Query, "limit=5") {
		t.Errorf("calls = %+v", *calls)
	}
	next, err := invokeLayout(t, "list", `{"cursor":"`+page.NextCursor+`"}`)
	if err != nil || next == "" || !strings.Contains((*calls)[1].Query, "starting_after="+metaOtherID) {
		t.Errorf("next = %s, %v, calls = %+v", next, err, *calls)
	}
	if _, err := invokeLayout(t, "list", `{"cursor":"forged"}`); !isInvalidRequest(err) {
		t.Errorf("forged cursor err = %v", err)
	}
}

func TestPageLayoutsGetReducesTabsAndWidgets(t *testing.T) {
	calls := serveLayouts(t)
	out, err := invokeLayout(t, "get", `{"id":"`+layoutID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var layout PageLayout
	if err := json.Unmarshal([]byte(out), &layout); err != nil || layout.Type != "DASHBOARD" || layout.Name != "Sales board" ||
		len(layout.Tabs) != 1 || layout.Tabs[0].Title != "Overview" || layout.Tabs[0].LayoutMode != "GRID" {
		t.Fatalf("layout = %s, %v", out, err)
	}
	widgets := layout.Tabs[0].Widgets
	if len(widgets) != 6 {
		t.Fatalf("widgets = %s", out)
	}
	chart := widgets[0]
	if chart.Type != "GRAPH" || chart.ChartType != "BAR_CHART" || chart.Object != "deal" || chart.ObjectMetadataID != metaObjID ||
		chart.Position == nil || *chart.Position.ColumnSpan != 6 || chart.Position.LayoutMode != "GRID" {
		t.Errorf("chart = %+v", chart)
	}
	if widgets[1].Type != "IFRAME" || widgets[1].IframeOrigin != "https://203.0.113.7:8443" ||
		widgets[1].Position == nil || *widgets[1].Position.Index != 2 {
		t.Errorf("iframe = %+v", widgets[1])
	}
	if widgets[2].Type != "unknown" || widgets[2].Position != nil || widgets[2].ChartType != "" {
		t.Errorf("unknown widget = %+v", widgets[2])
	}
	if widgets[3].Type != "STANDALONE_RICH_TEXT" || widgets[3].Position.LayoutMode != "unknown" {
		t.Errorf("rich text = %+v", widgets[3])
	}
	if widgets[4].IframeOrigin != "" || widgets[5].Type != "FRONT_COMPONENT" || widgets[5].IframeOrigin != "" {
		t.Errorf("bad iframe or component = %+v %+v", widgets[4], widgets[5])
	}
	if len(*calls) != 2 || (*calls)[0].Path != pageLayoutsPath+"/"+layoutID || (*calls)[0].Query != "" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestIframeOriginKeepsOnlySchemeHostAndPort(t *testing.T) {
	for raw, want := range map[string]string{
		"https://a.example:8443/p?q=1#f":      "https://a.example:8443",
		"HTTP://u:p@198.51.100.2/x":           "http://198.51.100.2",
		"https://[::1]:9000/x":                "https://[::1]:9000",
		"javascript:alert(1)":                 "",
		"ftp://c.example/x":                   "",
		"//c.example/x":                       "",
		"https:///path":                       "",
		"https://bad host/x":                  "",
		"%zz":                                 "",
		"":                                    "",
		"https://" + strings.Repeat("h", 400): "",
	} {
		if got := iframeOrigin(raw); got != want {
			t.Errorf("iframeOrigin(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPageLayoutsGetRefusesOtherTypesAndDeletedLikeUnknown(t *testing.T) {
	serveLayouts(t)
	for _, id := range []string{layoutOther, layoutGone, layoutMissing} {
		_, err := invokeLayout(t, "get", `{"id":"`+id+`"}`)
		if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), "provider-text-canary") ||
			strings.Contains(err.Error(), "RECORD_PAGE") {
			t.Errorf("%s err = %v", id, err)
		}
	}
	if _, err := invokeLayout(t, "get", `{"id":"not-a-uuid"}`); !isInvalidRequest(err) {
		t.Errorf("invalid id err = %v", err)
	}
}

func TestPageLayoutsWorkspaceGateComesBeforeSecretAndNetwork(t *testing.T) {
	refuse(t)
	for _, tool := range []string{"list", "get"} {
		red := &redact.Redactor{}
		conn := targetConnection("object/person")
		unavailable := countingResolver(t)
		var err error
		if tool == "list" {
			_, err = invokePageLayoutsList(context.Background(), conn, unavailable, red, json.RawMessage(`{}`))
		} else {
			_, err = invokePageLayoutsGet(context.Background(), conn, unavailable, red, json.RawMessage(`{"id":"`+layoutID+`"}`))
		}
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s err = %v", tool, err)
		}
	}
}

func TestPageLayoutsAreBoundedAndOversizedAnswersAreInvalid(t *testing.T) {
	widget := func(i int) string {
		return `{"id":"` + layoutWidgetA + `","title":"w","type":"TASKS"}`
	}
	var many []string
	for i := 0; i < dashboardWidgetsMax+1; i++ {
		many = append(many, widget(i))
	}
	body := `{"id":"` + layoutID + `","name":"n","type":"DASHBOARD","tabs":[{"id":"` + layoutTabID + `","title":"t","widgets":[` +
		strings.Join(many, ",") + `]}]}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil })
	stubLimiter(t, cloudKey)
	if _, err := invokeLayout(t, "get", `{"id":"`+layoutID+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("too many widgets err = %v", err)
	}

	var tabs []string
	for i := 0; i < dashboardTabsMax+1; i++ {
		tabs = append(tabs, `{"id":"`+layoutTabID+`","title":"t","widgets":[]}`)
	}
	body = `{"id":"` + layoutID + `","name":"n","type":"DASHBOARD","tabs":[` + strings.Join(tabs, ",") + `]}`
	if _, err := invokeLayout(t, "get", `{"id":"`+layoutID+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("too many tabs err = %v", err)
	}

	huge := `{"id":"` + layoutID + `","name":"` + strings.Repeat("x", pageLayoutResponseBytes) + `"}`
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, huge), nil })
	stubLimiter(t, cloudKey)
	if _, err := invokeLayout(t, "get", `{"id":"`+layoutID+`"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("oversized err = %v", err)
	}
	if _, err := invokeLayout(t, "list", `{}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("oversized list err = %v", err)
	}

	long := strings.Repeat("v", 3000)
	body = `{"id":"` + layoutID + `","name":"` + long + `","type":"DASHBOARD","tabs":[{"id":"` + layoutTabID + `","title":"` + long +
		`","widgets":[{"id":"` + layoutWidgetA + `","title":"` + long + `","type":"TASKS"}]}]}`
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == metadataObjectsPath {
			return jsonResponse(http.StatusOK, `{"data":[],"pageInfo":{"hasNextPage":false}}`), nil
		}
		return jsonResponse(http.StatusOK, body), nil
	})
	stubLimiter(t, cloudKey)
	out, err := invokeLayout(t, "get", `{"id":"`+layoutID+`"}`)
	var layout PageLayout
	if err != nil || json.Unmarshal([]byte(out), &layout) != nil || len(layout.Name) != metaNameMax ||
		len(layout.Tabs[0].Title) != metaNameMax || len(layout.Tabs[0].Widgets[0].Title) != metaNameMax {
		t.Errorf("titles not capped: %v, %d", err, len(out))
	}
}

func TestPageLayoutsListRejectsMoreLayoutsThanRequested(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":[`+layoutJSON(layoutID, "DASHBOARD", "")+`,`+layoutJSON(layoutID, "DASHBOARD", "")+
			`],"pageInfo":{"hasNextPage":false}}`), nil
	})
	stubLimiter(t, cloudKey)
	if _, err := invokeLayout(t, "list", `{"limit":1}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestPageLayoutsForbiddenNamesNoProviderText(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{"message":"provider-text-canary"}`), nil
	})
	stubLimiter(t, cloudKey)
	_, err := invokeLayout(t, "get", `{"id":"`+layoutID+`"}`)
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "provider-text-canary") {
		t.Errorf("err = %v", err)
	}
}
