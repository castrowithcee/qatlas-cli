package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	metaObjID   = "aaaaaaaa-0000-4000-8000-000000000001"
	metaFieldID = "bbbbbbbb-0000-4000-8000-000000000002"
	metaOptID   = "cccccccc-0000-4000-8000-000000000003"
	metaOtherID = "dddddddd-0000-4000-8000-000000000004"
	metaDefCn   = "canary-default-value-4411"
	metaSetCn   = "canary-settings-6622"
	metaRelCn   = "canary-relation-7733"
	metaTypeCn  = "CANARY_TYPE_8844"
)

const metaFieldJSON = `{"id":"` + metaFieldID + `","type":"SELECT","name":"stage","label":"Sta\u0007ge","description":"d",` +
	`"isCustom":true,"isSystem":false,"isActive":true,"isNullable":false,"isUnique":false,` +
	`"defaultValue":"'` + metaDefCn + `'","settings":{"x":"` + metaSetCn + `"},` +
	`"relation":{"targetObjectMetadata":{"id":"` + metaRelCn + `"}},"objectMetadataId":"` + metaObjID + `",` +
	`"options":[{"id":"` + metaOptID + `","label":"Open","value":"OPEN","color":"red","position":0}]}`

const metaFieldOddJSON = `{"id":"` + metaOtherID + `","type":"` + metaTypeCn + `","name":"n","label":"l"}`

const metaObjectJSON = `{"id":"` + metaObjID + `","nameSingular":"deal","namePlural":"deals","labelSingular":"Deal",` +
	`"labelPlural":"Deals","description":"Sales","isCustom":true,"isActive":true,"isSystem":false,"isRemote":false,` +
	`"labelIdentifierFieldMetadataId":"` + metaFieldID + `","fields":[` + metaFieldJSON + `,` + metaFieldOddJSON + `]}`

const metaPageInfo = `"pageInfo":{"hasNextPage":true,"hasPreviousPage":false,"startCursor":"` + metaObjID +
	`","endCursor":"` + metaOtherID + `"},"totalCount":1`

type metaFormat struct {
	name                    string
	list, objectGet, fldGet string
}

var metaFormats = []metaFormat{
	{"new", `{"data":[` + metaObjectJSON + `],` + metaPageInfo + `}`, metaObjectJSON, metaFieldJSON},
	{"legacy", `{"data":{"objects":[` + metaObjectJSON + `]},` + metaPageInfo + `}`,
		`{"data":{"object":` + metaObjectJSON + `}}`, `{"data":{"field":` + metaFieldJSON + `}}`},
}

