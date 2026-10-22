package twentycrm

import (
	"net/http"
	"strings"
	"testing"
)

const (
	flTabID    = "77777777-0000-4000-8000-0000000000f1"
	flWidgetA  = "88888888-0000-4000-8000-0000000000f1"
	flWidgetB  = "88888888-0000-4000-8000-0000000000f2"
	flWidgetC  = "88888888-0000-4000-8000-0000000000f3"
	flWidgetD  = "88888888-0000-4000-8000-0000000000f4"
	flFragment = "fragment-canary-4411"
)

func deepObject(levels int) string {
	return strings.Repeat(`{"a":`, levels) + `1` + strings.Repeat(`}`, levels)
}

func fullLayoutJSON(id string, extraWidget string) string {
	return `{"id":"` + id + `","name":"Board","type":"DASHBOARD","objectMetadataId":"` + metaObjID + `",` +
		`"defaultTabToFocusOnMobileAndSidePanelId":"` + flTabID + `","universalIdentifier":"` + fvUUID + `",` +
		`"applicationId":"` + fvApp + `","isSystemSideEffect":true,"isFirstTabPinned":false,` + fvStamps + `,"tabs":[` +
		`{"id":"` + flTabID + `","title":"Main","position":1,"layoutMode":"GRID","isActive":true,"isOverridden":true,` +
		`"universalIdentifier":"` + fvUUID + `","applicationId":"` + fvApp + `","isSystemSideEffect":false,` + fvStamps + `,"widgets":[` +
		`{"id":"` + flWidgetA + `","title":"Chart","type":"GRAPH","universalIdentifier":"` + fvUUID + `","applicationId":"` + fvApp + `",` +
		`"isSystemSideEffect":true,"gridPosition":{"row":1,"column":1,"rowSpan":1,"columnSpan":1},"isOverridden":true,` +
		`"configuration":{"configurationType":"BAR_CHART","aggregateFieldMetadataId":"` + metaFieldID + `","limit":12,` +
		`"note":"see https://docs.example/x?token=` + layoutTokenCn + `","link":"https://docs.example/plain","nested":[{"u":"https://u@localhost/p"}]},` +
		`"conditionalDisplay":{"and":[{"fieldName":"stage","value":true}]},` +
		`"conditionalAvailabilityExpression":"isDefined(record)",` + fvStamps + `},` +
		`{"id":"` + flWidgetB + `","title":"Embed","type":"IFRAME","position":{"layoutMode":"GRID","row":0,"column":0,"rowSpan":2,"columnSpan":2},` +
		`"configuration":{"configurationType":"IFRAME","url":"https://host.example:8443/embed","height":300}}` +
		extraWidget + `]}]}`
}

func serveFullLayout(t *testing.T, extraWidget string) {
	t.Helper()
	serve(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case pageLayoutsPath:
			return jsonResponse(http.StatusOK, `{"data":[`+fullLayoutJSON(layoutID, extraWidget)+`],"pageInfo":{"hasNextPage":false}}`), nil
		case pageLayoutsPath + "/" + layoutID:
			return jsonResponse(http.StatusOK, fullLayoutJSON(layoutID, extraWidget)), nil
		case metadataObjectsPath:
			return jsonResponse(http.StatusOK, `{"data":[`+metaObjectJSON+`],"pageInfo":{"hasNextPage":false}}`), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, cloudKey)
}

