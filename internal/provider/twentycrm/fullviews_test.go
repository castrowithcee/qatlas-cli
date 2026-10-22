package twentycrm

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	fvStamp1  = "2026-02-03T04:05:06.789Z"
	fvStamp2  = "2026-02-04T05:06:07.890Z"
	fvUUID    = "99999999-0000-4000-8000-000000000001"
	fvApp     = "99999999-0000-4000-8000-000000000002"
	fvCreator = "99999999-0000-4000-8000-000000000003"
	fvGroup   = "99999999-0000-4000-8000-000000000004"
	fvUser    = "99999999-0000-4000-8000-000000000005"
)

// fvStamps is the pair of timestamps every nested DTO carries.
const fvStamps = `"createdAt":"` + fvStamp1 + `","updatedAt":"` + fvStamp2 + `"`

func fullViewJSON(id string) string {
	return `{"id":"` + id + `","universalIdentifier":"` + fvUUID + `","applicationId":"` + fvApp + `","isSystemSideEffect":true,` +
		`"name":"Board","objectMetadataId":"` + metaObjID + `","type":"KANBAN","key":"INDEX","icon":"IconKanban","position":3,` +
		`"isCompact":true,"isCustom":true,"isActive":true,"visibility":"WORKSPACE","openRecordIn":"SIDE_PANEL",` +
		`"kanbanAggregateOperation":"SUM","kanbanAggregateOperationFieldMetadataId":"` + metaFieldID + `",` +
		`"mainGroupByFieldMetadataId":"` + metaFieldID + `","shouldHideEmptyGroups":true,"kanbanColumnWidth":280,` +
		`"groupLoadLimit":9007199,"calendarFieldMetadataId":"` + metaFieldID + `","calendarEndFieldMetadataId":"` + fvUUID + `",` +
		`"calendarLayout":"MONTH","createdByUserWorkspaceId":"` + fvUser + `","workspaceId":"` + fvApp + `",` + fvStamps + `,` +
		`"viewFieldGroups":[{"id":"` + fvGroup + `","name":"Main","position":1,"isVisible":true,"isActive":true,` +
		`"isOverridden":false,"viewFields":[],` + fvStamps + `},{"id":"` + fvUUID + `","deletedAt":"` + fvStamp1 + `"}],` +
		`"viewFields":[{"id":"` + fvUUID + `","universalIdentifier":"` + fvApp + `","applicationId":"` + fvCreator + `",` +
		`"isSystemSideEffect":false,"fieldMetadataId":"` + metaFieldID + `","isVisible":true,"size":90,"position":4,` +
		`"aggregateOperation":"COUNT","viewFieldGroupId":"` + fvGroup + `","isActive":true,"isOverridden":true,` + fvStamps + `}],` +
		`"viewFilters":[{"id":"` + viewFilterID + `","fieldMetadataId":"` + metaFieldID + `","operand":"IS","value":"x",` + fvStamps + `}],` +
		`"viewFilterGroups":[{"id":"` + viewFGroupID + `","logicalOperator":"OR",` + fvStamps + `}],` +
		`"viewSorts":[{"id":"` + viewFilterID + `","fieldMetadataId":"` + metaFieldID + `","direction":"ASC",` + fvStamps + `}],` +
		`"viewGroups":[{"id":"` + viewGroupID + `","fieldValue":"OPEN","isVisible":false,"position":2,` + fvStamps + `}]}`
}

func serveFullView(t *testing.T) *[]wfCall {
	t.Helper()
	calls := &[]wfCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, wfCall{request.URL.Path, request.URL.RawQuery})
		switch request.URL.Path {
		case viewsPath:
			return jsonResponse(http.StatusOK, `{"data":[`+fullViewJSON(viewID)+`],"pageInfo":{"hasNextPage":false}}`), nil
		case viewsPath + "/" + viewID:
			return jsonResponse(http.StatusOK, fullViewJSON(viewID)), nil
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

func decodeMap(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func wantMembers(t *testing.T, what string, got map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s.%s = %v, want %v", what, key, got[key], value)
		}
	}
}

