package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const metaWriteCn = "canary-meta-write-5521"

type metaWriteCall struct{ Method, Path, Body string }

// serveMetaWrite answers reads with the entity of the path and every writing request with write.
func serveMetaWrite(t *testing.T, reads map[string]string, write func() (*http.Response, error)) *[]metaWriteCall {
	t.Helper()
	calls := &[]metaWriteCall{}
	serve(t, func(request *http.Request) (*http.Response, error) {
		call := metaWriteCall{Method: request.Method, Path: request.URL.Path}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			call.Body = string(data)
		}
		*calls = append(*calls, call)
		if request.Method == http.MethodGet {
			if body, ok := reads[request.URL.Path]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
			t.Errorf("unexpected read %s", request.URL.Path)
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
		return write()
	})
	stubLimiter(t, cloudKey)
	return calls
}

func metaWrites(calls *[]metaWriteCall) []metaWriteCall {
	out := []metaWriteCall{}
	for _, call := range *calls {
		if call.Method != http.MethodGet {
			out = append(out, call)
		}
	}
	return out
}

func metaAnswer(body string) func() (*http.Response, error) {
	return func() (*http.Response, error) { return jsonResponse(http.StatusOK, body), nil }
}

func runMetaWrite(handler capability.Handler, args string, targets ...string) (string, error) {
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

func metaObjectBody(id string, custom, system bool) string {
	flag := map[bool]string{true: "true", false: "false"}
	return `{"id":"` + id + `","nameSingular":"deal","namePlural":"deals","labelSingular":"Deal","labelPlural":"Deals",` +
		`"isCustom":` + flag[custom] + `,"isSystem":` + flag[system] + `,"isActive":true,"fields":[` + metaFieldJSON + `]}`
}

func metaFieldBody(kind string, custom, system bool) string {
	flag := map[bool]string{true: "true", false: "false"}
	return `{"id":"` + metaFieldID + `","type":"` + kind + `","name":"stage","label":"Stage","isCustom":` + flag[custom] +
		`,"isSystem":` + flag[system] + `,"isActive":true,"objectMetadataId":"` + metaObjID + `"}`
}

func TestMetaObjectsCreateSendsOneFixedRequest(t *testing.T) {
	for name, body := range map[string]string{
		"new":    metaObjectBody(metaObjID, true, false),
		"legacy": `{"data":{"createOneObject":` + metaObjectBody(metaObjID, true, false) + `}}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, nil, metaAnswer(body))
			out, err := runMetaWrite(invokeMetaObjectsCreate, `{"name_singular":"deal","name_plural":"deals",`+
				`"label_singular":"Deal","label_plural":"Deals","description":"Sales"}`)
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 1 || (*calls)[0].Method != http.MethodPost || (*calls)[0].Path != metadataObjectsPath {
				t.Fatalf("calls = %+v", *calls)
			}
			var sent map[string]any
			if json.Unmarshal([]byte((*calls)[0].Body), &sent) != nil || len(sent) != 5 ||
				sent["nameSingular"] != "deal" || sent["namePlural"] != "deals" || sent["labelSingular"] != "Deal" ||
				sent["labelPlural"] != "Deals" || sent["description"] != "Sales" {
				t.Errorf("body = %s", (*calls)[0].Body)
			}
			if !strings.Contains(out, `"id":"`+metaObjID+`"`) || !strings.Contains(out, `"field_count":1`) {
				t.Errorf("result = %s", out)
			}
		})
	}
}

func TestMetaObjectsUpdateReadsOnceAndPatches(t *testing.T) {
	path := metadataObjectsPath + "/" + metaObjID
	calls := serveMetaWrite(t, map[string]string{path: metaObjectBody(metaObjID, true, false)},
		metaAnswer(metaObjectBody(metaObjID, true, false)))
	out, err := runMetaWrite(invokeMetaObjectsUpdate, `{"id":"`+metaObjID+`","label_singular":"Offer",`+
		`"description":"","is_active":false}`)
	if err != nil {
		t.Fatal(err)
	}
	sent := metaWrites(calls)
	if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].Path != path {
		t.Fatalf("writes = %+v", sent)
	}
	var body map[string]any
	if json.Unmarshal([]byte(sent[0].Body), &body) != nil || len(body) != 3 || body["labelSingular"] != "Offer" ||
		body["description"] != "" || body["isActive"] != false {
		t.Errorf("body = %s", sent[0].Body)
	}
	if !strings.Contains(out, metaObjID) {
		t.Errorf("result = %s", out)
	}
}

func TestMetaObjectsUpdateReachesStandardAndSystemObjects(t *testing.T) {
	path := metadataObjectsPath + "/" + metaObjID
	for name, test := range map[string]struct {
		custom, system bool
		args           string
		want           map[string]any
	}{
		"standard object, description and is_active": {false, false,
			`{"id":"` + metaObjID + `","description":"x","is_active":false}`,
			map[string]any{"description": "x", "isActive": false}},
		"standard object, labels": {false, false, `{"id":"` + metaObjID + `","label_plural":"Offers"}`,
			map[string]any{"labelPlural": "Offers"}},
		"system object, label": {false, true, `{"id":"` + metaObjID + `","label_plural":"x"}`,
			map[string]any{"labelPlural": "x"}},
		"system object, is_active": {false, true, `{"id":"` + metaObjID + `","is_active":false}`,
			map[string]any{"isActive": false}},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, map[string]string{path: metaObjectBody(metaObjID, test.custom, test.system)},
				metaAnswer(metaObjectBody(metaObjID, test.custom, test.system)))
			if _, err := runMetaWrite(invokeMetaObjectsUpdate, test.args); err != nil {
				t.Fatal(err)
			}
			sent := metaWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].Path != path {
				t.Fatalf("writes = %+v", sent)
			}
			var body map[string]any
			_ = json.Unmarshal([]byte(sent[0].Body), &body)
			if len(body) != len(test.want) {
				t.Errorf("body = %s", sent[0].Body)
			}
			for key, want := range test.want {
				if body[key] != want {
					t.Errorf("body = %s", sent[0].Body)
				}
			}
		})
	}
}

func TestMetaFieldsCreateSendsOneFixedRequest(t *testing.T) {
	for name, body := range map[string]string{
		"new":    metaFieldBody("SELECT", true, false),
		"legacy": `{"data":{"createOneField":` + metaFieldBody("SELECT", true, false) + `}}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, nil, metaAnswer(body))
			out, err := runMetaWrite(invokeMetaFieldsCreate, `{"object_id":"`+metaObjID+`","type":"SELECT",`+
				`"name":"stage","label":"Stage","is_nullable":false,"options":[`+
				`{"label":"Open","value":"OPEN","color":"red"},{"label":"Done","value":"DONE"}]}`)
			if err != nil {
				t.Fatal(err)
			}
			sent := metaWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].Path != metadataFieldsPath {
				t.Fatalf("writes = %+v", sent)
			}
			var payload struct {
				ObjectMetadataID string           `json:"objectMetadataId"`
				Type, Name       string           `json:"-"`
				Options          []map[string]any `json:"options"`
				IsNullable       *bool            `json:"isNullable"`
			}
			var raw map[string]any
			if json.Unmarshal([]byte(sent[0].Body), &payload) != nil || json.Unmarshal([]byte(sent[0].Body), &raw) != nil ||
				payload.ObjectMetadataID != metaObjID || raw["type"] != "SELECT" || raw["name"] != "stage" ||
				raw["label"] != "Stage" || payload.IsNullable == nil || *payload.IsNullable || len(payload.Options) != 2 ||
				payload.Options[0]["color"] != "red" || payload.Options[1]["color"] != "gray" ||
				payload.Options[1]["position"] != float64(1) || len(raw) != 6 {
				t.Errorf("body = %s", sent[0].Body)
			}
			if !strings.Contains(out, `"type":"SELECT"`) {
				t.Errorf("result = %s", out)
			}
		})
	}
}

