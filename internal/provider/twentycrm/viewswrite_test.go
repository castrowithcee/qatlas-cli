package twentycrm

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	vwDateA    = "eeeeeeee-0000-4000-8000-000000000005"
	vwDateB    = "eeeeeeee-0000-4000-8000-000000000006"
	vwDay      = "eeeeeeee-0000-4000-8000-000000000007"
	vwText     = "eeeeeeee-0000-4000-8000-000000000008"
	vwOff      = "eeeeeeee-0000-4000-8000-000000000009"
	vwNewView  = "66666666-0000-4000-8000-0000000000f1"
	vwProvider = "canary-view-write-3377"
)

func vwField(id, kind string, active bool) string {
	flag := map[bool]string{true: "true", false: "false"}
	return `{"id":"` + id + `","type":"` + kind + `","name":"f` + id[7:8] + `","label":"F","isCustom":true,"isSystem":false,` +
		`"isActive":` + flag[active] + `,"objectMetadataId":"` + metaObjID + `"}`
}

func vwObject() string {
	return `{"id":"` + metaObjID + `","nameSingular":"deal","namePlural":"deals","labelSingular":"Deal","labelPlural":"Deals",` +
		`"isCustom":true,"isSystem":false,"isActive":true,"fields":[` + vwField(metaFieldID, "SELECT", true) + `,` +
		vwField(vwDateA, "DATE_TIME", true) + `,` + vwField(vwDateB, "DATE_TIME", true) + `,` + vwField(vwDay, "DATE", true) +
		`,` + vwField(vwText, "TEXT", true) + `,` + vwField(vwOff, "SELECT", false) + `]}`
}

// vwView is a view as Twenty answers it; key may be empty.
func vwView(id, visibility, kind, key string, custom bool) string {
	keyJSON := "null"
	if key != "" {
		keyJSON = `"` + key + `"`
	}
	return `{"id":"` + id + `","name":"Open deals","objectMetadataId":"` + metaObjID + `","type":"` + kind + `","key":` + keyJSON +
		`,"icon":"IconList","position":1,"isCustom":` + map[bool]string{true: "true", false: "false"}[custom] +
		`,"visibility":"` + visibility + `","createdByUserWorkspaceId":"` + creatorCn + `","applicationId":"` + appCn + `"}`
}

func vwReads(extra map[string]string) map[string]string {
	reads := map[string]string{metadataObjectsPath + "/" + metaObjID: vwObject()}
	for path, body := range extra {
		reads[path] = body
	}
	return reads
}

func vwCreate(t *testing.T, args, answer string) (string, error, *[]metaWriteCall) {
	t.Helper()
	calls := serveMetaWrite(t, vwReads(nil), metaAnswer(answer))
	out, err := runMetaWrite(invokeViewsCreate, args)
	return out, err, calls
}

func TestViewsCreateSendsOneFixedRequest(t *testing.T) {
	base := `"name":"Open deals","object_id":"` + metaObjID + `"`
	tests := map[string]struct{ args, kind, want string }{
		"table": {`{` + base + `}`, "TABLE", `"icon":"IconList"`},
		"list":  {`{` + base + `,"type":"LIST","icon":"IconBolt"}`, "LIST", `"icon":"IconBolt"`},
		"kanban": {`{` + base + `,"type":"KANBAN","main_group_by_field_id":"` + metaFieldID + `"}`, "KANBAN",
			`"mainGroupByFieldMetadataId":"` + metaFieldID + `"`},
		"calendar": {`{` + base + `,"type":"CALENDAR","calendar_layout":"WEEK","calendar_field_id":"` + vwDateA +
			`","calendar_end_field_id":"` + vwDateB + `"}`, "CALENDAR", `"calendarEndFieldMetadataId":"` + vwDateB + `"`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			out, err, calls := vwCreate(t, test.args, vwView(vwNewView, "WORKSPACE", test.kind, "", true))
			if err != nil {
				t.Fatal(err)
			}
			sent := metaWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].Path != viewsPath || len(*calls) != 2 ||
				(*calls)[0].Path != metadataObjectsPath+"/"+metaObjID {
				t.Fatalf("calls = %+v", *calls)
			}
			if !strings.Contains(sent[0].Body, `"visibility":"WORKSPACE"`) || !strings.Contains(sent[0].Body, `"type":"`+test.kind+`"`) ||
				!strings.Contains(sent[0].Body, test.want) {
				t.Errorf("body = %s", sent[0].Body)
			}
			if !strings.Contains(out, `"id":"`+vwNewView+`"`) || !strings.Contains(out, `"object":"deal"`) ||
				strings.Contains(out, creatorCn) || strings.Contains(out, appCn) {
				t.Errorf("result = %s", out)
			}
		})
	}
}