func TestViewsReturnEveryDocumentedMemberExactly(t *testing.T) {
	calls := serveFullView(t)
	summary := map[string]any{"universal_identifier": fvUUID, "application_id": fvApp, "is_system_side_effect": true,
		"kanban_aggregate_operation": "SUM", "kanban_aggregate_operation_field_id": metaFieldID, "should_hide_empty_groups": true,
		"kanban_column_width": float64(280), "group_load_limit": float64(9007199), "calendar_field_id": metaFieldID,
		"calendar_end_field_id": fvUUID, "calendar_layout": "MONTH", "created_at": fvStamp1, "updated_at": fvStamp2}

	list, err := invoke(t, "list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	row := decodeMap(t, list)["views"].([]any)[0].(map[string]any)
	wantMembers(t, "list", row, summary)

	listCalls := len(*calls)
	out, err := invoke(t, "get", `{"id":"`+viewID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(*calls) - listCalls; got != 2 {
		t.Errorf("get made %d requests, want 2", got)
	}
	view := decodeMap(t, out)
	wantMembers(t, "get", view, summary)
	for _, gone := range []string{"created_by_user_workspace_id", "createdByUserWorkspaceId", "open_record_in", "openRecordIn",
		"is_overridden", "workspace_id", fvUser} {
		if strings.Contains(out, gone) {
			t.Errorf("%s reached the output: %s", gone, out)
		}
	}
	groups := view["field_groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("field_groups = %v", groups)
	}
	wantMembers(t, "field group", groups[0].(map[string]any), map[string]any{"id": fvGroup, "name": "Main", "position": float64(1),
		"is_visible": true, "is_active": true, "created_at": fvStamp1, "updated_at": fvStamp2})
	wantMembers(t, "get", view, map[string]any{"kanban_aggregate_operation_field": "stage", "calendar_field": "stage"})
	wantMembers(t, "field", view["fields"].([]any)[0].(map[string]any), map[string]any{"field_group_id": fvGroup,
		"universal_identifier": fvApp, "application_id": fvCreator, "is_system_side_effect": false, "is_active": true,
		"aggregate_operation": "COUNT", "created_at": fvStamp1, "updated_at": fvStamp2})
	for key, name := range map[string]string{"filters": "filter", "filter_groups": "filter group", "sorts": "sort", "groups": "group"} {
		wantMembers(t, name, view[key].([]any)[0].(map[string]any), map[string]any{"created_at": fvStamp1, "updated_at": fvStamp2})
	}
}

func TestViewsWithoutNewMembersKeepTheirOutput(t *testing.T) {
	serveViews(t, standardViewList())
	out, err := invoke(t, "get", `{"id":"`+viewID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{"universal_identifier", "created_at", "updated_at", "field_groups", "calendar", "kanban",
		"group_load_limit", "should_hide_empty_groups", "is_system_side_effect"} {
		if strings.Contains(out, member) {
			t.Errorf("%s appeared without a source value: %s", member, out)
		}
	}
}

func TestViewIntegersAndStampsAcceptOnlyWellFormedValues(t *testing.T) {
	for raw, want := range map[string]json.Number{`280`: "280", `-1`: "-1", `0`: "0", `1.5`: "", `"7"`: "", `null`: "", ``: "",
		`1e3`: "", `12345678901`: "", `true`: ""} {
		if got := integerNumber(json.RawMessage(raw)); got != want {
			t.Errorf("integerNumber(%q) = %q, want %q", raw, got, want)
		}
	}
	record := viewRecord{ID: viewID, ObjectMetadataID: metaObjID, Type: "TABLE", CreatedAt: ptr("not a time"),
		UniversalIdentifier: ptr("nope"), CalendarLayout: ptr("month; drop")}
	summary, err := record.summary("op", nil)
	if err != nil || summary.CreatedAt != "" || summary.UniversalIdentifier != "" || summary.CalendarLayout != "" {
		t.Errorf("malformed values passed: %+v, %v", summary, err)
	}
}