func serveMeta(t *testing.T, format metaFormat) *[]wfCall {
	t.Helper()
	calls := &[]wfCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		*calls = append(*calls, wfCall{request.URL.Path, request.URL.RawQuery})
		switch request.URL.Path {
		case metadataObjectsPath:
			return jsonResponse(http.StatusOK, format.list), nil
		case metadataObjectsPath + "/" + metaObjID:
			return jsonResponse(http.StatusOK, format.objectGet), nil
		case metadataFieldsPath + "/" + metaFieldID:
			return jsonResponse(http.StatusOK, format.fldGet), nil
		}
		t.Errorf("unexpected request %s", request.URL.Path)
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func runMeta(t *testing.T, handler capability.Handler, args string) (string, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(result)
	for _, canary := range []string{metaDefCn, metaSetCn, metaRelCn, metaTypeCn, "\\u0007"} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	return string(out), nil
}

func TestMetadataBothFormatsNormalizeIdentically(t *testing.T) {
	var lists, objects, fields []string
	for _, format := range metaFormats {
		serveMeta(t, format)
		list, err := runMeta(t, invokeMetaObjectsList, `{}`)
		if err != nil {
			t.Fatalf("%s list: %v", format.name, err)
		}
		object, err := runMeta(t, invokeMetaObjectsGet, `{"id":"`+metaObjID+`"}`)
		if err != nil {
			t.Fatalf("%s object: %v", format.name, err)
		}
		field, err := runMeta(t, invokeMetaFieldsGet, `{"id":"`+metaFieldID+`"}`)
		if err != nil {
			t.Fatalf("%s field: %v", format.name, err)
		}
		lists, objects, fields = append(lists, list), append(objects, object), append(fields, field)
	}
	for _, pair := range [][]string{lists, objects, fields} {
		if pair[0] != pair[1] {
			t.Errorf("formats differ:\n%s\n%s", pair[0], pair[1])
		}
	}
	for _, want := range []string{`"name_singular":"deal"`, `"label_plural":"Deals"`, `"is_custom":true`,
		`"is_system":false`, `"is_active":true`, `"field_count":2`, `"has_more":true`, `"next_cursor"`} {
		if !strings.Contains(lists[0], want) {
			t.Errorf("list lacks %s: %s", want, lists[0])
		}
	}
	if strings.Contains(lists[0], `"fields"`) {
		t.Errorf("list carries fields: %s", lists[0])
	}
	for _, want := range []string{`"label":"Stage"`, `"type":"SELECT"`, `"type":"unknown"`, `"is_nullable":false`,
		`"options":[{"id":"` + metaOptID + `","value":"OPEN","label":"Open","position":0}]`} {
		if !strings.Contains(objects[0], want) {
			t.Errorf("object lacks %s: %s", want, objects[0])
		}
	}
	if strings.Contains(objects[0], "object_metadata_id") || !strings.Contains(fields[0], `"object_metadata_id":"`+metaObjID+`"`) {
		t.Errorf("object_metadata_id: %s / %s", objects[0], fields[0])
	}
	for _, leak := range []string{"color", "defaultValue", "default_value", "settings", "relation"} {
		for _, out := range []string{lists[0], objects[0], fields[0]} {
			if strings.Contains(out, leak) {
				t.Errorf("%s reached an output: %s", leak, out)
			}
		}
	}
}

func TestMetadataThirdFormsAreInvalidResponses(t *testing.T) {
	for name, format := range map[string]metaFormat{
		"list data is a string": {list: `{"data":"x","pageInfo":{"hasNextPage":false}}`},
		"list data object without objects": {
			list: `{"data":{"rows":[]},"pageInfo":{"hasNextPage":false}}`},
		"list without data":     {list: `{"pageInfo":{"hasNextPage":false}}`},
		"list item without id":  {list: `{"data":[{"nameSingular":"x"}],"pageInfo":{"hasNextPage":false}}`},
		"list fields not array": {list: `{"data":[{"id":"` + metaObjID + `","fields":{}}],"pageInfo":{"hasNextPage":false}}`},
		"list bad end cursor":   {list: `{"data":[],"pageInfo":{"hasNextPage":true,"endCursor":"abc"}}`},
		"object wrong key":      {objectGet: `{"data":{"field":` + metaObjectJSON + `}}`},
		"object both forms":     {objectGet: `{"id":"` + metaObjID + `","data":{"object":` + metaObjectJSON + `}}`},
		"object data is array":  {objectGet: `{"data":[` + metaObjectJSON + `]}`},
		"object without id":     {objectGet: `{"nameSingular":"x"}`},
		"object bad field":      {objectGet: `{"id":"` + metaObjID + `","fields":[{"id":"x"}]}`},
		"object bad options":    {objectGet: `{"id":"` + metaObjID + `","fields":[{"id":"` + metaFieldID + `","options":"x"}]}`},
		"object other id":       {objectGet: strings.Replace(metaObjectJSON, metaObjID, metaOtherID, 1)},
		"field other id":        {fldGet: strings.Replace(metaFieldJSON, metaFieldID, metaOtherID, 1)},
		"field legacy other id": {fldGet: `{"data":{"field":` + strings.Replace(metaFieldJSON, metaFieldID, metaOtherID, 1) + `}}`},
		"field both forms":      {fldGet: `{"id":"` + metaFieldID + `","data":{"field":` + metaFieldJSON + `}}`},
		"field not an object":   {fldGet: `[]`},
	} {
		serveMeta(t, format)
		handlers := []struct {
			h    capability.Handler
			args string
			body string
		}{
			{invokeMetaObjectsList, `{}`, format.list},
			{invokeMetaObjectsGet, `{"id":"` + metaObjID + `"}`, format.objectGet},
			{invokeMetaFieldsGet, `{"id":"` + metaFieldID + `"}`, format.fldGet},
		}
		for _, h := range handlers {
			if h.body == "" {
				continue
			}
			if _, err := runMeta(t, h.h, h.args); classOf(err) != provider.ClassInvalidResponse {
				t.Errorf("%s: err = %v, want invalid-response", name, err)
			}
		}
	}
}

func TestMetadataToolsRefuseObjectTargetsBeforeSecretAndRequest(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, test := range []struct {
		handler capability.Handler
		args    string
	}{
		{invokeMetaObjectsList, `{}`}, {invokeMetaObjectsGet, `{"id":"` + metaObjID + `"}`},
		{invokeMetaFieldsGet, `{"id":"` + metaFieldID + `"}`},
	} {
		// A nil resolver would fail on any secret access.
		_, err := test.handler(context.Background(), targetConnection("object/person"), nil, red, json.RawMessage(test.args))
		if err == nil || classOf(err) != "" || !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s err = %v", test.args, err)
		}
	}
}

func TestMetadataBadArgumentsFailBeforeIO(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	for _, test := range []struct {
		handler capability.Handler
		args    string
	}{
		{invokeMetaObjectsGet, `{"id":"../etc"}`},
		{invokeMetaObjectsGet, `{"id":"` + metaObjID + `/fields"}`},
		{invokeMetaFieldsGet, `{"id":"x?y=1"}`},
		{invokeMetaObjectsList, `{"limit":101}`},
		{invokeMetaObjectsList, `{"cursor":"AAAA"}`},
	} {
		if _, err := test.handler(context.Background(), targetConnection(), resolver(red), red, json.RawMessage(test.args)); err == nil || classOf(err) != "" {
			t.Errorf("%s: err = %v", test.args, err)
		}
	}
}

