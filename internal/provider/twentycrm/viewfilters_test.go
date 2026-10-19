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
	flText   = "55555555-0000-4000-8000-0000000000e1"
	flSelect = "55555555-0000-4000-8000-0000000000e2"
	flMulti  = "55555555-0000-4000-8000-0000000000e3"
	flNumber = "55555555-0000-4000-8000-0000000000e4"
	flDate   = "55555555-0000-4000-8000-0000000000e5"
	flTime   = "55555555-0000-4000-8000-0000000000e6"
	flBool   = "55555555-0000-4000-8000-0000000000e7"
	flRel    = "55555555-0000-4000-8000-0000000000e8"
	flOff    = "55555555-0000-4000-8000-0000000000e9"
	flSystem = "55555555-0000-4000-8000-0000000000ea"
	flOther  = "55555555-0000-4000-8000-0000000000eb"
	flPos    = "55555555-0000-4000-8000-0000000000ec"

	flNew       = "44444444-0000-4000-8000-000000000001"
	flOfText    = "44444444-0000-4000-8000-000000000002"
	flOfUnlist  = "44444444-0000-4000-8000-000000000003"
	flOfStd     = "44444444-0000-4000-8000-000000000004"
	flOfSub     = "44444444-0000-4000-8000-000000000005"
	flOfRel     = "44444444-0000-4000-8000-000000000006"
	flOfDeleted = "44444444-0000-4000-8000-000000000007"
	flOfVector  = "44444444-0000-4000-8000-000000000008"
	flOfBool    = "44444444-0000-4000-8000-000000000009"
	flOfEmpty   = "44444444-0000-4000-8000-00000000000a"
	flOfSelect  = "44444444-0000-4000-8000-00000000000b"
	flOfGroup   = "44444444-0000-4000-8000-00000000000c"
)

func filterField(id, name, kind, options string, active, system bool) string {
	flags := map[bool]string{true: "true", false: "false"}
	if options == "" {
		options = "null"
	}
	return `{"id":"` + id + `","type":"` + kind + `","name":"` + name + `","label":"L","isActive":` + flags[active] +
		`,"isSystem":` + flags[system] + `,"options":` + options + `}`
}

var filterObjectJSON = `{"id":"` + metaObjID + `","nameSingular":"deal","namePlural":"deals","labelSingular":"Deal",` +
	`"labelPlural":"Deals","isCustom":true,"isActive":true,"isSystem":false,"fields":[` +
	filterField(flText, "title", "TEXT", "", true, false) + `,` +
	filterField(flSelect, "stage", "SELECT", `[{"value":"A","label":"a"},{"value":"B","label":"b"}]`, true, false) + `,` +
	filterField(flMulti, "tags", "MULTI_SELECT", `[{"value":"X","label":"x"},{"value":"Y","label":"y"}]`, true, false) + `,` +
	filterField(flNumber, "amount", "NUMBER", "", true, false) + `,` +
	filterField(flDate, "due", "DATE", "", true, false) + `,` +
	filterField(flTime, "closedAt", "DATE_TIME", "", true, false) + `,` +
	filterField(flBool, "won", "BOOLEAN", "", true, false) + `,` +
	filterField(flRel, "owner", "RELATION", "", true, false) + `,` +
	filterField(flOff, "legacy", "TEXT", "", false, false) + `,` +
	filterField(flSystem, "searchVector", "TEXT", "", true, true) + `,` +
	filterField(flPos, "position", "POSITION", "", true, false) + `]}`

func filterViewJSON(id, visibility, kind string, custom bool, key string) string {
	return strings.Replace(sortViewJSON(id, visibility, kind, custom, key), `"viewSorts":[`, `"viewSortsX":[`, 1)
}

func filterWire(id, view, field, operand, extra string) string {
	return `{"id":"` + id + `","viewId":"` + view + `","fieldMetadataId":"` + field + `","operand":"` + operand + `",` +
		`"value":"` + bodyCanary + `","subFieldName":null,"relationTargetFieldMetadataId":null,` +
		`"viewFilterGroupId":null,"workspaceId":"` + appCn + `","applicationId":"` + appCn + `"` + extra + `}`
}