func TestMetaFieldsCreateAcceptsEveryAllowedType(t *testing.T) {
	for _, kind := range metaCreatableTypes {
		t.Run(kind, func(t *testing.T) {
			calls := serveMetaWrite(t, nil, metaAnswer(metaFieldBody(kind, true, false)))
			args := `{"object_id":"` + metaObjID + `","type":"` + kind + `","name":"stage","label":"Stage"`
			if kind == "SELECT" || kind == "MULTI_SELECT" {
				args += `,"options":[{"label":"Open","value":"OPEN"}]`
			}
			if _, err := runMetaWrite(invokeMetaFieldsCreate, args+`}`); err != nil || len(metaWrites(calls)) != 1 {
				t.Errorf("err = %v, writes = %+v", err, metaWrites(calls))
			}
		})
	}
}

func TestMetaFieldsCreateRefusesBeforeAnyRequest(t *testing.T) {
	long := strings.Repeat("a", 64)
	options := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `{"label":"L","value":"V` + strings.Repeat("A", i%5) + string(rune('A'+i/26)) + string(rune('a'+i%26)) + `"}`
		}
		return `[` + strings.Join(items, ",") + `]`
	}
	base := `"object_id":"` + metaObjID + `","name":"stage","label":"Stage"`
	for name, args := range map[string]string{
		"relation":            `{` + base + `,"type":"RELATION"}`,
		"morph relation":      `{` + base + `,"type":"MORPH_RELATION"}`,
		"unlisted type":       `{` + base + `,"type":"ACTOR"}`,
		"lowercase type":      `{` + base + `,"type":"text"}`,
		"uppercase name":      `{"object_id":"` + metaObjID + `","type":"TEXT","name":"Stage","label":"x"}`,
		"digit first":         `{"object_id":"` + metaObjID + `","type":"TEXT","name":"1st","label":"x"}`,
		"underscore name":     `{"object_id":"` + metaObjID + `","type":"TEXT","name":"my_field","label":"x"}`,
		"long name":           `{"object_id":"` + metaObjID + `","type":"TEXT","name":"a` + long + `","label":"x"}`,
		"empty label":         `{"object_id":"` + metaObjID + `","type":"TEXT","name":"a","label":" "}`,
		"control in label":    `{"object_id":"` + metaObjID + `","type":"TEXT","name":"a","label":"x\u0007"}`,
		"long label":          `{"object_id":"` + metaObjID + `","type":"TEXT","name":"a","label":"` + strings.Repeat("x", 101) + `"}`,
		"long description":    `{` + base + `,"type":"TEXT","description":"` + strings.Repeat("x", 501) + `"}`,
		"bad object id":       `{"object_id":"x?y=1","type":"TEXT","name":"a","label":"x"}`,
		"select without opts": `{` + base + `,"type":"SELECT"}`,
		"text with options":   `{` + base + `,"type":"TEXT","options":[{"label":"a","value":"A"}]}`,
		"101 options":         `{` + base + `,"type":"SELECT","options":` + options(101) + `}`,
		"empty options":       `{` + base + `,"type":"SELECT","options":[]}`,
		"duplicate value":     `{` + base + `,"type":"SELECT","options":[{"label":"a","value":"A"},{"label":"b","value":"A"}]}`,
		"bad option value":    `{` + base + `,"type":"SELECT","options":[{"label":"a","value":"a b"}]}`,
		"bad color":           `{` + base + `,"type":"SELECT","options":[{"label":"a","value":"A","color":"pink2"}]}`,
		"relation payload":    `{` + base + `,"type":"TEXT","relationCreationPayload":{}}`,
		"free body":           `{` + base + `,"type":"TEXT","settings":{}}`,
		"is_active":           `{` + base + `,"type":"TEXT","is_active":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			if _, err := runMetaWrite(invokeMetaFieldsCreate, args); !asInvalidOK(err) {
				t.Errorf("err = %v, want invalid-request", err)
			}
		})
	}
	t.Run("100 options pass", func(t *testing.T) {
		owner := metadataObjectsPath + "/" + metaObjID
		calls := serveMetaWrite(t, map[string]string{owner: metaObjectBody(metaObjID, true, false)},
			metaAnswer(metaFieldBody("SELECT", true, false)))
		if _, err := runMetaWrite(invokeMetaFieldsCreate, `{`+base+`,"type":"SELECT","options":`+options(100)+`}`); err != nil ||
			len(metaWrites(calls)) != 1 {
			t.Errorf("err = %v", err)
		}
	})
}

func TestMetaObjectWritesRefuseInvalidInputBeforeAnyRequest(t *testing.T) {
	for name, test := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"singular name": {invokeMetaObjectsCreate, `{"name_singular":"Deal","name_plural":"deals","label_singular":"a","label_plural":"b"}`},
		"plural name":   {invokeMetaObjectsCreate, `{"name_singular":"deal","name_plural":"de-als","label_singular":"a","label_plural":"b"}`},
		"same names":    {invokeMetaObjectsCreate, `{"name_singular":"deal","name_plural":"deal","label_singular":"a","label_plural":"b"}`},
		"missing label": {invokeMetaObjectsCreate, `{"name_singular":"deal","name_plural":"deals","label_singular":"a"}`},
		"extra name":    {invokeMetaObjectsUpdate, `{"id":"` + metaObjID + `","name_singular":"x","label_singular":"a"}`},
		"empty update":  {invokeMetaObjectsUpdate, `{"id":"` + metaObjID + `"}`},
		"bad id":        {invokeMetaObjectsUpdate, `{"id":"x?y=1","label_singular":"a"}`},
		"field rename":  {invokeMetaFieldsUpdate, `{"id":"` + metaFieldID + `","name":"x"}`},
		"field type":    {invokeMetaFieldsUpdate, `{"id":"` + metaFieldID + `","type":"TEXT","label":"x"}`},
		"field empty":   {invokeMetaFieldsUpdate, `{"id":"` + metaFieldID + `"}`},
		"field bad id":  {invokeMetaFieldsUpdate, `{"id":"nope","label":"x"}`},
		"field options": {invokeMetaFieldsUpdate, `{"id":"` + metaFieldID + `","options":[]}`},
		"not json":      {invokeMetaFieldsUpdate, `[]`},
	} {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			if _, err := runMetaWrite(test.handler, test.args); !asInvalidOK(err) {
				t.Errorf("err = %v, want invalid-request", err)
			}
		})
	}
}

func TestMetaFieldsCreateLeavesObjectAcceptanceToTwenty(t *testing.T) {
	for name, status := range map[string]int{"accepted": http.StatusOK, "400": http.StatusBadRequest,
		"422": http.StatusUnprocessableEntity} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, nil, func() (*http.Response, error) {
				if status != http.StatusOK {
					return jsonResponse(status, `{"message":"`+metaWriteCn+`"}`), nil
				}
				return jsonResponse(status, metaFieldBody("TEXT", true, false)), nil
			})
			_, err := runMetaWrite(invokeMetaFieldsCreate, `{"object_id":"`+metaObjID+`","type":"TEXT","name":"stage","label":"Stage"}`)
			sent := metaWrites(calls)
			if len(*calls) != 1 || len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].Path != metadataFieldsPath {
				t.Fatalf("calls = %+v", *calls)
			}
			if status == http.StatusOK {
				if err != nil {
					t.Error(err)
				}
			} else if err == nil || strings.Contains(err.Error(), metaWriteCn) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestMetaFieldsUpdateReadsOnceAndPatches(t *testing.T) {
	path := metadataFieldsPath + "/" + metaFieldID
	calls := serveMetaWrite(t, map[string]string{path: metaFieldBody("SELECT", true, false)},
		metaAnswer(`{"data":{"updateOneField":`+metaFieldBody("SELECT", true, false)+`}}`))
	out, err := runMetaWrite(invokeMetaFieldsUpdate, `{"id":"`+metaFieldID+`","label":"Phase","is_active":false,`+
		`"options":[{"id":"`+metaOptID+`","label":"Open","value":"OPEN"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	sent := metaWrites(calls)
	if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].Path != path {
		t.Fatalf("writes = %+v", sent)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(sent[0].Body), &body)
	options, _ := body["options"].([]any)
	first, _ := options[0].(map[string]any)
	if len(body) != 3 || body["label"] != "Phase" || body["isActive"] != false || len(options) != 1 || first["id"] != metaOptID ||
		first["value"] != "OPEN" {
		t.Errorf("body = %s", sent[0].Body)
	}
	if !strings.Contains(out, metaFieldID) {
		t.Errorf("result = %s", out)
	}
}

func TestMetaFieldsUpdateRefusals(t *testing.T) {
	path := metadataFieldsPath + "/" + metaFieldID
	for name, test := range map[string]struct{ field, args string }{
		"relation":                 {metaFieldBody("RELATION", true, false), `{"id":"` + metaFieldID + `","label":"x"}`},
		"morph relation":           {metaFieldBody("MORPH_RELATION", true, false), `{"id":"` + metaFieldID + `","label":"x"}`},
		"standard relation":        {metaFieldBody("RELATION", false, false), `{"id":"` + metaFieldID + `","is_active":false}`},
		"options on text":          {metaFieldBody("TEXT", true, false), `{"id":"` + metaFieldID + `","options":[{"label":"a","value":"A"}]}`},
		"options on standard text": {metaFieldBody("TEXT", false, false), `{"id":"` + metaFieldID + `","options":[{"label":"a","value":"A"}]}`},
		"other id":                 {strings.Replace(metaFieldBody("TEXT", true, false), metaFieldID, metaOtherID, 1), `{"id":"` + metaFieldID + `","label":"x"}`},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, map[string]string{path: test.field}, metaAnswer(`{}`))
			_, err := runMetaWrite(invokeMetaFieldsUpdate, test.args)
			if err == nil || len(metaWrites(calls)) != 0 || strings.Contains(err.Error(), metaTypeCn) {
				t.Errorf("err = %v, writes = %+v", err, metaWrites(calls))
			}
		})
	}
}

