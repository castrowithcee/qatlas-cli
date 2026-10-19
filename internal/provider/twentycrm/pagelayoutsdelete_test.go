package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

type layoutDeleteCase struct {
	name    string
	handler capability.Handler
	desc    *capability.Descriptor
	known   string
	path    string
	named   []string
	foreign map[string]string
}

func layoutDeleteCases() []layoutDeleteCase {
	return []layoutDeleteCase{
		{"widget", invokePageLayoutWidgetsDelete, &pagelayoutwidgetsDelete, layoutWidgetD, pageLayoutWidgetsPath,
			[]string{`"id":"` + layoutWidgetD + `"`, `"tab_id":"` + layoutTabID + `"`, `"type":"STANDALONE_RICH_TEXT"`,
				`"title":"Notes"`, `"deleted":true`},
			map[string]string{"unknown": wWidgetNone, "deleted": wWidgetGone, "record page": wWidgetOther,
				"tab off layout": wWidgetStray}},
		{"tab", invokePageLayoutTabsDelete, &pagelayouttabsDelete, layoutTabID, pageLayoutTabsPath,
			[]string{`"id":"` + layoutTabID + `"`, `"layout_id":"` + layoutID + `"`, `"title":"T"`, `"deleted":true`},
			map[string]string{"unknown": wTabMissing, "deleted": wTabGone, "record page": wTabOther,
				"tab off layout": wTabStray}},
	}
}

func TestPageLayoutDeleteReadsFirstThenSendsOneFixedDelete(t *testing.T) {
	for _, tt := range layoutDeleteCases() {
		t.Run(tt.name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusOK, `{"success":true}`)
			result, err := runColumn(tt.handler, `{"id":"`+strings.ToUpper(tt.known)+`"}`)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := json.Marshal(result)
			for _, want := range tt.named {
				if !strings.Contains(string(out), want) {
					t.Errorf("result %s omits %s", out, want)
				}
			}
			for _, canary := range []string{creatorCn, appCn, layoutBodyText, "configuration", "markdown"} {
				if strings.Contains(string(out), canary) {
					t.Errorf("%s reached the output: %s", canary, out)
				}
			}
			sent := columnWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodDelete || sent[0].URI != tt.path+"/"+tt.known ||
				sent[0].Body != "" || (*calls)[0].Method != http.MethodGet {
				t.Errorf("calls = %+v", *calls)
			}
		})
	}
}

func TestPageLayoutDeleteRefusesBeforeTheDelete(t *testing.T) {
	for _, tt := range layoutDeleteCases() {
		for name, id := range tt.foreign {
			t.Run(tt.name+" "+name, func(t *testing.T) {
				calls := serveWidgets(t, http.StatusOK, `{"success":true}`)
				_, err := runColumn(tt.handler, `{"id":"`+id+`"}`)
				if classOf(err) != provider.ClassNotFound || len(columnWrites(calls)) != 0 ||
					strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("err = %v, writes = %+v", err, columnWrites(calls))
				}
			})
		}
	}
}

func TestPageLayoutDeleteRefusesBadArgumentsAndTargetsBeforeAnyRequest(t *testing.T) {
	for _, tt := range layoutDeleteCases() {
		for name, args := range map[string]string{
			"no id": `{}`, "bad id": `{"id":"x"}`, "path": `{"id":"` + tt.known + `/x"}`,
			"extra": `{"id":"` + tt.known + `","force":true}`,
		} {
			t.Run(tt.name+" "+name, func(t *testing.T) {
				refuse(t)
				if _, err := runColumn(tt.handler, args); !asInvalidOK(err) {
					t.Errorf("err = %v, want invalid-request", err)
				}
			})
		}
		t.Run(tt.name+" workspace gate", func(t *testing.T) {
			refuse(t)
			_, err := tt.handler(context.Background(), targetConnection("object/person"), countingResolver(t),
				&redact.Redactor{}, json.RawMessage(`{"id":"`+tt.known+`"}`))
			if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
				t.Errorf("err = %v, want invalid-request without the target", err)
			}
		})
	}
}

func TestPageLayoutDeleteNeverRetriesAndReportsUncertainty(t *testing.T) {
	for _, tt := range layoutDeleteCases() {
		for name, reply := range map[string]struct {
			status int
			body   string
		}{
			"server error": {http.StatusInternalServerError, `{"message":"` + bodyCanary + `"}`},
			"not json":     {http.StatusOK, `<html>` + bodyCanary},
			"empty":        {http.StatusOK, ``},
			"no success":   {http.StatusOK, `{}`},
			"other id":     {http.StatusOK, `{"success":true,"id":"` + wTabMissing + `"}`},
		} {
			t.Run(tt.name+" "+name, func(t *testing.T) {
				calls := serveWidgets(t, reply.status, reply.body)
				_, err := runColumn(tt.handler, `{"id":"`+tt.known+`"}`)
				if err == nil || len(columnWrites(calls)) != 1 ||
					!strings.Contains(err.Error(), "may have taken effect") || strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("err = %v, writes = %d", err, len(columnWrites(calls)))
				}
			})
		}
		t.Run(tt.name+" permission", func(t *testing.T) {
			serveWidgets(t, http.StatusForbidden, `{"message":"`+bodyCanary+`"}`)
			_, err := runColumn(tt.handler, `{"id":"`+tt.known+`"}`)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "Layouts") ||
				strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestPageLayoutDeleteDescriptorsAreGuarded(t *testing.T) {
	want := capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: layoutDataSensitivity}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := reg.Lookup(Provider + ".page-layouts.delete"); ok {
		t.Error("page-layouts.delete is registered")
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, tt := range layoutDeleteCases() {
		d := tt.desc
		if d.Risk != want || !d.RequiresToolAllowList || d.Provider != Provider || d.Version != 1 {
			t.Errorf("descriptor = %+v", d)
		}
		for _, text := range []string{"Permanently", "cannot be restored", "Layouts"} {
			if !strings.Contains(d.Description, text) {
				t.Errorf("%s description omits %q", d.ID, text)
			}
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
	}
	if !strings.Contains(pagelayouttabsDelete.Description, "all widgets of the tab") {
		t.Error("tab description omits the widgets that go with the tab")
	}
}

func TestPageLayoutDeleteNeedsConfirmationAndTheToolsList(t *testing.T) {
	for _, tt := range layoutDeleteCases() {
		t.Run(tt.name, func(t *testing.T) {
			calls := serveWidgets(t, http.StatusOK, `{"success":true}`)
			cfg := coreConfig()
			all := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}
			bare := cfg.Connections["crm"]
			bare.Effects = all
			cfg.Connections["crm"] = bare
			cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
				Effects: all, Tools: []string{tt.desc.ID}}
			stubLimiter(t, internalKey)
			red := &redact.Redactor{}
			core := application.New(registry(t), cfg, resolver(red), red)
			args := json.RawMessage(`{"id":"` + tt.known + `"}`)
			if _, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: tt.desc.ID,
				Connection: "crm", Arguments: args, Confirmed: true}); err == nil {
				t.Error("offered by permissions alone")
			}
			if _, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: tt.desc.ID,
				Connection: "crm-internal", Arguments: args}); err == nil {
				t.Error("ran without confirmation")
			}
			if len(*calls) != 0 {
				t.Errorf("requests before approval: %+v", *calls)
			}
		})
	}
}