func TestMetadataListPaginationUsesTheBoundUUIDCursor(t *testing.T) {
	calls := serveMeta(t, metaFormats[0])
	out, err := runMeta(t, invokeMetaObjectsList, `{"limit":7}`)
	if err != nil {
		t.Fatal(err)
	}
	var page MetaObjectList
	if json.Unmarshal([]byte(out), &page) != nil || page.NextCursor == "" || !page.HasMore {
		t.Fatal(out)
	}
	if strings.Contains(page.NextCursor, metaOtherID) {
		t.Errorf("cursor is not opaque: %s", page.NextCursor)
	}
	if _, err := runMeta(t, invokeMetaObjectsList, `{"limit":7,"cursor":"`+page.NextCursor+`"}`); err != nil {
		t.Fatal(err)
	}
	query, _ := url.ParseQuery((*calls)[1].Query)
	if (*calls)[0].Query != "limit=7" || query.Get("limit") != "7" || query.Get("starting_after") != metaOtherID || len(query) != 2 {
		t.Errorf("requests = %+v", *calls)
	}

	// The last page has no cursor.
	serveMeta(t, metaFormat{list: `{"data":[],"pageInfo":{"hasNextPage":false,"endCursor":"` + metaOtherID + `"}}`})
	out, err = runMeta(t, invokeMetaObjectsList, `{}`)
	if err != nil || strings.Contains(out, "next_cursor") || !strings.Contains(out, `"has_more":false`) {
		t.Errorf("last page = %s, %v", out, err)
	}

	// A cursor of another connection or a manipulated one never reaches the network.
	refuse(t)
	red := &redact.Redactor{}
	forged := provider.EncodeCursor(provider.CursorBinding("metaobjects.list", "crm"), "not-a-uuid")
	for name, resolved := range map[string]string{page.NextCursor: "other", forged: "crm", page.NextCursor + "x": "crm"} {
		conn := targetConnection()
		conn.Name = resolved
		if _, err := invokeMetaObjectsList(context.Background(), conn, resolver(red), red,
			json.RawMessage(`{"cursor":"`+name+`"}`)); err == nil || classOf(err) != "" {
			t.Errorf("cursor accepted for %s: %v", resolved, err)
		}
	}
}

func TestMetadataListRejectsMoreObjectsThanRequested(t *testing.T) {
	serveMeta(t, metaFormat{list: `{"data":[` + metaObjectJSON + `,` + metaObjectJSON + `],"pageInfo":{"hasNextPage":false}}`})
	if _, err := runMeta(t, invokeMetaObjectsList, `{"limit":1}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestMetadataForbiddenNamesThePermissionOnly(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{"message":"provider-text-canary"}`), nil
	})
	stubLimiter(t, cloudKey)
	_, err := runMeta(t, invokeMetaObjectsGet, `{"id":"`+metaObjID+`"}`)
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Data model") ||
		strings.Contains(err.Error(), "provider-text-canary") {
		t.Errorf("err = %v", err)
	}
}

func TestMetadataCapsListsAndText(t *testing.T) {
	var fields, options []string
	for i := 0; i < metaFieldsMax+5; i++ {
		fields = append(fields, `{"id":"`+metaFieldID+`","type":"TEXT","name":"f","label":"l"}`)
	}
	for i := 0; i < metaOptionsMax+5; i++ {
		options = append(options, `{"value":"v","label":"l"}`)
	}
	long := strings.Repeat("x", 5000)
	body := `{"id":"` + metaObjID + `","labelSingular":"` + long + `","description":"` + long + `","fields":[` +
		strings.Join(fields, ",") + `]}`
	serveMeta(t, metaFormat{objectGet: body, fldGet: `{"id":"` + metaFieldID + `","type":"SELECT","options":[` + strings.Join(options, ",") + `]}`})
	out, err := runMeta(t, invokeMetaObjectsGet, `{"id":"`+metaObjID+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var object MetaObject
	if json.Unmarshal([]byte(out), &object); len(object.Fields) != metaFieldsMax || !object.MoreFields ||
		object.FieldCount != metaFieldsMax+5 || len(object.LabelSingular) != metaNameMax || len(object.Description) != metaTextMax {
		t.Errorf("object = %d fields, more %v, count %d", len(object.Fields), object.MoreFields, object.FieldCount)
	}
	out, err = runMeta(t, invokeMetaFieldsGet, `{"id":"`+metaFieldID+`"}`)
	var field MetaField
	if json.Unmarshal([]byte(out), &field); err != nil || len(field.Options) != metaOptionsMax || !field.MoreOptions {
		t.Errorf("field = %d options, more %v, %v", len(field.Options), field.MoreOptions, err)
	}
}

func TestMetadataToolsStayOutOfEveryProfile(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, d := range []capability.Descriptor{metaObjectsList, metaObjectsGet, metaFieldsGet} {
		if d.Risk.DataSensitivity != "twentycrm-schema" || d.Risk.Effect != capability.EffectRead ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld || d.RequiresToolAllowList {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
		for _, profile := range metadata.Profiles {
			if strings.Contains(strings.Join(profile.Tools, " "), d.ID) {
				t.Errorf("%s is in profile %s", d.ID, profile.ID)
			}
		}
	}
}