func TestViewsCreateAcceptsTheWrappedAnswer(t *testing.T) {
	answer := `{"data":{"view":` + vwView(vwNewView, "WORKSPACE", "TABLE", "", true) + `}}`
	if _, err, _ := vwCreate(t, `{"name":"A","object_id":"`+metaObjID+`"}`, answer); err != nil {
		t.Fatal(err)
	}
}

func TestViewsCreateRefusesBadArgumentsBeforeAnyRequest(t *testing.T) {
	base := `"name":"Open deals","object_id":"` + metaObjID + `"`
	for name, args := range map[string]string{
		"no name":            `{"object_id":"` + metaObjID + `"}`,
		"no object":          `{"name":"A"}`,
		"empty name":         `{"name":" ","object_id":"` + metaObjID + `"}`,
		"reserved name":      `{"name":"and","object_id":"` + metaObjID + `"}`,
		"forbidden char":     `{"name":"a.b","object_id":"` + metaObjID + `"}`,
		"long name":          `{"name":"` + strings.Repeat("a", 101) + `","object_id":"` + metaObjID + `"}`,
		"bad object":         `{"name":"A","object_id":"deal"}`,
		"widget type":        `{` + base + `,"type":"TABLE_WIDGET"}`,
		"unknown type":       `{` + base + `,"type":"GRID"}`,
		"bad icon":           `{` + base + `,"icon":"list"}`,
		"visibility":         `{` + base + `,"visibility":"UNLISTED"}`,
		"free field":         `{` + base + `,"key":"INDEX"}`,
		"kanban no field":    `{` + base + `,"type":"KANBAN"}`,
		"kanban calendar":    `{` + base + `,"type":"KANBAN","main_group_by_field_id":"` + metaFieldID + `","calendar_layout":"DAY"}`,
		"table with group":   `{` + base + `,"main_group_by_field_id":"` + metaFieldID + `"}`,
		"table with layout":  `{` + base + `,"calendar_layout":"DAY"}`,
		"calendar no field":  `{` + base + `,"type":"CALENDAR","calendar_layout":"DAY"}`,
		"calendar no layout": `{` + base + `,"type":"CALENDAR","calendar_field_id":"` + vwDateA + `"}`,
		"calendar layout":    `{` + base + `,"type":"CALENDAR","calendar_layout":"YEAR","calendar_field_id":"` + vwDateA + `"}`,
		"calendar group":     `{` + base + `,"type":"CALENDAR","calendar_layout":"DAY","calendar_field_id":"` + vwDateA + `","main_group_by_field_id":"` + metaFieldID + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			if _, err := runMetaWrite(invokeViewsCreate, args); !asInvalidOK(err) {
				t.Errorf("err = %v, want invalid-request", err)
			}
		})
	}
}

