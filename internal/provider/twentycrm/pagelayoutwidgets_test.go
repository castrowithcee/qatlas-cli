package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	wTabOther    = "77777777-0000-4000-8000-0000000000c1"
	wTabGone     = "77777777-0000-4000-8000-0000000000c2"
	wTabStray    = "77777777-0000-4000-8000-0000000000c3"
	wTabList     = "77777777-0000-4000-8000-0000000000c4"
	wTabFull     = "77777777-0000-4000-8000-0000000000c5"
	wTabMissing  = "77777777-0000-4000-8000-0000000000c6"
	wLayoutList  = "66666666-0000-4000-8000-0000000000b1"
	wLayoutFull  = "66666666-0000-4000-8000-0000000000b2"
	wNewWidget   = "88888888-0000-4000-8000-0000000000d1"
	wWidgetOther = "88888888-0000-4000-8000-0000000000d2"
	wWidgetGone  = "88888888-0000-4000-8000-0000000000d3"
	wWidgetStray = "88888888-0000-4000-8000-0000000000d4"
	wWidgetList  = "88888888-0000-4000-8000-0000000000d5"
	wWidgetNone  = "88888888-0000-4000-8000-0000000000d6"
	wViewKanban  = "66666666-0000-4000-8000-0000000000e1"
	wViewDeleted = "66666666-0000-4000-8000-0000000000e2"

	wPosition = `{"row":0,"column":1,"row_span":4,"column_span":6}`
	wGrid     = `{"layoutMode":"GRID","row":0,"column":1,"rowSpan":4,"columnSpan":6}`
)

func widgetWire(id, tab, title, kind, object, position string) string {
	encoded, _ := json.Marshal(title)
	objectJSON := `null`
	if object != "" {
		objectJSON = `"` + object + `"`
	}
	return `{"id":"` + id + `","pageLayoutTabId":"` + tab + `","title":` + string(encoded) + `,"type":"` + kind + `",` +
		`"objectMetadataId":` + objectJSON + `,"position":` + position + `,"isActive":true,"applicationId":"` + appCn + `",` +
		`"universalIdentifier":"` + creatorCn + `","configuration":{"configurationType":"` + kind + `","body":{"markdown":"` +
		layoutBodyText + `"}}}`
}

func tabRecord(id, layout, deleted string) string {
	del := `null`
	if deleted != "" {
		del = `"` + deleted + `"`
	}
	return `{"id":"` + id + `","title":"T","position":1,"pageLayoutId":"` + layout + `","layoutMode":"GRID","deletedAt":` + del +
		`,"applicationId":"` + appCn + `","widgets":[]}`
}

func fullLayout(id, tab string) string {
	widgets := make([]string, 0, dashboardWidgetsMax)
	for i := 0; i < dashboardWidgetsMax; i++ {
		widgets = append(widgets, `{"id":"88888888-0000-4000-8000-00000000`+strconv.Itoa(1000+i)+`","title":"W","type":"TASKS"}`)
	}
	return `{"id":"` + id + `","name":"Full","type":"DASHBOARD","tabs":[{"id":"` + tab + `","title":"T","position":0,` +
		`"layoutMode":"GRID","widgets":[` + strings.Join(widgets, ",") + `]}]}`
}