func TestMetaFieldsUpdateReachesStandardAndSystemFields(t *testing.T) {
	path := metadataFieldsPath + "/" + metaFieldID
	oneWrite := func(t *testing.T, field, args string) map[string]any {
		t.Helper()
		calls := serveMetaWrite(t, map[string]string{path: field}, metaAnswer(field))
		if _, err := runMetaWrite(invokeMetaFieldsUpdate, args); err != nil {
			t.Fatal(err)
		}
		sent := metaWrites(calls)
		if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].Path != path {
			t.Fatalf("writes = %+v", sent)
		}
		var body map[string]any
		_ = json.Unmarshal([]byte(sent[0].Body), &body)
		return body
	}
	t.Run("standard select, options as complete list", func(t *testing.T) {
		body := oneWrite(t, metaFieldBody("SELECT", false, false), `{"id":"`+metaFieldID+`","options":[`+
			`{"id":"`+metaOptID+`","label":"Open","value":"OPEN"},{"label":"Won","value":"WON","color":"green"}]}`)
		options, _ := body["options"].([]any)
		first, _ := options[0].(map[string]any)
		second, _ := options[1].(map[string]any)
		if len(body) != 1 || len(options) != 2 || first["id"] != metaOptID || first["value"] != "OPEN" ||
			second["value"] != "WON" || second["color"] != "green" {
			t.Errorf("body = %v", body)
		}
	})
	t.Run("standard field, description and is_active", func(t *testing.T) {
		body := oneWrite(t, metaFieldBody("TEXT", false, false),
			`{"id":"`+metaFieldID+`","description":"d","is_active":false}`)
		if len(body) != 2 || body["description"] != "d" || body["isActive"] != false {
			t.Errorf("body = %v", body)
		}
	})
	t.Run("system field, is_active", func(t *testing.T) {
		body := oneWrite(t, metaFieldBody("TEXT", false, true), `{"id":"`+metaFieldID+`","is_active":false}`)
		if len(body) != 1 || body["isActive"] != false {
			t.Errorf("body = %v", body)
		}
	})
	t.Run("unknown type is left to Twenty", func(t *testing.T) {
		body := oneWrite(t, metaFieldBody(metaTypeCn, true, false), `{"id":"`+metaFieldID+`","label":"x"}`)
		if len(body) != 1 || body["label"] != "x" {
			t.Errorf("body = %v", body)
		}
	})
}