func TestViewsCreateChecksObjectAndFieldsWithoutWriting(t *testing.T) {
	base := `"name":"A","object_id":"` + metaObjID + `"`
	calendar := func(fields string) string {
		return `{` + base + `,"type":"CALENDAR","calendar_layout":"DAY",` + fields + `}`
	}
	for name, args := range map[string]string{
		"kanban on text":     `{` + base + `,"type":"KANBAN","main_group_by_field_id":"` + vwText + `"}`,
		"kanban on inactive": `{` + base + `,"type":"KANBAN","main_group_by_field_id":"` + vwOff + `"}`,
		"kanban unknown":     `{` + base + `,"type":"KANBAN","main_group_by_field_id":"` + metaOtherID + `"}`,
		"calendar on select": calendar(`"calendar_field_id":"` + metaFieldID + `"`),
		"calendar on text":   calendar(`"calendar_field_id":"` + vwText + `"`),
		"calendar mixed end": calendar(`"calendar_field_id":"` + vwDateA + `","calendar_end_field_id":"` + vwDay + `"`),
		"calendar unknown":   calendar(`"calendar_field_id":"` + metaOtherID + `"`),
		"calendar bad end":   calendar(`"calendar_field_id":"` + vwDateA + `","calendar_end_field_id":"` + vwText + `"`),
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, vwReads(nil), metaAnswer(`{}`))
			_, err := runMetaWrite(invokeViewsCreate, args)
			if !asInvalidOK(err) || len(metaWrites(calls)) != 0 {
				t.Errorf("err = %v, writes = %+v", err, metaWrites(calls))
			}
		})
	}
	t.Run("unknown object", func(t *testing.T) {
		calls := serveMetaWrite(t, nil, metaAnswer(`{}`))
		serveNotFound := false
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodGet {
				t.Errorf("unexpected %s %s", request.Method, request.URL.Path)
			}
			serveNotFound = true
			return jsonResponse(http.StatusNotFound, `{"message":"`+vwProvider+`"}`), nil
		})
		stubLimiter(t, cloudKey)
		_, err := runMetaWrite(invokeViewsCreate, `{`+base+`}`)
		if err == nil || !serveNotFound || len(metaWrites(calls)) != 0 || strings.Contains(err.Error(), vwProvider) {
			t.Errorf("err = %v", err)
		}
	})
	for name, body := range map[string]string{
		"system object":   strings.Replace(vwObject(), `"isSystem":false,"isActive":true,"fields"`, `"isSystem":true,"isActive":true,"fields"`, 1),
		"inactive object": strings.Replace(vwObject(), `"isActive":true,"fields"`, `"isActive":false,"fields"`, 1),
		"other object":    strings.Replace(vwObject(), `{"id":"`+metaObjID+`","nameSingular"`, `{"id":"`+metaOtherID+`","nameSingular"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, map[string]string{metadataObjectsPath + "/" + metaObjID: body}, metaAnswer(`{}`))
			_, err := runMetaWrite(invokeViewsCreate, `{`+base+`}`)
			if err == nil || len(metaWrites(calls)) != 0 {
				t.Errorf("err = %v, writes = %+v", err, metaWrites(calls))
			}
		})
	}
}

func TestViewsUpdateReadsOnceAndPatchesNameAndIcon(t *testing.T) {
	path := viewsPath + "/" + viewID
	calls := serveMetaWrite(t, map[string]string{path: vwView(viewID, "WORKSPACE", "TABLE", "", true)},
		metaAnswer(vwView(viewID, "WORKSPACE", "TABLE", "", true)))
	out, err := runMetaWrite(invokeViewsUpdate, `{"id":"`+viewID+`","name":"Won deals","icon":"IconCheck"}`)
	if err != nil {
		t.Fatal(err)
	}
	sent := metaWrites(calls)
	if len(*calls) != 2 || (*calls)[0].Method != http.MethodGet || len(sent) != 1 || sent[0].Method != http.MethodPatch ||
		sent[0].Path != path || sent[0].Body != `{"icon":"IconCheck","name":"Won deals"}` {
		t.Fatalf("calls = %+v", *calls)
	}
	if !strings.Contains(out, viewID) || strings.Contains(out, creatorCn) || strings.Contains(out, appCn) {
		t.Errorf("result = %s", out)
	}
}

func TestViewsUpdateRefusesBeforeTheMutation(t *testing.T) {
	path := viewsPath + "/" + viewID
	for name, body := range map[string]string{
		"standard":  vwView(viewID, "WORKSPACE", "TABLE", "", false),
		"index":     vwView(viewID, "WORKSPACE", "TABLE", "INDEX", true),
		"unlisted":  vwView(viewID, "UNLISTED", "TABLE", "", true),
		"widget":    vwView(viewID, "WORKSPACE", "TABLE_WIDGET", "", true),
		"other id":  vwView(viewUnknown, "WORKSPACE", "TABLE", "", true),
		"no custom": strings.Replace(vwView(viewID, "WORKSPACE", "TABLE", "", true), `"isCustom":true,`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, map[string]string{path: body}, metaAnswer(`{}`))
			_, err := runMetaWrite(invokeViewsUpdate, `{"id":"`+viewID+`","name":"x"}`)
			if err == nil || len(metaWrites(calls)) != 0 {
				t.Errorf("err = %v, writes = %+v", err, metaWrites(calls))
			}
		})
	}
	t.Run("unlisted reads like unknown", func(t *testing.T) {
		serveMetaWrite(t, map[string]string{path: vwView(viewID, "UNLISTED", "TABLE", "", true)}, metaAnswer(`{}`))
		_, unlisted := runMetaWrite(invokeViewsUpdate, `{"id":"`+viewID+`","name":"x"}`)
		serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusNotFound, `{}`), nil })
		stubLimiter(t, cloudKey)
		_, unknown := runMetaWrite(invokeViewsUpdate, `{"id":"`+viewID+`","name":"x"}`)
		if unlisted == nil || unknown == nil || unlisted.Error() != unknown.Error() {
			t.Errorf("unlisted = %v, unknown = %v", unlisted, unknown)
		}
	})
}

func TestViewsUpdateRefusesBadArgumentsBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string]string{
		"nothing":       `{"id":"` + viewID + `"}`,
		"bad id":        `{"id":"x","name":"a"}`,
		"bad name":      `{"id":"` + viewID + `","name":"a;b"}`,
		"bad icon":      `{"id":"` + viewID + `","icon":""}`,
		"type":          `{"id":"` + viewID + `","name":"a","type":"KANBAN"}`,
		"visibility":    `{"id":"` + viewID + `","name":"a","visibility":"UNLISTED"}`,
		"layout object": `{"id":"` + viewID + `","name":"a","object_id":"` + metaObjID + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			if _, err := runMetaWrite(invokeViewsUpdate, args); !asInvalidOK(err) {
				t.Errorf("err = %v, want invalid-request", err)
			}
		})
	}
}