// serveWidgets answers the reads of the widget tools and every write with status and answer.
func serveWidgets(t *testing.T, status int, answer string) *[]colCall {
	t.Helper()
	calls := &[]colCall{}
	vlist := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(layoutJSON(layoutID, "DASHBOARD", ""), layoutID,
		wLayoutList), layoutTabID, wTabList), layoutWidgetD, wWidgetList)
	vlist = strings.Replace(vlist, `"icon":"IconHome","layoutMode":"GRID"`, `"icon":"IconHome","layoutMode":"VERTICAL_LIST"`, 1)
	layouts := map[string]string{
		layoutID:    layoutJSON(layoutID, "DASHBOARD", ""),
		layoutOther: layoutJSON(layoutOther, "RECORD_PAGE", ""),
		wLayoutList: vlist,
		wLayoutFull: fullLayout(wLayoutFull, wTabFull),
	}
	tabs := map[string]string{
		layoutTabID: tabRecord(layoutTabID, layoutID, ""),
		wTabOther:   tabRecord(wTabOther, layoutOther, ""),
		wTabGone:    tabRecord(wTabGone, layoutID, "2026-01-01T00:00:00Z"),
		wTabStray:   tabRecord(wTabStray, layoutID, ""),
		wTabList:    tabRecord(wTabList, wLayoutList, ""),
		wTabFull:    tabRecord(wTabFull, wLayoutFull, ""),
	}
	widgets := map[string]string{
		layoutWidgetD: widgetWire(layoutWidgetD, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", wGrid),
		layoutWidgetA: widgetWire(layoutWidgetA, layoutTabID, "Pipeline", "GRAPH", metaObjID, wGrid),
		wWidgetOther:  widgetWire(wWidgetOther, wTabOther, "Other", "STANDALONE_RICH_TEXT", "", wGrid),
		wWidgetGone: strings.Replace(widgetWire(wWidgetGone, layoutTabID, "Gone", "STANDALONE_RICH_TEXT", "", wGrid),
			`"isActive":true`, `"isActive":true,"deletedAt":"2026-01-01T00:00:00Z"`, 1),
		wWidgetStray: widgetWire(wWidgetStray, wTabStray, "Stray", "STANDALONE_RICH_TEXT", "", wGrid),
		wWidgetList:  widgetWire(wWidgetList, wTabList, "Listed", "STANDALONE_RICH_TEXT", "", `{"layoutMode":"VERTICAL_LIST","index":1}`),
	}
	views := map[string]string{
		colView:      colViewJSON(colView, "WORKSPACE", "TABLE", true, ""),
		colUnlisted:  colViewJSON(colUnlisted, "UNLISTED", "TABLE", true, ""),
		colWidget:    colViewJSON(colWidget, "WORKSPACE", "TABLE_WIDGET", true, ""),
		colStandard:  colViewJSON(colStandard, "WORKSPACE", "TABLE", false, ""),
		colIndex:     colViewJSON(colIndex, "WORKSPACE", "TABLE", true, "INDEX"),
		wViewKanban:  colViewJSON(wViewKanban, "WORKSPACE", "KANBAN", true, ""),
		wViewDeleted: strings.Replace(colViewJSON(wViewDeleted, "WORKSPACE", "TABLE", true, ""), `"isActive":true,`, `"isActive":true,"deletedAt":"2026-01-01T00:00:00Z",`, 1),
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
		for prefix, bodies := range map[string]map[string]string{pageLayoutsPath: layouts, pageLayoutTabsPath: tabs,
			pageLayoutWidgetsPath: widgets, viewsPath: views} {
			if body, ok := bodies[strings.TrimPrefix(path, prefix+"/")]; ok && strings.HasPrefix(path, prefix+"/") {
				return jsonResponse(http.StatusOK, body), nil
			}
		}
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func widgetCreateArgs(tab, kind, extra string) string {
	return `{"tab_id":"` + tab + `","type":"` + kind + `","title":"Notes","position":` + wPosition + extra + `}`
}

func richArgs(markdown string) string {
	encoded, _ := json.Marshal(markdown)
	return widgetCreateArgs(layoutTabID, "STANDALONE_RICH_TEXT", `,"markdown":`+string(encoded))
}

func tableArgs(view string) string {
	return widgetCreateArgs(layoutTabID, "RECORD_TABLE", `,"view_id":"`+view+`"`)
}

func TestPageLayoutWidgetsCreateRichTextReadsThenSendsOneFixedBody(t *testing.T) {
	calls := serveWidgets(t, http.StatusCreated, widgetWire(wNewWidget, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", wGrid))
	result, err := runColumn(invokePageLayoutWidgetsCreate, richArgs("# Goals\n\tone <b> & two"))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	want := `{"id":"` + wNewWidget + `","tab_id":"` + layoutTabID + `","type":"STANDALONE_RICH_TEXT","title":"Notes",` +
		`"position":{"layout_mode":"GRID","row":0,"column":1,"row_span":4,"column_span":6}}`
	if string(out) != want {
		t.Errorf("result = %s, want %s", out, want)
	}
	for _, canary := range []string{creatorCn, appCn, "applicationId", "universalIdentifier", layoutBodyText, "markdown", "Goals"} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	body := `{"pageLayoutTabId":"` + layoutTabID + `","title":"Notes","type":"STANDALONE_RICH_TEXT","position":` + wGrid +
		`,"configuration":{"configurationType":"STANDALONE_RICH_TEXT","body":{"markdown":"# Goals\n\tone \u003cb\u003e \u0026 two"}}}`
	sent := columnWrites(calls)
	if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].URI != pageLayoutWidgetsPath || sent[0].Body != body {
		t.Errorf("writes = %+v, want body %s", sent, body)
	}
	if len(*calls) != 3 || (*calls)[0].URI != pageLayoutTabsPath+"/"+layoutTabID ||
		(*calls)[1].URI != pageLayoutsPath+"/"+layoutID {
		t.Errorf("the tab and the layout are read first: %+v", *calls)
	}
}

func TestPageLayoutWidgetsCreateTableTakesTheObjectOfTheView(t *testing.T) {
	calls := serveWidgets(t, http.StatusCreated, widgetWire(wNewWidget, layoutTabID, "Notes", "RECORD_TABLE", metaObjID, wGrid))
	result, err := runColumn(invokePageLayoutWidgetsCreate, tableArgs(colView))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	if !strings.Contains(string(out), `"type":"RECORD_TABLE"`) || strings.Contains(string(out), metaObjID) ||
		strings.Contains(string(out), colView) {
		t.Errorf("result = %s", out)
	}
	body := `{"pageLayoutTabId":"` + layoutTabID + `","title":"Notes","type":"RECORD_TABLE","objectMetadataId":"` + metaObjID +
		`","position":` + wGrid + `,"configuration":{"configurationType":"RECORD_TABLE","viewId":"` + colView + `"}}`
	sent := columnWrites(calls)
	if len(sent) != 1 || sent[0].Body != body || sent[0].URI != pageLayoutWidgetsPath {
		t.Errorf("writes = %+v, want body %s", sent, body)
	}
	if (*calls)[2].URI != viewsPath+"/"+colView {
		t.Errorf("the view is read before the write: %+v", *calls)
	}
}

func TestPageLayoutWidgetsCreateRefusesWithoutAWritingRequest(t *testing.T) {
	withPosition := func(position string) string {
		return `{"tab_id":"` + layoutTabID + `","type":"STANDALONE_RICH_TEXT","title":"N","markdown":"x","position":` + position + `}`
	}
	for name, tt := range map[string]struct {
		args  string
		class provider.Class
		text  string
	}{
		"graph":             {widgetCreateArgs(layoutTabID, "GRAPH", `,"markdown":"x"`), "", "type"},
		"iframe":            {widgetCreateArgs(layoutTabID, "IFRAME", `,"markdown":"x"`), "", "type"},
		"front component":   {widgetCreateArgs(layoutTabID, "FRONT_COMPONENT", `,"markdown":"x"`), "", "type"},
		"view":              {widgetCreateArgs(layoutTabID, "VIEW", `,"markdown":"x"`), "", "type"},
		"unknown type":      {widgetCreateArgs(layoutTabID, "HOLOGRAM", `,"markdown":"x"`), "", "type"},
		"text without body": {widgetCreateArgs(layoutTabID, "STANDALONE_RICH_TEXT", ``), "", "type"},
		"table without":     {widgetCreateArgs(layoutTabID, "RECORD_TABLE", ``), "", "type"},
		"text with a view":  {widgetCreateArgs(layoutTabID, "STANDALONE_RICH_TEXT", `,"markdown":"x","view_id":"`+colView+`"`), "", "type"},
		"table with text":   {widgetCreateArgs(layoutTabID, "RECORD_TABLE", `,"markdown":"x","view_id":"`+colView+`"`), "", "type"},
		"empty markdown":    {richArgs(""), "", "markdown"},
		"blank markdown":    {richArgs(" \n\t "), "", "markdown"},
		"control markdown":  {richArgs("a\u0007b"), "", "markdown"},
		"carriage return":   {richArgs("a\rb"), "", "markdown"},
		"format character":  {richArgs("a‮b"), "", "markdown"},
		"long markdown":     {richArgs(strings.Repeat("x", dashboardMarkdownMax+1)), "", "markdown"},
		"control title":     {strings.Replace(richArgs("x"), `"Notes"`, `"a\u0007b"`, 1), "", "title"},
		"blank title":       {strings.Replace(richArgs("x"), `"Notes"`, `"  "`, 1), "", "title"},
		"long title":        {strings.Replace(richArgs("x"), `"Notes"`, `"`+strings.Repeat("x", 101)+`"`, 1), "", "title"},
		"negative row":      {withPosition(`{"row":-1,"column":0,"row_span":1,"column_span":1}`), "", "position"},
		"row beyond grid":   {withPosition(`{"row":100,"column":0,"row_span":1,"column_span":1}`), "", "position"},
		"column beyond":     {withPosition(`{"row":0,"column":12,"row_span":1,"column_span":1}`), "", "position"},
		"width beyond":      {withPosition(`{"row":0,"column":6,"row_span":1,"column_span":7}`), "", "position"},
		"height beyond":     {withPosition(`{"row":99,"column":0,"row_span":2,"column_span":1}`), "", "position"},
		"zero span":         {withPosition(`{"row":0,"column":0,"row_span":0,"column_span":1}`), "", "position"},
		"fraction":          {withPosition(`{"row":0.5,"column":0,"row_span":1,"column_span":1}`), "", "position"},
		"partial position":  {withPosition(`{"row":0,"column":0,"row_span":1}`), "", "position"},
		"tab not a uuid":    {strings.Replace(richArgs("x"), layoutTabID, "nope", 1), "", "UUID"},
		"table not a uuid":  {tableArgs("nope"), "", "UUID"},
		"foreign layout":    {strings.Replace(richArgs("x"), layoutTabID, wTabOther, 1), provider.ClassNotFound, ""},
		"deleted tab":       {strings.Replace(richArgs("x"), layoutTabID, wTabGone, 1), provider.ClassNotFound, ""},
		"unknown tab":       {strings.Replace(richArgs("x"), layoutTabID, wTabMissing, 1), provider.ClassNotFound, ""},
		"tab off layout":    {strings.Replace(richArgs("x"), layoutTabID, wTabStray, 1), provider.ClassNotFound, ""},
		"list tab":          {strings.Replace(richArgs("x"), layoutTabID, wTabList, 1), "", "grid tab"},
		"full tab":          {strings.Replace(richArgs("x"), layoutTabID, wTabFull, 1), "", "most widgets"},
		"index view":        {tableArgs(colIndex), "", "view"},
		"standard view":     {tableArgs(colStandard), "", "view"},
		"personal view":     {tableArgs(colUnlisted), provider.ClassNotFound, ""},
		"widget view":       {tableArgs(colWidget), provider.ClassNotFound, ""},
		"kanban view":       {tableArgs(wViewKanban), "", "view"},
		"deleted view":      {tableArgs(wViewDeleted), provider.ClassNotFound, ""},
		"unknown view":      {tableArgs(colUnknown), provider.ClassNotFound, ""},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusCreated, widgetWire(wNewWidget, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", wGrid))
			_, err := runColumn(invokePageLayoutWidgetsCreate, tt.args)
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

func TestPageLayoutWidgetsHiddenTabsAnswerLikeUnknownOnes(t *testing.T) {
	serveWidgets(t, http.StatusCreated, `{}`)
	_, unknown := runColumn(invokePageLayoutWidgetsCreate, strings.Replace(richArgs("x"), layoutTabID, wTabMissing, 1))
	for _, tab := range []string{wTabOther, wTabGone, wTabStray} {
		_, err := runColumn(invokePageLayoutWidgetsCreate, strings.Replace(richArgs("x"), layoutTabID, tab, 1))
		if err == nil || unknown == nil || err.Error() != unknown.Error() {
			t.Errorf("tab %s: err = %v, want %v", tab, err, unknown)
		}
	}
	_, unknown = runColumn(invokePageLayoutWidgetsUpdate, `{"id":"`+wWidgetNone+`","title":"T"}`)
	for _, widget := range []string{wWidgetOther, wWidgetGone, wWidgetStray} {
		_, err := runColumn(invokePageLayoutWidgetsUpdate, `{"id":"`+widget+`","title":"T"}`)
		if err == nil || unknown == nil || err.Error() != unknown.Error() {
			t.Errorf("widget %s: err = %v, want %v", widget, err, unknown)
		}
	}
}

func TestPageLayoutWidgetsUpdateSendsOnlyTitleAndPosition(t *testing.T) {
	for name, tt := range map[string]struct{ args, body, answer string }{
		"title": {`{"id":"` + layoutWidgetD + `","title":"  New    name "}`, `{"title":"New name"}`,
			widgetWire(layoutWidgetD, layoutTabID, "New name", "STANDALONE_RICH_TEXT", "", wGrid)},
		"position": {`{"id":"` + layoutWidgetD + `","position":` + wPosition + `}`, `{"position":` + wGrid + `}`,
			widgetWire(layoutWidgetD, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", wGrid)},
		"both": {`{"id":"` + layoutWidgetD + `","title":"N","position":` + wPosition + `}`, `{"title":"N","position":` + wGrid + `}`,
			widgetWire(layoutWidgetD, layoutTabID, "N", "STANDALONE_RICH_TEXT", "", wGrid)},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusOK, tt.answer)
			result, err := runColumn(invokePageLayoutWidgetsUpdate, tt.args)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := json.Marshal(result)
			for _, canary := range []string{creatorCn, appCn, layoutBodyText, "configuration", "markdown"} {
				if strings.Contains(string(out), canary) {
					t.Errorf("%s reached the output: %s", canary, out)
				}
			}
			sent := columnWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodPatch || sent[0].URI != pageLayoutWidgetsPath+"/"+layoutWidgetD ||
				sent[0].Body != tt.body {
				t.Errorf("writes = %+v, want body %s", sent, tt.body)
			}
			if len(*calls) != 4 || (*calls)[0].URI != pageLayoutWidgetsPath+"/"+layoutWidgetD ||
				(*calls)[1].URI != pageLayoutTabsPath+"/"+layoutTabID || (*calls)[2].URI != pageLayoutsPath+"/"+layoutID {
				t.Errorf("widget, tab, and layout are read first: %+v", *calls)
			}
		})
	}
}

func TestPageLayoutWidgetsUpdateRefusesWithoutAWritingRequest(t *testing.T) {
	for name, tt := range map[string]struct {
		args  string
		class provider.Class
		text  string
	}{
		"nothing":          {`{"id":"` + layoutWidgetD + `"}`, "", "title, a position"},
		"partial position": {`{"id":"` + layoutWidgetD + `","position":{"row":0,"column":0,"row_span":1}}`, "", "position"},
		"beyond grid":      {`{"id":"` + layoutWidgetD + `","position":{"row":0,"column":11,"row_span":1,"column_span":2}}`, "", "position"},
		"blank title":      {`{"id":"` + layoutWidgetD + `","title":" "}`, "", "title"},
		"control title":    {`{"id":"` + layoutWidgetD + `","title":"a\u0007"}`, "", "title"},
		"not a uuid":       {`{"id":"nope","title":"T"}`, "", "UUID"},
		"list tab":         {`{"id":"` + wWidgetList + `","position":` + wPosition + `}`, "", "grid tab"},
		"unknown":          {`{"id":"` + wWidgetNone + `","title":"T"}`, provider.ClassNotFound, ""},
		"deleted":          {`{"id":"` + wWidgetGone + `","title":"T"}`, provider.ClassNotFound, ""},
		"foreign layout":   {`{"id":"` + wWidgetOther + `","title":"T"}`, provider.ClassNotFound, ""},
		"tab off layout":   {`{"id":"` + wWidgetStray + `","title":"T"}`, provider.ClassNotFound, ""},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusOK, `{}`)
			_, err := runColumn(invokePageLayoutWidgetsUpdate, tt.args)
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

func TestPageLayoutWidgetsUpdateKeepsTheTitleOfAWidgetInAListTab(t *testing.T) {
	calls := serveWidgets(t, http.StatusOK, widgetWire(wWidgetList, wTabList, "Renamed", "STANDALONE_RICH_TEXT", "", `{"layoutMode":"VERTICAL_LIST","index":1}`))
	if _, err := runColumn(invokePageLayoutWidgetsUpdate, `{"id":"`+wWidgetList+`","title":"Renamed"}`); err != nil {
		t.Fatal(err)
	}
	if sent := columnWrites(calls); len(sent) != 1 || sent[0].Body != `{"title":"Renamed"}` {
		t.Errorf("writes = %+v", sent)
	}
}

func TestPageLayoutWidgetsNeedAConnectionWithoutObjectTargets(t *testing.T) {
	for name, tool := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create": {invokePageLayoutWidgetsCreate, richArgs("x")},
		"update": {invokePageLayoutWidgetsUpdate, `{"id":"` + layoutWidgetD + `","title":"T"}`},
	} {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			_, err := tool.handler(context.Background(), targetConnection("object/person"), countingResolver(t),
				&redact.Redactor{}, json.RawMessage(tool.args))
			if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestPageLayoutWidgetsNeverRepeatAfterAnUnclearResult(t *testing.T) {
	for tool, args := range map[string]struct {
		handler capability.Handler
		args    string
	}{
		"create": {invokePageLayoutWidgetsCreate, richArgs("x")},
		"update": {invokePageLayoutWidgetsUpdate, `{"id":"` + layoutWidgetD + `","title":"T"}`},
	} {
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
			t.Run(tool+" "+name, func(t *testing.T) {
				written := 0
				serveWidgets(t, http.StatusOK, `{}`)
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
				_, err := runColumn(args.handler, args.args)
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

func TestPageLayoutWidgetAnswersMustNameTheRequestedWidget(t *testing.T) {
	create := map[string]string{
		"other tab":   widgetWire(wNewWidget, wTabOther, "Notes", "STANDALONE_RICH_TEXT", "", wGrid),
		"other title": widgetWire(wNewWidget, layoutTabID, "Other", "STANDALONE_RICH_TEXT", "", wGrid),
		"other type":  widgetWire(wNewWidget, layoutTabID, "Notes", "GRAPH", "", wGrid),
		"other place": widgetWire(wNewWidget, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", `{"layoutMode":"GRID","row":1,"column":1,"rowSpan":4,"columnSpan":6}`),
		"no place":    widgetWire(wNewWidget, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", `null`),
		"deleted":     strings.Replace(widgetWire(wNewWidget, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", wGrid), `"isActive":true`, `"deletedAt":"2026-01-01T00:00:00Z"`, 1),
		"no id":       `{"pageLayoutTabId":"` + layoutTabID + `"}`,
	}
	for name, answer := range create {
		t.Run("create "+name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusCreated, answer)
			_, err := runColumn(invokePageLayoutWidgetsCreate, richArgs("x"))
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") ||
				len(columnWrites(calls)) != 1 {
				t.Errorf("err = %v, writes = %d", err, len(columnWrites(calls)))
			}
		})
	}
	t.Run("create table with another object", func(t *testing.T) {
		calls := serveWidgets(t, http.StatusCreated, widgetWire(wNewWidget, layoutTabID, "Notes", "RECORD_TABLE", metaOtherID, wGrid))
		_, err := runColumn(invokePageLayoutWidgetsCreate, tableArgs(colView))
		if classOf(err) != provider.ClassInvalidResponse || len(columnWrites(calls)) != 1 {
			t.Errorf("err = %v", err)
		}
	})
	for name, answer := range map[string]string{
		"other widget": widgetWire(layoutWidgetA, layoutTabID, "T", "STANDALONE_RICH_TEXT", "", wGrid),
		"other tab":    widgetWire(layoutWidgetD, wTabOther, "T", "STANDALONE_RICH_TEXT", "", wGrid),
		"other type":   widgetWire(layoutWidgetD, layoutTabID, "T", "GRAPH", "", wGrid),
		"other title":  widgetWire(layoutWidgetD, layoutTabID, "X", "STANDALONE_RICH_TEXT", "", wGrid),
	} {
		t.Run("update "+name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusOK, answer)
			_, err := runColumn(invokePageLayoutWidgetsUpdate, `{"id":"`+layoutWidgetD+`","title":"T"}`)
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") ||
				len(columnWrites(calls)) != 1 {
				t.Errorf("err = %v, writes = %d", err, len(columnWrites(calls)))
			}
		})
	}
}

func TestPageLayoutWidgetsPermissionNamesTheLayoutsRight(t *testing.T) {
	serveWidgets(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
	for _, call := range []func() error{
		func() error { _, err := runColumn(invokePageLayoutWidgetsCreate, richArgs("x")); return err },
		func() error {
			_, err := runColumn(invokePageLayoutWidgetsUpdate, `{"id":"`+layoutWidgetD+`","title":"T"}`)
			return err
		},
	} {
		err := call()
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "called Layouts") ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("err = %v", err)
		}
	}
}

func TestPageLayoutWidgetDescriptorsDeclareTheirRisk(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, tool := range []struct {
		d    capability.Descriptor
		risk capability.Risk
	}{
		{pagelayoutwidgetsCreate, capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: layoutDataSensitivity}},
		{pagelayoutwidgetsUpdate, capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: layoutDataSensitivity}},
	} {
		d, want := tool.d, tool.risk
		if d.Risk != want || !d.RequiresToolAllowList || d.Provider != Provider {
			t.Errorf("%s = %+v", d.ID, d)
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
	}
}

func TestPageLayoutWidgetsNeedConfirmationAndTheToolsList(t *testing.T) {
	for _, tool := range []struct {
		id   string
		args string
	}{
		{pagelayoutwidgetsCreate.ID, richArgs("x")},
		{pagelayoutwidgetsUpdate.ID, `{"id":"` + layoutWidgetD + `","title":"T"}`},
	} {
		calls := serveWidgets(t, http.StatusCreated, `{}`)
		cfg := coreConfig()
		all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
		bare := cfg.Connections["crm"]
		bare.Permissions = all
		cfg.Connections["crm"] = bare
		cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
			Permissions: all, Tools: []string{tool.id}}
		stubLimiter(t, internalKey)
		red := &redact.Redactor{}
		core := application.New(registry(t), cfg, resolver(red), red)
		args := json.RawMessage(tool.args)
		request := application.InvokeRequest{Operation: tool.id, Connection: "crm", Arguments: args, Confirmed: true}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s offered without a tools list", tool.id)
		}
		request = application.InvokeRequest{Operation: tool.id, Connection: "crm-internal", Arguments: args}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirmation", tool.id)
		}
		if len(*calls) != 0 {
			t.Errorf("requests before approval: %+v", *calls)
		}
	}
}