func TestMetaUpdatesHideTwentyRefusalText(t *testing.T) {
	objPath := metadataObjectsPath + "/" + metaObjID
	fieldPath := metadataFieldsPath + "/" + metaFieldID
	for name, test := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"object": {invokeMetaObjectsUpdate, `{"id":"` + metaObjID + `","is_active":false}`},
		"field":  {invokeMetaFieldsUpdate, `{"id":"` + metaFieldID + `","label":"x"}`},
	} {
		for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
			t.Run(name+" "+http.StatusText(status), func(t *testing.T) {
				calls := serveMetaWrite(t, map[string]string{objPath: metaObjectBody(metaObjID, false, true),
					fieldPath: metaFieldBody("TEXT", false, true)}, func() (*http.Response, error) {
					return jsonResponse(status, `{"message":"`+metaWriteCn+`"}`), nil
				})
				_, err := runMetaWrite(test.handler, test.args)
				if err == nil || strings.Contains(err.Error(), metaWriteCn) || len(metaWrites(calls)) != 1 {
					t.Errorf("err = %v, writes = %+v", err, metaWrites(calls))
				}
			})
		}
	}
}

func metaWriteInvocations() map[string]struct {
	handler capability.Handler
	args    string
	reads   map[string]string
} {
	type invocation = struct {
		handler capability.Handler
		args    string
		reads   map[string]string
	}
	return map[string]invocation{
		"objects create": {invokeMetaObjectsCreate, `{"name_singular":"deal","name_plural":"deals","label_singular":"a","label_plural":"b"}`, nil},
		"objects update": {invokeMetaObjectsUpdate, `{"id":"` + metaObjID + `","label_plural":"b"}`,
			map[string]string{metadataObjectsPath + "/" + metaObjID: metaObjectBody(metaObjID, true, false)}},
		"fields create": {invokeMetaFieldsCreate, `{"object_id":"` + metaObjID + `","type":"TEXT","name":"a","label":"A"}`, nil},
		"fields update": {invokeMetaFieldsUpdate, `{"id":"` + metaFieldID + `","label":"x"}`,
			map[string]string{metadataFieldsPath + "/" + metaFieldID: metaFieldBody("TEXT", true, false)}},
	}
}

