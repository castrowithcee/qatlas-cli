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

const layoutNewTab = "77777777-0000-4000-8000-0000000000b9"

func tabWire(layout, title string, position float64, extra string) string {
	pos, _ := json.Marshal(position)
	return `{"id":"` + layoutNewTab + `","title":"` + title + `","position":` + string(pos) + `,"pageLayoutId":"` + layout +
		`","layoutMode":"GRID","icon":null,"isActive":true,"applicationId":"` + appCn + `","universalIdentifier":"` +
		creatorCn + `","widgets":[]` + extra + `}`
}

// serveTabs answers the layout reads from serveLayouts and every write with status and answer.
func serveTabs(t *testing.T, status int, answer string) *[]colCall {
	t.Helper()
	calls := &[]colCall{}
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
		switch request.URL.Path {
		case pageLayoutsPath + "/" + layoutID:
			return jsonResponse(http.StatusOK, layoutJSON(layoutID, "DASHBOARD", "")), nil
		case pageLayoutsPath + "/" + layoutOther:
			return jsonResponse(http.StatusOK, layoutJSON(layoutOther, "RECORD_PAGE", "")), nil
		case pageLayoutsPath + "/" + layoutGone:
			return jsonResponse(http.StatusOK, layoutJSON(layoutGone, "DASHBOARD", "2026-01-01T00:00:00Z")), nil
		}
		return jsonResponse(http.StatusNotFound, `{"message":"`+bodyCanary+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	return calls
}

func tabArgs(layout, title string, position string) string {
	encoded, _ := json.Marshal(title)
	return `{"layout_id":"` + layout + `","title":` + string(encoded) + `,"position":` + position + `}`
}

func TestPageLayoutTabsCreateReadsTheLayoutThenSendsOneFixedBody(t *testing.T) {
	calls := serveTabs(t, http.StatusCreated, tabWire(layoutID, "Pipeline", 2.5, ""))
	result, err := runColumn(invokePageLayoutTabsCreate, tabArgs(strings.ToUpper(layoutID), "Pipeline", "2.5"))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(result)
	want := `{"id":"` + layoutNewTab + `","layout_id":"` + layoutID + `","title":"Pipeline","position":2.5}`
	if string(out) != want {
		t.Errorf("result = %s, want %s", out, want)
	}
	for _, canary := range []string{creatorCn, appCn, "applicationId", "universalIdentifier"} {
		if strings.Contains(string(out), canary) {
			t.Errorf("%s reached the output: %s", canary, out)
		}
	}
	sent := columnWrites(calls)
	body := `{"pageLayoutId":"` + layoutID + `","title":"Pipeline","position":2.5,"layoutMode":"GRID"}`
	if len(sent) != 1 || sent[0].Method != http.MethodPost || sent[0].URI != pageLayoutTabsPath || sent[0].Body != body {
		t.Errorf("writes = %+v, want body %s", sent, body)
	}
	if (*calls)[0].Method != http.MethodGet || (*calls)[0].URI != pageLayoutsPath+"/"+layoutID {
		t.Errorf("the layout is read first: %+v", *calls)
	}
}

func TestPageLayoutTabsCreateRefusesWithoutAWritingRequest(t *testing.T) {
	long := strings.Repeat("x", 101)
	for name, tt := range map[string]struct {
		args  string
		class provider.Class
		text  string
	}{
		"other layout type": {tabArgs(layoutOther, "Tab", "1"), provider.ClassNotFound, ""},
		"deleted layout":    {tabArgs(layoutGone, "Tab", "1"), provider.ClassNotFound, ""},
		"unknown layout":    {tabArgs(layoutMissing, "Tab", "1"), provider.ClassNotFound, ""},
		"not a uuid":        {tabArgs("nope", "Tab", "1"), "", "UUID"},
		"empty title":       {tabArgs(layoutID, "", "1"), "", "title"},
		"blank title":       {tabArgs(layoutID, "  ", "1"), "", "title"},
		"control title":     {tabArgs(layoutID, "a\u0007b", "1"), "", "title"},
		"long title":        {tabArgs(layoutID, long, "1"), "", "title"},
		"negative position": {tabArgs(layoutID, "Tab", "-1"), "", "position"},
		"huge position":     {tabArgs(layoutID, "Tab", "1000001"), "", "position"},
		"no position":       {`{"layout_id":"` + layoutID + `","title":"Tab"}`, "", "position"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveTabs(t, http.StatusCreated, tabWire(layoutID, "Tab", 1, ""))
			_, err := runColumn(invokePageLayoutTabsCreate, tt.args)
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

func TestPageLayoutTabsHiddenLayoutsAnswerLikeUnknownOnes(t *testing.T) {
	serveTabs(t, http.StatusCreated, `{}`)
	_, unknown := runColumn(invokePageLayoutTabsCreate, tabArgs(layoutMissing, "Tab", "1"))
	for _, layout := range []string{layoutOther, layoutGone} {
		_, err := runColumn(invokePageLayoutTabsCreate, tabArgs(layout, "Tab", "1"))
		if err == nil || unknown == nil || err.Error() != unknown.Error() {
			t.Errorf("layout %s: err = %v, want %v", layout, err, unknown)
		}
	}
}

func TestPageLayoutTabsCreateNeedsAConnectionWithoutObjectTargets(t *testing.T) {
	refuse(t)
	_, err := invokePageLayoutTabsCreate(context.Background(), targetConnection("object/person"), countingResolver(t),
		&redact.Redactor{}, json.RawMessage(tabArgs(layoutID, "Tab", "1")))
	if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
		t.Errorf("err = %v", err)
	}
}

func TestPageLayoutTabsCreateNeverRepeatsAfterAnUnclearResult(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		fail   error
		body   string
	}{
		"server error": {status: 500, body: `{"error":"` + bodyCanary + `"}`},
		"gateway":      {status: 504},
		"unreadable":   {status: 201, body: `not json ` + bodyCanary},
		"timeout":      {fail: &net.DNSError{IsTimeout: true, Err: bodyCanary}},
		"connection":   {fail: errors.New(bodyCanary)},
	} {
		t.Run(name, func(t *testing.T) {
			written := 0
			serveTabs(t, http.StatusOK, `{}`)
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
			_, err := runColumn(invokePageLayoutTabsCreate, tabArgs(layoutID, "Tab", "1"))
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

func TestPageLayoutTabsAnswerMustNameTheRequestedTab(t *testing.T) {
	for name, answer := range map[string]string{
		"other layout": tabWire(layoutOther, "Tab", 1, ""),
		"other title":  tabWire(layoutID, "Other", 1, ""),
		"other pos":    tabWire(layoutID, "Tab", 2, ""),
		"deleted":      tabWire(layoutID, "Tab", 1, `,"deletedAt":"2026-01-01T00:00:00Z"`),
		"other mode":   strings.Replace(tabWire(layoutID, "Tab", 1, ""), `"GRID"`, `"CANVAS"`, 1),
		"no id":        `{"pageLayoutId":"` + layoutID + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveTabs(t, http.StatusCreated, answer)
			_, err := runColumn(invokePageLayoutTabsCreate, tabArgs(layoutID, "Tab", "1"))
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "before repeating") ||
				len(columnWrites(calls)) != 1 {
				t.Errorf("err = %v, writes = %d", err, len(columnWrites(calls)))
			}
		})
	}
}

func TestPageLayoutTabsPermissionNamesTheLayoutsRight(t *testing.T) {
	serveTabs(t, http.StatusForbidden, `{"messages":["`+bodyCanary+`"]}`)
	_, err := runColumn(invokePageLayoutTabsCreate, tabArgs(layoutID, "Tab", "1"))
	if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "called Layouts") ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestPageLayoutTabsDescriptorDeclaresItsRisk(t *testing.T) {
	d := pagelayouttabsCreate
	want := capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: layoutDataSensitivity}
	if d.Risk != want || !d.RequiresToolAllowList || d.Provider != Provider {
		t.Errorf("%s = %+v", d.ID, d)
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == d.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestPageLayoutTabsCreateNeedsConfirmationAndTheToolsList(t *testing.T) {
	calls := serveTabs(t, http.StatusCreated, tabWire(layoutID, "Tab", 1, ""))
	cfg := coreConfig()
	all := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}
	bare := cfg.Connections["crm"]
	bare.Effects = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Effects: all, Tools: []string{pagelayouttabsCreate.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	args := json.RawMessage(tabArgs(layoutID, "Tab", "1"))
	request := application.InvokeRequest{Operation: pagelayouttabsCreate.ID, Connection: "crm", Arguments: args, Confirmed: true}
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Error("offered without a tools list")
	}
	request = application.InvokeRequest{Operation: pagelayouttabsCreate.ID, Connection: "crm-internal", Arguments: args}
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Error("ran without confirmation")
	}
	if len(*calls) != 0 {
		t.Errorf("requests before approval: %+v", *calls)
	}
}
