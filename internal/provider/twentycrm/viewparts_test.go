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

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	colView     = "66666666-0000-4000-8000-0000000000a1"
	colUnlisted = "66666666-0000-4000-8000-0000000000a2"
	colWidget   = "66666666-0000-4000-8000-0000000000a3"
	colStandard = "66666666-0000-4000-8000-0000000000a4"
	colIndex    = "66666666-0000-4000-8000-0000000000a5"
	colUnknown  = "66666666-0000-4000-8000-0000000000a6"

	colOK        = "77777777-0000-4000-8000-0000000000f1"
	colSystem    = "77777777-0000-4000-8000-0000000000f2"
	colInactive  = "77777777-0000-4000-8000-0000000000f3"
	colTaken     = "77777777-0000-4000-8000-0000000000f4"
	colForeign   = "77777777-0000-4000-8000-0000000000f5"
	colDeleted   = "77777777-0000-4000-8000-0000000000f6"
	colTakenID   = "88888888-0000-4000-8000-000000000001"
	colNewID     = "88888888-0000-4000-8000-000000000002"
	colOfUnlist  = "88888888-0000-4000-8000-000000000003"
	colOfStd     = "88888888-0000-4000-8000-000000000004"
	colOfMissing = "88888888-0000-4000-8000-000000000005"
	colOfView    = "88888888-0000-4000-8000-000000000006"
)

func colViewJSON(id, visibility, kind string, custom bool, key string) string {
	keyJSON := `null`
	if key != "" {
		keyJSON = `"` + key + `"`
	}
	return `{"id":"` + id + `","name":"Pipeline","objectMetadataId":"` + metaObjID + `","type":"` + kind + `",` +
		`"key":` + keyJSON + `,"isCustom":` + map[bool]string{true: "true", false: "false"}[custom] + `,"isActive":true,` +
		`"visibility":"` + visibility + `","createdByUserWorkspaceId":"` + creatorCn + `","applicationId":"` + appCn + `",` +
		`"viewFields":[{"id":"` + colTakenID + `","fieldMetadataId":"` + colTaken + `","isVisible":true,"size":100,"position":0},` +
		`{"id":"` + colOfMissing + `","fieldMetadataId":"` + colDeleted + `","deletedAt":"2026-01-01T00:00:00Z"}],` +
		`"viewFilters":[],"viewFilterGroups":[],"viewSorts":[],"viewGroups":[]}`
}

func colField(id, name string, active, system bool) string {
	flags := map[bool]string{true: "true", false: "false"}
	return `{"id":"` + id + `","type":"TEXT","name":"` + name + `","label":"L","isActive":` + flags[active] +
		`,"isSystem":` + flags[system] + `}`
}

var colObjectJSON = `{"id":"` + metaObjID + `","nameSingular":"deal","namePlural":"deals","labelSingular":"Deal",` +
	`"labelPlural":"Deals","isCustom":true,"isActive":true,"isSystem":false,"fields":[` +
	colField(colOK, "amount", true, false) + `,` + colField(colSystem, "position", true, true) + `,` +
	colField(colInactive, "legacy", false, false) + `,` + colField(colTaken, "stage", true, false) + `,` +
	colField(colDeleted, "closed", true, false) + `]}`

func colWire(id, view, field string, extra string) string {
	return `{"id":"` + id + `","viewId":"` + view + `","fieldMetadataId":"` + field + `","isVisible":true,"size":120,` +
		`"position":2,"workspaceId":"` + appCn + `","applicationId":"` + appCn + `"` + extra + `}`
}

type colCall struct{ Method, URI, Body string }