func TestMetaWritesNeedAConnectionWithoutObjectTargets(t *testing.T) {
	for name, test := range metaWriteInvocations() {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			_, err := runMetaWrite(test.handler, test.args, "object/person")
			if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
				t.Errorf("err = %v, want invalid-request without the target", err)
			}
		})
	}
}

func TestMetaWritesNeverRetryAndReportUncertainty(t *testing.T) {
	for name, test := range metaWriteInvocations() {
		for outcome, write := range map[string]func() (*http.Response, error){
			"server error": func() (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+metaWriteCn+`"}`), nil
			},
			"timeout": func() (*http.Response, error) {
				return nil, &net.DNSError{IsTimeout: true, Err: metaWriteCn}
			},
			"reset":    func() (*http.Response, error) { return nil, errors.New(metaWriteCn + ": connection reset by peer") },
			"not json": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `<html>`+metaWriteCn), nil },
			"unusable": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"data":[1]}`), nil },
			"foreign": func() (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"id":"`+metaOtherID+`","type":"TEXT"}`), nil
			},
		} {
			t.Run(name+" "+outcome, func(t *testing.T) {
				calls := serveMetaWrite(t, test.reads, write)
				_, err := runMetaWrite(test.handler, test.args)
				if err == nil || len(metaWrites(calls)) != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
					strings.Contains(err.Error(), metaWriteCn) {
					t.Errorf("err = %v, writes = %d", err, len(metaWrites(calls)))
				}
			})
		}
	}
}