func TestViewsUpdateAnswerMustNameTheView(t *testing.T) {
	path := viewsPath + "/" + viewID
	other := strings.Replace(vwView(viewID, "WORKSPACE", "TABLE", "", true), metaObjID, metaOtherID, 1)
	for name, answer := range map[string]string{
		"other view":   vwView(viewUnknown, "WORKSPACE", "TABLE", "", true),
		"other object": other,
		"unlisted":     vwView(viewID, "UNLISTED", "TABLE", "", true),
	} {
		t.Run(name, func(t *testing.T) {
			serveMetaWrite(t, map[string]string{path: vwView(viewID, "WORKSPACE", "TABLE", "", true)}, metaAnswer(answer))
			_, err := runMetaWrite(invokeViewsUpdate, `{"id":"`+viewID+`","name":"x"}`)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func viewWriteInvocations() map[string]struct {
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
		"create": {invokeViewsCreate, `{"name":"A","object_id":"` + metaObjID + `"}`, vwReads(nil)},
		"update": {invokeViewsUpdate, `{"id":"` + viewID + `","name":"A"}`,
			map[string]string{viewsPath + "/" + viewID: vwView(viewID, "WORKSPACE", "TABLE", "", true)}},
	}
}

func TestViewWritesNeedAConnectionWithoutObjectTargets(t *testing.T) {
	for name, test := range viewWriteInvocations() {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			_, err := runMetaWrite(test.handler, test.args, "object/person")
			if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
				t.Errorf("err = %v, want invalid-request without the target", err)
			}
		})
	}
}

func TestViewWritesNeverRetryAndReportUncertainty(t *testing.T) {
	for name, test := range viewWriteInvocations() {
		for outcome, write := range map[string]func() (*http.Response, error){
			"server error": func() (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+vwProvider+`"}`), nil
			},
			"timeout": func() (*http.Response, error) { return nil, &net.DNSError{IsTimeout: true, Err: vwProvider} },
			"reset":   func() (*http.Response, error) { return nil, errors.New(vwProvider + ": connection reset by peer") },
			"not json": func() (*http.Response, error) {
				return jsonResponse(http.StatusOK, `<html>`+vwProvider), nil
			},
			"unusable": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"data":[1]}`), nil },
			"foreign": func() (*http.Response, error) {
				other := strings.Replace(vwView(viewID, "WORKSPACE", "TABLE", "", true), metaObjID, metaOtherID, 1)
				return jsonResponse(http.StatusOK, other), nil
			},
		} {
			t.Run(name+" "+outcome, func(t *testing.T) {
				calls := serveMetaWrite(t, test.reads, write)
				_, err := runMetaWrite(test.handler, test.args)
				if err == nil || len(metaWrites(calls)) != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
					strings.Contains(err.Error(), vwProvider) {
					t.Errorf("err = %v, writes = %d", err, len(metaWrites(calls)))
				}
				if name == "create" && err != nil && !strings.Contains(err.Error(), "second view") {
					t.Errorf("create does not warn about a duplicate: %v", err)
				}
			})
		}
	}
}

func TestViewWritesMapPermissionWithoutProviderText(t *testing.T) {
	for name, test := range viewWriteInvocations() {
		t.Run(name, func(t *testing.T) {
			serveMetaWrite(t, test.reads, func() (*http.Response, error) {
				return jsonResponse(http.StatusForbidden, `{"message":"`+vwProvider+`"}`), nil
			})
			_, err := runMetaWrite(test.handler, test.args)
			var failure *provider.Error
			if !errors.As(err, &failure) || failure.Class != provider.ClassPermission ||
				!strings.Contains(err.Error(), "Views") || strings.Contains(err.Error(), vwProvider) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestViewWriteDescriptorsAreGuarded(t *testing.T) {
	for _, d := range []capability.Descriptor{viewsCreate, viewsUpdate} {
		wantEffect, wantIdem := capability.EffectUpdate, capability.IdempotencyIdempotent
		if strings.HasSuffix(d.ID, ".create") {
			wantEffect, wantIdem = capability.EffectCreate, capability.IdempotencyNonIdempotent
		}
		want := capability.Risk{Effect: wantEffect, Idempotency: wantIdem, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: viewDataSensitivity}
		if d.Risk != want || !d.RequiresToolAllowList || d.Provider != Provider || d.Version != 1 {
			t.Errorf("%s = %+v", d.ID, d)
		}
		if !strings.Contains(d.Description, "visible to all members of the workspace") ||
			!strings.Contains(d.Description, "API key of its own") {
			t.Errorf("%s omits the visibility or the key advice", d.ID)
		}
	}
	for _, kind := range viewCreatableTypes {
		if strings.HasSuffix(kind, "_WIDGET") {
			t.Errorf("creatable type %s is a widget type", kind)
		}
	}
}