// serveColumns answers the reads of the column tools and answers every write with status and answer.
func serveColumns(t *testing.T, status int, answer string) *[]colCall {
	t.Helper()
	calls := &[]colCall{}
	views := map[string]string{
		colView:     colViewJSON(colView, "WORKSPACE", "TABLE", true, ""),
		colUnlisted: colViewJSON(colUnlisted, "UNLISTED", "TABLE", true, ""),
		colWidget:   colViewJSON(colWidget, "WORKSPACE", "TABLE_WIDGET", true, ""),
		colStandard: colViewJSON(colStandard, "WORKSPACE", "TABLE", false, ""),
		colIndex:    colViewJSON(colIndex, "WORKSPACE", "TABLE", true, "INDEX"),
	}
	columns := map[string]string{
		colNewID:     colWire(colNewID, colView, colOK, ""),
		colOfUnlist:  colWire(colOfUnlist, colUnlisted, colOK, ""),
		colOfStd:     colWire(colOfStd, colStandard, colOK, ""),
		colOfView:    colWire(colOfView, colView, colTaken, ""),
		colOfMissing: colWire(colOfMissing, colView, colDeleted, `,"deletedAt":"2026-01-01T00:00:00Z"`),
	}
	serve(t, func(request *http.Request) (*http.Response, error) {
		call := colCall{Method: request.Method, URI: request.URL.RequestURI()}
		if request.Body != nil {
			data, _ := io.ReadAll(request.Body)
			call.Body = string(data)
		}
		*calls = append(*calls, call)
		if request.Method != http.MethodGet {
			return jsonResponse(status, answer), nil
		}
		path := request.URL.Path
		switch {
		case path == metadataObjectsPath+"/"+metaObjID:
			return jsonResponse(http.StatusOK, colObjectJSON), nil
		case strings.HasPrefix(path, viewsPath+"/"):
			if body, ok := views[strings.TrimPrefix(path, viewsPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		case strings.HasPrefix(path, viewFieldsPath+"/"):
			if body, ok := columns[strings.TrimPrefix(path, viewFieldsPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		}
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func columnWrites(calls *[]colCall) []colCall {
	var written []colCall
	for _, call := range *calls {
		if call.Method != http.MethodGet {
			written = append(written, call)
		}
	}
	return written
}

func runColumn(handler capability.Handler, args string, targets ...string) (any, error) {
	red := &redact.Redactor{}
	return handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
}

func createArgs(field string) string {
	return `{"view_id":"` + colView + `","field_id":"` + field + `"}`
}

func TestViewColumnsCreateSendsOneCheckedBodyAndNamesTheColumn(t *testing.T) {
	calls := serveColumns(t, http.StatusCreated, colWire(colNewID, colView, colOK, ""))
	result, err := runColumn(invokeViewFieldsCreate,
		`{"view_id":"`+colView+`","field_id":"`+colOK+`","is_visible":false,"size":150,"position":3}`)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	var column ViewColumn
	if json.Unmarshal(out, &column) != nil || column.ID != colNewID || column.ViewID != colView ||
		column.FieldID != colOK || column.Field != "amount" {
		t.Fatalf("column = %s", out)
	}
	for _, canary := range []string{creatorCn, appCn, "applicationId", "workspaceId"} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	sent := columnWrites(calls)
	want := `{"viewId":"` + colView + `","fieldMetadataId":"` + colOK + `","isVisible":false,"size":150,"position":3}`
	if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].URI != viewFieldsPath || sent[0].Body != want {
		t.Errorf("writes = %+v, want body %s", sent, want)
	}

	calls = serveColumns(t, http.StatusCreated, colWire(colNewID, colView, colOK, ""))
	if _, err = runColumn(invokeViewFieldsCreate, createArgs(colOK)); err != nil {
		t.Fatal(err)
	}
	if sent = columnWrites(calls); len(sent) != 1 ||
		sent[0].Body != `{"viewId":"`+colView+`","fieldMetadataId":"`+colOK+`"}` {
		t.Errorf("minimal body = %+v", sent)
	}
}

func TestViewColumnsCreateRefusesWithoutAWritingRequest(t *testing.T) {
	for name, tt := range map[string]struct {
		args  string
		class provider.Class
		text  string
	}{
		"unknown view":    {`{"view_id":"` + colUnknown + `","field_id":"` + colOK + `"}`, provider.ClassNotFound, ""},
		"unlisted view":   {`{"view_id":"` + colUnlisted + `","field_id":"` + colOK + `"}`, provider.ClassNotFound, ""},
		"widget view":     {`{"view_id":"` + colWidget + `","field_id":"` + colOK + `"}`, provider.ClassNotFound, ""},
		"standard view":   {`{"view_id":"` + colStandard + `","field_id":"` + colOK + `"}`, "", "custom views"},
		"index view":      {`{"view_id":"` + colIndex + `","field_id":"` + colOK + `"}`, "", "custom views"},
		"foreign field":   {createArgs(colForeign), "", "not an active, non-system field"},
		"system field":    {createArgs(colSystem), "", "not an active, non-system field"},
		"inactive field":  {createArgs(colInactive), "", "not an active, non-system field"},
		"duplicate":       {createArgs(colTaken), "", "already a column of the view (column " + colTakenID},
		"size too large":  {`{"view_id":"` + colView + `","field_id":"` + colOK + `","size":5000}`, "", "size must"},
		"negative place":  {`{"view_id":"` + colView + `","field_id":"` + colOK + `","position":-1}`, "", "position a number"},
		"not a view uuid": {`{"view_id":"nope","field_id":"` + colOK + `"}`, "", "UUIDs"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveColumns(t, http.StatusCreated, colWire(colNewID, colView, colOK, ""))
			_, err := runColumn(invokeViewFieldsCreate, tt.args)
			if err == nil || len(columnWrites(calls)) != 0 {
				t.Fatalf("err = %v, writes = %+v", err, columnWrites(calls))
			}
			if tt.class != "" && classOf(err) != tt.class {
				t.Errorf("class = %q, err = %v", classOf(err), err)
			}
			if tt.class == "" && (!asInvalidOK(err) || !strings.Contains(err.Error(), tt.text)) {
				t.Errorf("err = %v, want invalid-request with %q", err, tt.text)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("provider text in the error: %v", err)
			}
		})
	}
}

func TestViewColumnsHiddenViewsAnswerLikeUnknownOnes(t *testing.T) {
	serveColumns(t, http.StatusOK, `{}`)
	_, unknown := runColumn(invokeViewFieldsCreate, `{"view_id":"`+colUnknown+`","field_id":"`+colOK+`"}`)
	for _, view := range []string{colUnlisted, colWidget} {
		_, err := runColumn(invokeViewFieldsCreate, `{"view_id":"`+view+`","field_id":"`+colOK+`"}`)
		if err == nil || unknown == nil || err.Error() != unknown.Error() {
			t.Errorf("view %s: err = %v, want %v", view, err, unknown)
		}
	}
}

func TestViewColumnsUpdateReadsTheColumnThenSendsOneChange(t *testing.T) {
	calls := serveColumns(t, http.StatusOK, colWire(colNewID, colView, colOK, ""))
	result, err := runColumn(invokeViewFieldsUpdate, `{"id":"`+colNewID+`","is_visible":false,"size":90,"position":1.5}`)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	var column ViewColumn
	if json.Unmarshal(out, &column) != nil || column.ID != colNewID || column.ViewID != colView || column.FieldID != colOK {
		t.Fatalf("column = %s", out)
	}
	sent := columnWrites(calls)
	if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].URI != viewFieldsPath+"/"+colNewID ||
		sent[0].Body != `{"isVisible":false,"size":90,"position":1.5}` {
		t.Errorf("writes = %+v", sent)
	}
	if (*calls)[0].URI != viewFieldsPath+"/"+colNewID || (*calls)[1].URI != viewsPath+"/"+colView {
		t.Errorf("the column and its view are read first: %+v", *calls)
	}
}

func TestViewColumnsUpdateIsBoundToTheViewOfTheColumn(t *testing.T) {
	for name, tt := range map[string]struct {
		id    string
		class provider.Class
	}{
		"column of an unlisted view": {colOfUnlist, provider.ClassNotFound},
		"column of a standard view":  {colOfStd, ""},
		"unknown column":             {colUnknown, provider.ClassNotFound},
		"deleted column":             {colOfMissing, provider.ClassNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveColumns(t, http.StatusOK, colWire(tt.id, colView, colOK, ""))
			_, err := runColumn(invokeViewFieldsUpdate, `{"id":"`+tt.id+`","is_visible":true}`)
			if err == nil || len(columnWrites(calls)) != 0 || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, writes = %+v", err, columnWrites(calls))
			}
			if tt.class != "" && classOf(err) != tt.class {
				t.Errorf("class = %q, err = %v", classOf(err), err)
			}
			if tt.class == "" && !asInvalidOK(err) {
				t.Errorf("err = %v, want invalid-request", err)
			}
		})
	}
}

func TestViewColumnsUpdateRefusesInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for _, args := range []string{
		`{"id":"` + colNewID + `"}`,
		`{"id":"nope","is_visible":true}`,
		`{"id":"` + colNewID + `","size":0}`,
		`{"id":"` + colNewID + `","size":2001}`,
		`{"id":"` + colNewID + `","position":1000001}`,
	} {
		_, err := runColumn(invokeViewFieldsUpdate, args)
		if !asInvalidOK(err) {
			t.Errorf("%s: err = %v", args, err)
		}
	}
}

func TestViewColumnToolsNeedAConnectionWithoutObjectTargets(t *testing.T) {
	refuse(t)
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create": {invokeViewFieldsCreate, createArgs(colOK)},
		"update": {invokeViewFieldsUpdate, `{"id":"` + colNewID + `","is_visible":true}`},
	} {
		_, err := call.handler(context.Background(), targetConnection("object/person"), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(call.args))
		if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestViewColumnsNeverRepeatAfterAnUnclearResult(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		fail   error
		body   string
	}{
		"server error": {status: 500, body: `{"error":"` + bodyCanary + `"}`},
		"gateway":      {status: 504},
		"unreadable":   {status: 200, body: `not json ` + bodyCanary},
		"timeout":      {fail: &net.DNSError{IsTimeout: true, Err: bodyCanary}},
		"connection":   {fail: errors.New(bodyCanary)},
	} {
		for action, call := range map[string]struct {
			handler capability.Handler
			args    string
		}{
			"create": {invokeViewFieldsCreate, createArgs(colOK)},
			"update": {invokeViewFieldsUpdate, `{"id":"` + colNewID + `","is_visible":false}`},
		} {
			t.Run(name+" "+action, func(t *testing.T) {
				written := 0
				serveColumns(t, http.StatusOK, `{}`)
				inner := transport
				serve(t, func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet {
						return inner.RoundTrip(request)
					}
					written++
					if tt.fail != nil {
						return nil, tt.fail
					}
					return jsonResponse(tt.status, tt.body), nil
				})
				_, err := runColumn(call.handler, call.args)
				if err == nil || written != 1 {
					t.Fatalf("err = %v, writes = %d, want one", err, written)
				}
				if strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("provider text in the error: %v", err)
				}
				var failure *provider.Error
				mayHave := errors.As(err, &failure) && (failure.Class != provider.ClassUnreachable || failure.MayHaveArrived())
				if mayHave && !strings.Contains(err.Error(), "before repeating") {
					t.Errorf("no uncertainty named: %v", err)
				}
			})
		}
	}
}