func TestMetaWritesMapPermissionAndRefusalsWithoutProviderText(t *testing.T) {
	for name, test := range metaWriteInvocations() {
		t.Run(name, func(t *testing.T) {
			serveMetaWrite(t, test.reads, func() (*http.Response, error) {
				return jsonResponse(http.StatusForbidden, `{"message":"`+metaWriteCn+`"}`), nil
			})
			_, err := runMetaWrite(test.handler, test.args)
			var failure *provider.Error
			if !errors.As(err, &failure) || failure.Class != provider.ClassPermission ||
				!strings.Contains(err.Error(), "Data model") || strings.Contains(err.Error(), metaWriteCn) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestMetaWriteDescriptorsAreGuarded(t *testing.T) {
	for _, d := range []capability.Descriptor{metaObjectsCreate, metaObjectsUpdate, metaFieldsCreate, metaFieldsUpdate} {
		wantEffect := capability.EffectUpdate
		wantIdem := capability.IdempotencyIdempotent
		if strings.HasSuffix(d.ID, ".create") {
			wantEffect, wantIdem = capability.EffectCreate, capability.IdempotencyNonIdempotent
		}
		risk := d.Risk
		if !d.RequiresToolAllowList || risk.Effect != wantEffect || risk.Idempotency != wantIdem ||
			risk.Confirmation != capability.ConfirmationRequired || !risk.OpenWorld ||
			risk.DataSensitivity != metadataSensitivity {
			t.Errorf("%s = %+v", d.ID, d)
		}
		if !strings.Contains(d.Description, "every user and integration") {
			t.Errorf("%s does not name the workspace-wide effect", d.ID)
		}
	}
	if !strings.Contains(metaObjectsCreate.Description, "never does itself") ||
		!strings.Contains(metaFieldsUpdate.Description, "clears that value") ||
		!strings.Contains(metaFieldsUpdate.Description, "workflow filters") ||
		!strings.Contains(metaObjectsUpdate.Description, "hides it") {
		t.Error("descriptions omit the target, option, or visibility consequence")
	}
}