// serveFilters answers the reads of the filter tools and answers every write with status and answer.
func serveFilters(t *testing.T, status int, answer string) *[]colCall {
	t.Helper()
	calls := &[]colCall{}
	views := map[string]string{
		colView:     filterViewJSON(colView, "WORKSPACE", "TABLE", true, ""),
		colUnlisted: filterViewJSON(colUnlisted, "UNLISTED", "TABLE", true, ""),
		colWidget:   filterViewJSON(colWidget, "WORKSPACE", "TABLE_WIDGET", true, ""),
		colStandard: filterViewJSON(colStandard, "WORKSPACE", "TABLE", false, ""),
		colIndex:    filterViewJSON(colIndex, "WORKSPACE", "TABLE", true, "INDEX"),
	}
	filters := map[string]string{
		flOfText:    filterWire(flOfText, colView, flText, "CONTAINS", ""),
		flOfUnlist:  filterWire(flOfUnlist, colUnlisted, flText, "CONTAINS", ""),
		flOfStd:     filterWire(flOfStd, colStandard, flText, "CONTAINS", ""),
		flOfSub:     filterWire(flOfSub, colView, flText, "CONTAINS", `,"subFieldName":"firstName"`),
		flOfRel:     filterWire(flOfRel, colView, flText, "CONTAINS", `,"relationTargetFieldMetadataId":"`+flText+`"`),
		flOfDeleted: filterWire(flOfDeleted, colView, flText, "CONTAINS", `,"deletedAt":"2026-01-01T00:00:00Z"`),
		flOfVector:  filterWire(flOfVector, colView, flText, "VECTOR_SEARCH", ""),
		flOfBool:    filterWire(flOfBool, colView, flBool, "IS", ""),
		flOfEmpty:   filterWire(flOfEmpty, colView, flText, "IS_EMPTY", ""),
		flOfSelect:  filterWire(flOfSelect, colView, flSelect, "IS", ""),
		flOfGroup:   filterWire(flOfGroup, colView, flText, "CONTAINS", `,"viewFilterGroupId":"`+colTakenID+`"`),
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
			return jsonResponse(http.StatusOK, filterObjectJSON), nil
		case strings.HasPrefix(path, viewsPath+"/"):
			if body, ok := views[strings.TrimPrefix(path, viewsPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		case strings.HasPrefix(path, viewFiltersPath+"/"):
			if body, ok := filters[strings.TrimPrefix(path, viewFiltersPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		}
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func filterCreateArgs(field, rest string) string {
	if rest != "" {
		rest = "," + rest
	}
	return `{"view_id":"` + colView + `","field_id":"` + field + `"` + rest + `}`
}

func TestViewFiltersCreateSendsOneCheckedBodyPerFieldType(t *testing.T) {
	for name, tt := range map[string]struct {
		field, rest, operand, value string
	}{
		"text":          {flText, `"operand":"CONTAINS","text":"Berlin"`, "CONTAINS", "Berlin"},
		"select":        {flSelect, `"operand":"IS","options":["A","B"]`, "IS", `["A","B"]`},
		"multi select":  {flMulti, `"operand":"DOES_NOT_CONTAIN","options":["Y"]`, "DOES_NOT_CONTAIN", `["Y"]`},
		"number":        {flNumber, `"operand":"GREATER_THAN_OR_EQUAL","number":10.5`, "GREATER_THAN_OR_EQUAL", "10.5"},
		"date":          {flDate, `"operand":"IS_BEFORE","date":"2026-05-01"`, "IS_BEFORE", "2026-05-01"},
		"relative":      {flDate, `"operand":"IS_RELATIVE","relative":{"direction":"PAST","amount":7,"unit":"DAY"}`, "IS_RELATIVE", "PAST_7_DAY"},
		"relative this": {flDate, `"operand":"IS_RELATIVE","relative":{"direction":"THIS","unit":"MONTH"}`, "IS_RELATIVE", "THIS_1_MONTH"},
		"today":         {flDate, `"operand":"IS_TODAY"`, "IS_TODAY", ""},
		"date time":     {flTime, `"operand":"IS_AFTER","date":"2026-05-01T08:00:00Z"`, "IS_AFTER", "2026-05-01T08:00:00Z"},
		"boolean":       {flBool, `"operand":"IS","flag":false`, "IS", "false"},
		"empty":         {flRel + "x", "", "", ""},
	} {
		if name == "empty" {
			tt.field, tt.rest, tt.operand = flText, `"operand":"IS_EMPTY"`, "IS_EMPTY"
		}
		t.Run(name, func(t *testing.T) {
			calls := serveFilters(t, http.StatusCreated, filterWire(flNew, colView, tt.field, tt.operand, ""))
			result, err := runColumn(invokeViewFiltersCreate, filterCreateArgs(tt.field, tt.rest))
			if err != nil {
				t.Fatal(err)
			}
			out, _ := json.Marshal(result)
			var filter ViewFilterResult
			if json.Unmarshal(out, &filter) != nil || filter.ID != flNew || filter.ViewID != colView ||
				filter.FieldID != tt.field || filter.Operand != tt.operand || filter.Value != tt.value || filter.Field == "" {
				t.Fatalf("filter = %s", out)
			}
			for _, canary := range []string{creatorCn, appCn, "applicationId", "workspaceId", bodyCanary} {
				if strings.Contains(string(out), canary) {
					t.Errorf("%s reached the output: %s", canary, out)
				}
			}
			encoded, _ := json.Marshal(tt.value)
			want := `{"viewId":"` + colView + `","fieldMetadataId":"` + tt.field + `","operand":"` + tt.operand +
				`","value":` + string(encoded) + `}`
			sent := columnWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].URI != viewFiltersPath || sent[0].Body != want {
				t.Errorf("writes = %+v, want body %s", sent, want)
			}
		})
	}
}

func TestViewFiltersCreateRefusesWithoutAWritingRequest(t *testing.T) {
	text := `"operand":"CONTAINS","text":"x"`
	for name, tt := range map[string]struct {
		args  string
		class provider.Class
		text  string
	}{
		"unknown view":       {`{"view_id":"` + colUnknown + `","field_id":"` + flText + `",` + text + `}`, provider.ClassNotFound, ""},
		"unlisted view":      {`{"view_id":"` + colUnlisted + `","field_id":"` + flText + `",` + text + `}`, provider.ClassNotFound, ""},
		"widget view":        {`{"view_id":"` + colWidget + `","field_id":"` + flText + `",` + text + `}`, provider.ClassNotFound, ""},
		"standard view":      {`{"view_id":"` + colStandard + `","field_id":"` + flText + `",` + text + `}`, "", "custom views"},
		"index view":         {`{"view_id":"` + colIndex + `","field_id":"` + flText + `",` + text + `}`, "", "custom views"},
		"foreign field":      {filterCreateArgs(flOther, text), "", "a filter can be written for"},
		"system field":       {filterCreateArgs(flSystem, text), "", "a filter can be written for"},
		"inactive field":     {filterCreateArgs(flOff, text), "", "a filter can be written for"},
		"relation field":     {filterCreateArgs(flRel, `"operand":"IS_EMPTY"`), "", "a filter can be written for"},
		"unsupported type":   {filterCreateArgs(flPos, `"operand":"IS_EMPTY"`), "", "a filter can be written for"},
		"operand for type":   {filterCreateArgs(flText, `"operand":"IS","text":"x"`), "", "not supported for a field of type TEXT"},
		"unknown operand":    {filterCreateArgs(flText, `"operand":"STARTS_WITH","text":"x"`), "", "supported filter operands"},
		"vector search":      {filterCreateArgs(flText, `"operand":"VECTOR_SEARCH","text":"x"`), "", "supported filter operands"},
		"missing operand":    {filterCreateArgs(flText, `"text":"x"`), "", "supported filter operands"},
		"value for empty":    {filterCreateArgs(flText, `"operand":"IS_EMPTY","text":"x"`), "", "takes no value"},
		"missing value":      {filterCreateArgs(flText, `"operand":"CONTAINS"`), "", "exactly one value"},
		"wrong value kind":   {filterCreateArgs(flText, `"operand":"CONTAINS","number":1`), "", "exactly one value"},
		"two values":         {filterCreateArgs(flText, `"operand":"CONTAINS","text":"x","flag":true`), "", "at most one value"},
		"control character":  {filterCreateArgs(flText, `"operand":"CONTAINS","text":"a\u0007b"`), "", "control characters"},
		"unknown option":     {filterCreateArgs(flSelect, `"operand":"IS","options":["Z"]`), "", "options of this field"},
		"option of other":    {filterCreateArgs(flSelect, `"operand":"IS","options":["X"]`), "", "options of this field"},
		"bad date":           {filterCreateArgs(flDate, `"operand":"IS","date":"2026-13-01"`), "", "date must be"},
		"date time before":   {filterCreateArgs(flTime, `"operand":"IS_BEFORE","date":"2026-01-01"`), "", "date must be"},
		"relative with zone": {filterCreateArgs(flDate, `"operand":"IS_RELATIVE","relative":{"direction":"PAST","amount":0,"unit":"DAY"}`), "", "amount"},
		"boolean as text":    {filterCreateArgs(flBool, `"operand":"IS","text":"true"`), "", "exactly one value"},
		"not a view uuid":    {`{"view_id":"nope","field_id":"` + flText + `",` + text + `}`, "", "UUIDs"},
		"not a field uuid":   {`{"view_id":"` + colView + `","field_id":"nope",` + text + `}`, "", "UUIDs"},
		"invalid json":       {`[]`, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveFilters(t, http.StatusCreated, filterWire(flNew, colView, flText, "CONTAINS", ""))
			_, err := runColumn(invokeViewFiltersCreate, tt.args)
			if err == nil || len(columnWrites(calls)) != 0 {
				t.Fatalf("err = %v, writes = %+v", err, columnWrites(calls))
			}
			if tt.class != "" && classOf(err) != tt.class {
				t.Errorf("class = %q, err = %v", classOf(err), err)
			}
			if tt.class == "" && tt.text != "" && (!asInvalidOK(err) || !strings.Contains(err.Error(), tt.text)) {
				t.Errorf("err = %v, want invalid-request with %q", err, tt.text)
			}
			if strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("provider text in the error: %v", err)
			}
		})
	}
}

func TestViewFiltersHiddenViewsAnswerLikeUnknownOnes(t *testing.T) {
	serveFilters(t, http.StatusOK, `{}`)
	args := func(view string) string {
		return `{"view_id":"` + view + `","field_id":"` + flText + `","operand":"IS_EMPTY"}`
	}
	_, unknown := runColumn(invokeViewFiltersCreate, args(colUnknown))
	for _, view := range []string{colUnlisted, colWidget} {
		_, err := runColumn(invokeViewFiltersCreate, args(view))
		if err == nil || unknown == nil || err.Error() != unknown.Error() {
			t.Errorf("view %s: err = %v, want %v", view, err, unknown)
		}
	}
}

func TestViewFiltersUpdateReadsTheFilterThenSendsOneChange(t *testing.T) {
	for name, tt := range map[string]struct {
		id, args, operand, body, value string
	}{
		"value only":        {flOfText, `"text":"Hamburg"`, "CONTAINS", `{"value":"Hamburg"}`, "Hamburg"},
		"operand and value": {flOfText, `"operand":"DOES_NOT_CONTAIN","text":"Hamburg"`, "DOES_NOT_CONTAIN", `{"operand":"DOES_NOT_CONTAIN","value":"Hamburg"}`, "Hamburg"},
		"operand to empty":  {flOfText, `"operand":"IS_NOT_EMPTY"`, "IS_NOT_EMPTY", `{"operand":"IS_NOT_EMPTY","value":""}`, ""},
		"empty to value":    {flOfEmpty, `"operand":"CONTAINS","text":"x"`, "CONTAINS", `{"operand":"CONTAINS","value":"x"}`, "x"},
		"options":           {flOfSelect, `"options":["B"]`, "IS", `{"value":"[\"B\"]"}`, `["B"]`},
		"boolean":           {flOfBool, `"flag":true`, "IS", `{"value":"true"}`, "true"},
		"grouped filter":    {flOfGroup, `"text":"y"`, "CONTAINS", `{"value":"y"}`, "y"},
	} {
		t.Run(name, func(t *testing.T) {
			field := map[string]string{flOfEmpty: flText, flOfSelect: flSelect, flOfBool: flBool}[tt.id]
			if field == "" {
				field = flText
			}
			calls := serveFilters(t, http.StatusOK, filterWire(tt.id, colView, field, tt.operand, ""))
			result, err := runColumn(invokeViewFiltersUpdate, `{"id":"`+tt.id+`",`+tt.args+`}`)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := json.Marshal(result)
			var filter ViewFilterResult
			if json.Unmarshal(out, &filter) != nil || filter.ID != tt.id || filter.ViewID != colView ||
				filter.FieldID != field || filter.Operand != tt.operand || filter.Value != tt.value {
				t.Fatalf("filter = %s", out)
			}
			sent := columnWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].URI != viewFiltersPath+"/"+tt.id ||
				sent[0].Body != tt.body {
				t.Errorf("writes = %+v, want body %s", sent, tt.body)
			}
			if (*calls)[0].URI != viewFiltersPath+"/"+tt.id || (*calls)[1].URI != viewsPath+"/"+colView {
				t.Errorf("the filter and its view are read first: %+v", *calls)
			}
		})
	}
}

func TestViewFiltersUpdateRefusesWithoutAWritingRequest(t *testing.T) {
	for name, tt := range map[string]struct {
		id, args string
		class    provider.Class
		text     string
	}{
		"filter of unlisted view": {flOfUnlist, `"text":"x"`, provider.ClassNotFound, ""},
		"filter of standard view": {flOfStd, `"text":"x"`, "", "custom views"},
		"unknown filter":          {colUnknown, `"text":"x"`, provider.ClassNotFound, ""},
		"deleted filter":          {flOfDeleted, `"text":"x"`, provider.ClassNotFound, ""},
		"sub field filter":        {flOfSub, `"text":"x"`, "", "without sub field"},
		"relation target filter":  {flOfRel, `"text":"x"`, "", "without sub field"},
		"vector search filter":    {flOfVector, `"text":"x"`, "", "without sub field"},
		"value for empty filter":  {flOfEmpty, `"text":"x"`, "", "takes no value"},
		"operand needs value":     {flOfText, `"operand":"DOES_NOT_CONTAIN"`, "", "exactly one value"},
		"operand for type":        {flOfText, `"operand":"IS","text":"x"`, "", "not supported for a field of type TEXT"},
		"unknown option":          {flOfSelect, `"options":["Z"]`, "", "options of this field"},
		"unknown operand":         {flOfText, `"operand":"VECTOR_SEARCH","text":"x"`, "", "supported filter operands"},
		"control character":       {flOfText, `"text":"a\u0000b"`, "", "control characters"},
		"nothing to change":       {flOfText, ``, "", "operand or one value"},
		"two values":              {flOfText, `"text":"x","flag":true`, "", "at most one value"},
		"not a uuid":              {"nope", `"text":"x"`, "", "must be a UUID"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveFilters(t, http.StatusOK, filterWire(flOfText, colView, flText, "CONTAINS", ""))
			args := `{"id":"` + tt.id + `"` + map[bool]string{true: "," + tt.args, false: ""}[tt.args != ""] + `}`
			_, err := runColumn(invokeViewFiltersUpdate, args)
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

func TestViewFiltersUpdateMovesAnUnwrittenOperandToAWrittenOne(t *testing.T) {
	calls := serveFilters(t, http.StatusOK, filterWire(flOfVector, colView, flText, "CONTAINS", ""))
	if _, err := runColumn(invokeViewFiltersUpdate, `{"id":"`+flOfVector+`","operand":"CONTAINS","text":"x"}`); err != nil {
		t.Fatal(err)
	}
	if sent := columnWrites(calls); len(sent) != 1 || sent[0].Body != `{"operand":"CONTAINS","value":"x"}` {
		t.Errorf("writes = %+v", sent)
	}
}

func TestViewFilterToolsNeedAConnectionWithoutObjectTargets(t *testing.T) {
	refuse(t)
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create": {invokeViewFiltersCreate, filterCreateArgs(flText, `"operand":"IS_EMPTY"`)},
		"update": {invokeViewFiltersUpdate, `{"id":"` + flOfText + `","text":"x"}`},
	} {
		_, err := call.handler(context.Background(), targetConnection("object/person"), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(call.args))
		if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestViewFiltersRefuseInvalidArgumentsBeforeAnyRequest(t *testing.T) {
	refuse(t)
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create no operand":  {invokeViewFiltersCreate, filterCreateArgs(flText, `"text":"x"`)},
		"create bad operand": {invokeViewFiltersCreate, filterCreateArgs(flText, `"operand":"nope"`)},
		"create two values":  {invokeViewFiltersCreate, filterCreateArgs(flText, `"operand":"CONTAINS","text":"x","number":1`)},
		"create bad uuid":    {invokeViewFiltersCreate, `{"view_id":"x","field_id":"y","operand":"IS_EMPTY"}`},
		"update nothing":     {invokeViewFiltersUpdate, `{"id":"` + flOfText + `"}`},
		"update bad operand": {invokeViewFiltersUpdate, `{"id":"` + flOfText + `","operand":"nope"}`},
		"update two values":  {invokeViewFiltersUpdate, `{"id":"` + flOfText + `","text":"x","number":1}`},
		"update bad uuid":    {invokeViewFiltersUpdate, `{"id":"x","text":"x"}`},
	} {
		if _, err := runColumn(call.handler, call.args); !asInvalidOK(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestViewFiltersNeverRepeatAfterAnUnclearResult(t *testing.T) {
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
			"create": {invokeViewFiltersCreate, filterCreateArgs(flText, `"operand":"CONTAINS","text":"x"`)},
			"update": {invokeViewFiltersUpdate, `{"id":"` + flOfText + `","text":"y"}`},
		} {
			t.Run(name+" "+action, func(t *testing.T) {
				written := 0
				serveFilters(t, http.StatusOK, `{}`)
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

func TestViewFiltersAnswerMustNameTheRequestedFilter(t *testing.T) {
	create := filterCreateArgs(flText, `"operand":"CONTAINS","text":"x"`)
	update := `{"id":"` + flOfText + `","text":"y"}`
	for name, tt := range map[string]struct {
		handler capability.Handler
		args    string
		answer  string
	}{
		"create other view":    {invokeViewFiltersCreate, create, filterWire(flNew, colUnlisted, flText, "CONTAINS", "")},
		"create other field":   {invokeViewFiltersCreate, create, filterWire(flNew, colView, flNumber, "CONTAINS", "")},
		"create other operand": {invokeViewFiltersCreate, create, filterWire(flNew, colView, flText, "DOES_NOT_CONTAIN", "")},
		"create no id":         {invokeViewFiltersCreate, create, `{"viewId":"` + colView + `"}`},
		"create bad operand":   {invokeViewFiltersCreate, create, filterWire(flNew, colView, flText, "SIDEWAYS", "")},
		"update other id":      {invokeViewFiltersUpdate, update, filterWire(flOfSub, colView, flText, "CONTAINS", "")},
		"update other view":    {invokeViewFiltersUpdate, update, filterWire(flOfText, colUnlisted, flText, "CONTAINS", "")},
		"update other operand": {invokeViewFiltersUpdate, update, filterWire(flOfText, colView, flText, "IS_EMPTY", "")},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveFilters(t, http.StatusOK, tt.answer)
			_, err := runColumn(tt.handler, tt.args)
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") ||
				len(columnWrites(calls)) != 1 {
				t.Errorf("err = %v, writes = %d", err, len(columnWrites(calls)))
			}
		})
	}
}

func TestViewFiltersPermissionNamesTheViewsRight(t *testing.T) {
	for name, call := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create": {invokeViewFiltersCreate, filterCreateArgs(flText, `"operand":"IS_EMPTY"`)},
		"update": {invokeViewFiltersUpdate, `{"id":"` + flOfText + `","text":"y"}`},
	} {
		serveFilters(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
		_, err := runColumn(call.handler, call.args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "called Views") ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestViewFilterDescriptorsDeclareTheirRiskAndTheTable(t *testing.T) {
	for _, tt := range []struct {
		d      capability.Descriptor
		effect capability.Effect
		idem   capability.Idempotency
	}{
		{viewfiltersCreate, capability.EffectCreate, capability.IdempotencyNonIdempotent},
		{viewfiltersUpdate, capability.EffectUpdate, capability.IdempotencyIdempotent},
	} {
		want := capability.Risk{Effect: tt.effect, Idempotency: tt.idem, Confirmation: capability.ConfirmationRequired,
			OpenWorld: true, DataSensitivity: viewDataSensitivity}
		if tt.d.Risk != want || !tt.d.RequiresToolAllowList || tt.d.Provider != Provider {
			t.Errorf("%s = %+v", tt.d.ID, tt.d)
		}
		for _, operand := range filterOperands {
			if !strings.Contains(tt.d.Description, operand) {
				t.Errorf("%s: the description does not name %s", tt.d.ID, operand)
			}
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == viewfiltersCreate.ID || id == viewfiltersUpdate.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestViewFilterToolsNeedConfirmationAndTheToolsList(t *testing.T) {
	calls := serveFilters(t, http.StatusCreated, filterWire(flNew, colView, flText, "IS_EMPTY", ""))
	cfg := coreConfig()
	all := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}
	bare := cfg.Connections["crm"]
	bare.Effects = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Effects: all, Tools: []string{viewfiltersCreate.ID, viewfiltersUpdate.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	arguments := map[string]string{
		viewfiltersCreate.ID: filterCreateArgs(flText, `"operand":"IS_EMPTY"`),
		viewfiltersUpdate.ID: `{"id":"` + flOfText + `","text":"y"}`,
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