func TestViewColumnsAnswerMustNameTheRequestedColumn(t *testing.T) {
	for name, tt := range map[string]struct {
		handler capability.Handler
		args    string
		answer  string
	}{
		"create other view":  {invokeViewFieldsCreate, createArgs(colOK), colWire(colNewID, colUnlisted, colOK, "")},
		"create other field": {invokeViewFieldsCreate, createArgs(colOK), colWire(colNewID, colView, colTaken, "")},
		"create no id":       {invokeViewFieldsCreate, createArgs(colOK), `{"viewId":"` + colView + `"}`},
		"update other id":    {invokeViewFieldsUpdate, `{"id":"` + colNewID + `","size":10}`, colWire(colOfView, colView, colOK, "")},
		"update other view":  {invokeViewFieldsUpdate, `{"id":"` + colNewID + `","size":10}`, colWire(colNewID, colUnlisted, colOK, "")},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveColumns(t, http.StatusOK, tt.answer)
			_, err := runColumn(tt.handler, tt.args)
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") ||
				len(columnWrites(calls)) != 1 {
				t.Errorf("err = %v, writes = %d", err, len(columnWrites(calls)))
			}
		})
	}
}

func TestViewColumnsPermissionNamesTheViewsRight(t *testing.T) {
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create": {invokeViewFieldsCreate, createArgs(colOK)},
		"update": {invokeViewFieldsUpdate, `{"id":"` + colNewID + `","is_visible":false}`},
	} {
		serveColumns(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
		_, err := runColumn(call.handler, call.args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "called Views") ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestViewColumnDescriptorsDeclareTheirRisk(t *testing.T) {
	for _, tt := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		idem   capability.Idempotency
	}{
		{viewfieldsCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{viewfieldsUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idem, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: viewDataSensitivity}
		if tt.d.Risk != want || !tt.d.RequiresToolAllowList || tt.d.Provider != Provider {
			t.Errorf("%s = %+v", tt.d.ID, tt.d)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == viewfieldsCreate.ID || id == viewfieldsUpdate.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestViewColumnToolsNeedConfirmationAndTheToolsList(t *testing.T) {
	calls := serveColumns(t, http.StatusCreated, colWire(colNewID, colView, colOK, ""))
	cfg := coreConfig()
	all := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}
	bare := cfg.Connections["crm"]
	bare.Effects = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Effects: all, Tools: []string{viewfieldsCreate.ID, viewfieldsUpdate.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	arguments := map[string]string{
		viewfieldsCreate.ID: createArgs(colOK),
		viewfieldsUpdate.ID: `{"id":"` + colNewID + `","is_visible":false}`,
	}
	for operation, args := range arguments {
		request := application.InvokeRequest{Operation: operation, Connection: "crm",
			Arguments: json.RawMessage(args), Confirmed: true}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s was offered without a tools list", operation)
		}
		request = application.InvokeRequest{Operation: operation, Connection: "crm-internal", Arguments: json.RawMessage(args)}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirmation", operation)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests before approval: %+v", *calls)
	}
}
