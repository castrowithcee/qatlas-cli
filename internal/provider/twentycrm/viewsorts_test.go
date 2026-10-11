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
	sortFieldText   = "77777777-0000-4000-8000-0000000000e1"
	sortFieldName   = "77777777-0000-4000-8000-0000000000e2"
	sortFieldRel    = "77777777-0000-4000-8000-0000000000e3"
	sortFieldTaken  = "77777777-0000-4000-8000-0000000000e4"
	sortFieldSystem = "77777777-0000-4000-8000-0000000000e5"
	sortFieldOff    = "77777777-0000-4000-8000-0000000000e6"
	sortFieldOther  = "77777777-0000-4000-8000-0000000000e7"
	sortFieldDel    = "77777777-0000-4000-8000-0000000000e8"

	sortNewID     = "99999999-0000-4000-8000-000000000001"
	sortTakenID   = "99999999-0000-4000-8000-000000000002"
	sortOfUnlist  = "99999999-0000-4000-8000-000000000003"
	sortOfStd     = "99999999-0000-4000-8000-000000000004"
	sortDeletedID = "99999999-0000-4000-8000-000000000005"
)

func sortField(id, name, kind string, active, system bool) string {
	flags := map[bool]string{true: "true", false: "false"}
	return `{"id":"` + id + `","type":"` + kind + `","name":"` + name + `","label":"L","isActive":` + flags[active] +
		`,"isSystem":` + flags[system] + `}`
}

var sortObjectJSON = `{"id":"` + metaObjID + `","nameSingular":"deal","namePlural":"deals","labelSingular":"Deal",` +
	`"labelPlural":"Deals","isCustom":true,"isActive":true,"isSystem":false,"fields":[` +
	sortField(sortFieldText, "amount", "TEXT", true, false) + `,` + sortField(sortFieldName, "contact", "FULL_NAME", true, false) + `,` +
	sortField(sortFieldRel, "owner", "RELATION", true, false) + `,` + sortField(sortFieldTaken, "stage", "SELECT", true, false) + `,` +
	sortField(sortFieldSystem, "position", "POSITION", true, true) + `,` + sortField(sortFieldOff, "legacy", "TEXT", false, false) + `,` +
	sortField(sortFieldDel, "closed", "DATE", true, false) + `]}`

func sortViewJSON(id, visibility, kind string, custom bool, key string) string {
	keyJSON := `null`
	if key != "" {
		keyJSON = `"` + key + `"`
	}
	return `{"id":"` + id + `","name":"Pipeline","objectMetadataId":"` + metaObjID + `","type":"` + kind + `",` +
		`"key":` + keyJSON + `,"isCustom":` + map[bool]string{true: "true", false: "false"}[custom] + `,"isActive":true,` +
		`"visibility":"` + visibility + `","createdByUserWorkspaceId":"` + creatorCn + `","applicationId":"` + appCn + `",` +
		`"viewFields":[],"viewFilters":[],"viewFilterGroups":[],` +
		`"viewSorts":[{"id":"` + sortTakenID + `","fieldMetadataId":"` + sortFieldTaken + `","direction":"ASC"},` +
		`{"id":"` + sortDeletedID + `","fieldMetadataId":"` + sortFieldDel + `","direction":"ASC","deletedAt":"2026-01-01T00:00:00Z"}],` +
		`"viewGroups":[]}`
}

func sortWire(id, view, field, direction, extra string) string {
	return `{"id":"` + id + `","viewId":"` + view + `","fieldMetadataId":"` + field + `","direction":"` + direction + `",` +
		`"subFieldName":null,"workspaceId":"` + appCn + `","applicationId":"` + appCn + `"` + extra + `}`
}