func TestPageLayoutsReturnEveryDocumentedMemberExactly(t *testing.T) {
	serveFullLayout(t, `,{"id":"`+flWidgetC+`","title":"Gone","type":"TASKS","deletedAt":"`+fvStamp1+`","configuration":{}}`)
	layoutMembers := map[string]any{"object_metadata_id": metaObjID, "default_tab_id": flTabID, "universal_identifier": fvUUID,
		"application_id": fvApp, "is_system_side_effect": true, "created_at": fvStamp1, "updated_at": fvStamp2}

	list, err := invokeLayout(t, "list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	wantMembers(t, "list", decodeMap(t, list)["layouts"].([]any)[0].(map[string]any), layoutMembers)

	out, err := invokeLayout(t, "get", `{"id":"`+layoutID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"is_overridden", "grid_position", "gridPosition", "isOverridden"} {
		if strings.Contains(out, gone) {
			t.Errorf("%s reached the output: %s", gone, out)
		}
	}
	layout := decodeMap(t, out)
	wantMembers(t, "layout", layout, layoutMembers)
	tab := layout["tabs"].([]any)[0].(map[string]any)
	wantMembers(t, "tab", tab, map[string]any{"universal_identifier": fvUUID, "application_id": fvApp,
		"is_system_side_effect": false, "created_at": fvStamp1, "updated_at": fvStamp2})
	widgets := tab["widgets"].([]any)
	if len(widgets) != 2 {
		t.Fatalf("widgets = %v", widgets)
	}
	chart := widgets[0].(map[string]any)
	wantMembers(t, "widget", chart, map[string]any{"universal_identifier": fvUUID, "application_id": fvApp,
		"is_system_side_effect": true, "created_at": fvStamp1, "updated_at": fvStamp2,
		"conditional_availability_expression": "isDefined(record)", "chart_type": "BAR_CHART"})
	configuration := chart["configuration"].(map[string]any)
	wantMembers(t, "configuration", configuration, map[string]any{"configurationType": "BAR_CHART",
		"aggregateFieldMetadataId": metaFieldID, "limit": float64(12), "link": "https://docs.example/plain"})
	if _, ok := configuration["note"]; ok {
		t.Errorf("an address with a query stayed: %v", configuration)
	}
	if nested := configuration["nested"]; nested != nil {
		t.Errorf("an address with credentials stayed: %v", nested)
	}
	if display, _ := chart["conditional_display"].(map[string]any); display["and"] == nil {
		t.Errorf("conditional_display = %v", chart["conditional_display"])
	}

	embed := widgets[1].(map[string]any)
	if embed["iframe_origin"] != "https://host.example:8443" {
		t.Errorf("iframe_origin = %v", embed["iframe_origin"])
	}
	frame := embed["configuration"].(map[string]any)
	if _, ok := frame["url"]; ok || frame["height"] != float64(300) {
		t.Errorf("iframe configuration = %v", frame)
	}
	if strings.Contains(out, "host.example/embed") || strings.Contains(out, layoutTokenCn) {
		t.Errorf("address reached the output: %s", out)
	}
}

func TestOversizedWidgetSettingsAreLeftOutAndNamed(t *testing.T) {
	extra := `,{"id":"` + flWidgetD + `","title":"Big","type":"STANDALONE_RICH_TEXT","configuration":` + deepObject(9) + `,` +
		`"conditionalDisplay":{"text":"` + strings.Repeat("x", 17<<10) + `"},"conditionalAvailabilityExpression":"` +
		strings.Repeat("y", 17<<10) + `"},` +
		`{"id":"` + flWidgetC + `","title":"Edge","type":"STANDALONE_RICH_TEXT","configuration":` + deepObject(8) + `}`
	serveFullLayout(t, extra)
	out, err := invokeLayout(t, "get", `{"id":"`+layoutID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	widgets := decodeMap(t, out)["tabs"].([]any)[0].(map[string]any)["widgets"].([]any)
	big, edge := widgets[2].(map[string]any), widgets[3].(map[string]any)
	for _, member := range []string{"configuration", "conditional_display", "conditional_availability_expression"} {
		if _, ok := big[member]; ok {
			t.Errorf("oversized %s was output", member)
		}
	}
	omitted, _ := big["omitted_fields"].([]any)
	if len(omitted) != 3 || omitted[0] != "configuration" || omitted[1] != "conditional_display" ||
		omitted[2] != "conditional_availability_expression" {
		t.Errorf("omitted_fields = %v", big["omitted_fields"])
	}
	if edge["configuration"] == nil || edge["omitted_fields"] != nil {
		t.Errorf("a configuration at the limit was left out: %v", edge)
	}
}

func TestFreeObjectLimitsAndAddressRules(t *testing.T) {
	if value, omitted := boundFreeObject([]byte(`null`)); value != nil || omitted {
		t.Errorf("null = %v, %v", value, omitted)
	}
	if value, omitted := boundFreeObject([]byte(`{}`)); value != nil || omitted {
		t.Errorf("empty object = %v, %v", value, omitted)
	}
	if value, omitted := boundFreeObject([]byte(`{"n":9007199254740993}`)); omitted || value.(map[string]any)["n"].(interface{ String() string }).String() != "9007199254740993" {
		t.Errorf("large integer = %v, %v", value, omitted)
	}
	if value, omitted := boundFreeObject([]byte(`{"url":"https://a.example/x","k":"v"}`), "url"); omitted || len(value.(map[string]any)) != 1 {
		t.Errorf("dropped key = %v, %v", value, omitted)
	}
	for _, text := range []string{"https://a.example/p?x=1", "https://a.example/p#" + flFragment, "https://u@localhost/p",
		"see [x](https://a.example/p?x=1) here", "FTP://a.example?x", "//a.example"} {
		want := !strings.HasPrefix(text, "//")
		if got := carriesAccess(text); got != want {
			t.Errorf("carriesAccess(%q) = %v, want %v", text, got, want)
		}
	}
	for _, text := range []string{"https://a.example/p", "mail me: x@y", "a?b", "https://a.example/p@q"} {
		if carriesAccess(text) {
			t.Errorf("carriesAccess(%q) dropped a harmless text", text)
		}
	}
}