// serveSorts answers the reads of the sort tools and answers every write with status and answer.
func serveSorts(t *testing.T, status int, answer string) *[]colCall {
	t.Helper()
	calls := &[]colCall{}
	views := map[string]string{
		colView:     sortViewJSON(colView, "WORKSPACE", "TABLE", true, ""),
		colUnlisted: sortViewJSON(colUnlisted, "UNLISTED", "TABLE", true, ""),
		colWidget:   sortViewJSON(colWidget, "WORKSPACE", "TABLE_WIDGET", true, ""),
		colStandard: sortViewJSON(colStandard, "WORKSPACE", "TABLE", false, ""),
		colIndex:    sortViewJSON(colIndex, "WORKSPACE", "TABLE", true, "INDEX"),
	}
	sorts := map[string]string{
		sortNewID:     sortWire(sortNewID, colView, sortFieldText, "ASC", ""),
		sortOfUnlist:  sortWire(sortOfUnlist, colUnlisted, sortFieldText, "ASC", ""),
		sortOfStd:     sortWire(sortOfStd, colStandard, sortFieldText, "ASC", ""),
		sortTakenID:   sortWire(sortTakenID, colView, sortFieldTaken, "ASC", ""),
		sortDeletedID: sortWire(sortDeletedID, colView, sortFieldDel, "ASC", `,"deletedAt":"2026-01-01T00:00:00Z"`),
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
			return jsonResponse(http.StatusOK, sortObjectJSON), nil
		case strings.HasPrefix(path, viewsPath+"/"):
			if body, ok := views[strings.TrimPrefix(path, viewsPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		case strings.HasPrefix(path, viewSortsPath+"/"):
			if body, ok := sorts[strings.TrimPrefix(path, viewSortsPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		}
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func sortCreateArgs(field string) string {
	return `{"view_id":"` + colView + `","field_id":"` + field + `"}`
}

func TestViewSortsCreateSendsOneCheckedBodyAndNamesTheSort(t *testing.T) {
	calls := serveSorts(t, http.StatusCreated, sortWire(sortNewID, colView, sortFieldText, "DESC", ""))
	result, err := runColumn(invokeViewSortsCreate,
		`{"view_id":"`+colView+`","field_id":"`+sortFieldText+`","direction":"DESC"}`)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	var sort ViewSortResult
	if json.Unmarshal(out, &sort) != nil || sort.ID != sortNewID || sort.ViewID != colView ||
		sort.FieldID != sortFieldText || sort.Field != "amount" || sort.Direction != "DESC" {
		t.Fatalf("sort = %s", out)
	}
	for _, canary := range []string{creatorCn, appCn, "applicationId", "workspaceId"} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	sent := columnWrites(calls)
	want := `{"viewId":"` + colView + `","fieldMetadataId":"` + sortFieldText + `","direction":"DESC"}`
	if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].URI != viewSortsPath || sent[0].Body != want {
		t.Errorf("writes = %+v, want body %s", sent, want)
	}

	calls = serveSorts(t, http.StatusCreated,
		strings.Replace(sortWire(sortNewID, colView, sortFieldName, "ASC", ""), `"subFieldName":null`, `"subFieldName":"lastName"`, 1))
	if _, err = runColumn(invokeViewSortsCreate,
		`{"view_id":"`+colView+`","field_id":"`+sortFieldName+`","sub_field_name":"lastName"}`); err != nil {
		t.Fatal(err)
	}
	want = `{"viewId":"` + colView + `","fieldMetadataId":"` + sortFieldName + `","subFieldName":"lastName"}`
	if sent = columnWrites(calls); len(sent) != 1 || sent[0].Body != want {
		t.Errorf("sub field body = %+v, want %s", sent, want)
	}
}

func TestViewSortsCreateRefusesWithoutAWritingRequest(t *testing.T) {
	view := func(id, field string) string { return `{"view_id":"` + id + `","field_id":"` + field + `"}` }
	for name, tt := range map[string]struct {
		args  string
		class provider.Class
		text  string
	}{
		"unknown view":        {view(colUnknown, sortFieldText), provider.ClassNotFound, ""},
		"unlisted view":       {view(colUnlisted, sortFieldText), provider.ClassNotFound, ""},
		"widget view":         {view(colWidget, sortFieldText), provider.ClassNotFound, ""},
		"standard view":       {view(colStandard, sortFieldText), "", "custom views"},
		"index view":          {view(colIndex, sortFieldText), "", "custom views"},
		"foreign field":       {view(colView, sortFieldOther), "", "can be sorted"},
		"system field":        {view(colView, sortFieldSystem), "", "can be sorted"},
		"inactive field":      {view(colView, sortFieldOff), "", "can be sorted"},
		"relation field":      {view(colView, sortFieldRel), "", "can be sorted"},
		"full name no sub":    {view(colView, sortFieldName), "", "sub_field_name"},
		"sub on text":         {`{"view_id":"` + colView + `","field_id":"` + sortFieldText + `","sub_field_name":"firstName"}`, "", "sub_field_name"},
		"unknown sub":         {`{"view_id":"` + colView + `","field_id":"` + sortFieldName + `","sub_field_name":"middle"}`, "", "sub_field_name"},
		"duplicate":           {view(colView, sortFieldTaken), "", "already sorted by the view (sort " + sortTakenID},
		"invalid direction":   {`{"view_id":"` + colView + `","field_id":"` + sortFieldText + `","direction":"UP"}`, "", "ASC or DESC"},
		"lowercase direction": {`{"view_id":"` + colView + `","field_id":"` + sortFieldText + `","direction":"asc"}`, "", "ASC or DESC"},
		"not a view uuid":     {view("nope", sortFieldText), "", "UUIDs"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveSorts(t, http.StatusCreated, sortWire(sortNewID, colView, sortFieldText, "ASC", ""))
			_, err := runColumn(invokeViewSortsCreate, tt.args)
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

func TestViewSortsHiddenViewsAnswerLikeUnknownOnes(t *testing.T) {
	serveSorts(t, http.StatusOK, `{}`)
	_, unknown := runColumn(invokeViewSortsCreate, sortCreateArgsFor(colUnknown))
	for _, view := range []string{colUnlisted, colWidget} {
		_, err := runColumn(invokeViewSortsCreate, sortCreateArgsFor(view))
		if err == nil || unknown == nil || err.Error() != unknown.Error() {
			t.Errorf("view %s: err = %v, want %v", view, err, unknown)
		}
	}
}

func sortCreateArgsFor(view string) string {
	return `{"view_id":"` + view + `","field_id":"` + sortFieldText + `"}`
}

func TestViewSortsUpdateReadsTheSortThenSendsOneChange(t *testing.T) {
	calls := serveSorts(t, http.StatusOK, sortWire(sortNewID, colView, sortFieldText, "DESC", ""))
	result, err := runColumn(invokeViewSortsUpdate, `{"id":"`+sortNewID+`","direction":"DESC"}`)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	var sort ViewSortResult
	if json.Unmarshal(out, &sort) != nil || sort.ID != sortNewID || sort.ViewID != colView ||
		sort.FieldID != sortFieldText || sort.Direction != "DESC" {
		t.Fatalf("sort = %s", out)
	}
	sent := columnWrites(calls)
	if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].URI != viewSortsPath+"/"+sortNewID ||
		sent[0].Body != `{"direction":"DESC"}` {
		t.Errorf("writes = %+v", sent)
	}
	if (*calls)[0].URI != viewSortsPath+"/"+sortNewID || (*calls)[1].URI != viewsPath+"/"+colView {
		t.Errorf("the sort and its view are read first: %+v", *calls)
	}
}

func TestViewSortsUpdateIsBoundToTheViewOfTheSort(t *testing.T) {
	for name, tt := range map[string]struct {
		id    string
		class provider.Class
	}{
		"sort of an unlisted view": {sortOfUnlist, provider.ClassNotFound},
		"sort of a standard view":  {sortOfStd, ""},
		"unknown sort":             {colUnknown, provider.ClassNotFound},
		"deleted sort":             {sortDeletedID, provider.ClassNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveSorts(t, http.StatusOK, sortWire(tt.id, colView, sortFieldText, "DESC", ""))
			_, err := runColumn(invokeViewSortsUpdate, `{"id":"`+tt.id+`","direction":"DESC"}`)
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

func TestViewSortsUpdateRefusesInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for _, args := range []string{
		`{"id":"` + sortNewID + `"}`,
		`{"id":"nope","direction":"ASC"}`,
		`{"id":"` + sortNewID + `","direction":"UP"}`,
	} {
		if _, err := runColumn(invokeViewSortsUpdate, args); !asInvalidOK(err) {
			t.Errorf("%s: err = %v", args, err)
		}
	}
}

func TestViewSortToolsNeedAConnectionWithoutObjectTargets(t *testing.T) {
	refuse(t)
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create": {invokeViewSortsCreate, sortCreateArgs(sortFieldText)},
		"update": {invokeViewSortsUpdate, `{"id":"` + sortNewID + `","direction":"ASC"}`},
	} {
		_, err := call.handler(context.Background(), targetConnection("object/person"), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(call.args))
		if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestViewSortsNeverRepeatAfterAnUnclearResult(t *testing.T) {
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
			"create": {invokeViewSortsCreate, sortCreateArgs(sortFieldText)},
			"update": {invokeViewSortsUpdate, `{"id":"` + sortNewID + `","direction":"DESC"}`},
		} {
			t.Run(name+" "+action, func(t *testing.T) {
				written := 0
				serveSorts(t, http.StatusOK, `{}`)
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

func TestViewSortsAnswerMustNameTheRequestedSort(t *testing.T) {
	updateArgs := `{"id":"` + sortNewID + `","direction":"DESC"}`
	for name, tt := range map[string]struct {
		handler capability.Handler
		args    string
		answer  string
	}{
		"create other view":  {invokeViewSortsCreate, sortCreateArgs(sortFieldText), sortWire(sortNewID, colUnlisted, sortFieldText, "ASC", "")},
		"create other field": {invokeViewSortsCreate, sortCreateArgs(sortFieldText), sortWire(sortNewID, colView, sortFieldTaken, "ASC", "")},
		"create no id":       {invokeViewSortsCreate, sortCreateArgs(sortFieldText), `{"viewId":"` + colView + `"}`},
		"create bad dir":     {invokeViewSortsCreate, sortCreateArgs(sortFieldText), sortWire(sortNewID, colView, sortFieldText, "SIDEWAYS", "")},
		"update other id":    {invokeViewSortsUpdate, updateArgs, sortWire(sortTakenID, colView, sortFieldText, "DESC", "")},
		"update other view":  {invokeViewSortsUpdate, updateArgs, sortWire(sortNewID, colUnlisted, sortFieldText, "DESC", "")},
		"update other dir":   {invokeViewSortsUpdate, updateArgs, sortWire(sortNewID, colView, sortFieldText, "ASC", "")},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveSorts(t, http.StatusOK, tt.answer)
			_, err := runColumn(tt.handler, tt.args)
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") ||
				len(columnWrites(calls)) != 1 {
				t.Errorf("err = %v, writes = %d", err, len(columnWrites(calls)))
			}
		})
	}
}

func TestViewSortsPermissionNamesTheViewsRight(t *testing.T) {
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create": {invokeViewSortsCreate, sortCreateArgs(sortFieldText)},
		"update": {invokeViewSortsUpdate, `{"id":"` + sortNewID + `","direction":"DESC"}`},
	} {
		serveSorts(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
		_, err := runColumn(call.handler, call.args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "called Views") ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestViewSortDescriptorsDeclareTheirRisk(t *testing.T) {
	for _, tt := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		idem   capability.Idempotency
	}{
		{viewsortsCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{viewsortsUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
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
			if id == viewsortsCreate.ID || id == viewsortsUpdate.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestViewSortToolsNeedConfirmationAndTheToolsList(t *testing.T) {
	calls := serveSorts(t, http.StatusCreated, sortWire(sortNewID, colView, sortFieldText, "ASC", ""))
	cfg := coreConfig()
	all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
	bare := cfg.Connections["crm"]
	bare.Permissions = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Permissions: all, Tools: []string{viewsortsCreate.ID, viewsortsUpdate.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	arguments := map[string]string{
		viewsortsCreate.ID: sortCreateArgs(sortFieldText),
		viewsortsUpdate.ID: `{"id":"` + sortNewID + `","direction":"DESC"}`,
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
