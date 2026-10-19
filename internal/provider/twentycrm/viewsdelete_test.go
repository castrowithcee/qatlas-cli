package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
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

func vdReads(view string) map[string]string {
	return vwReads(map[string]string{viewsPath + "/" + viewID: view})
}

func vdArgs() string { return `{"id":"` + viewID + `"}` }

func TestViewsDeleteReadsFirstThenSendsOneFixedDelete(t *testing.T) {
	for name, answer := range map[string]string{
		"success only": `{"success":true}`,
		"with id":      `{"success":true,"id":"` + viewID + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, vdReads(vwView(viewID, "WORKSPACE", "TABLE", "", true)), metaAnswer(answer))
			out, err := runMetaWrite(invokeViewsDelete, `{"id":"`+strings.ToUpper(viewID)+`"}`)
			if err != nil {
				t.Fatal(err)
			}
			sent := metaWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodDelete || sent[0].Path != viewsPath+"/"+viewID ||
				sent[0].Body != "" || (*calls)[0].Method != http.MethodGet || (*calls)[0].Path != viewsPath+"/"+viewID {
				t.Fatalf("calls = %+v", *calls)
			}
			if !strings.Contains(out, `"id":"`+viewID+`"`) || !strings.Contains(out, `"name":"Open deals"`) ||
				!strings.Contains(out, `"object":"deal"`) || !strings.Contains(out, `"deleted":true`) ||
				strings.Contains(out, creatorCn) || strings.Contains(out, appCn) {
				t.Errorf("result = %s", out)
			}
		})
	}
}

func TestViewsDeleteRefusesBeforeTheDelete(t *testing.T) {
	path := viewsPath + "/" + viewID
	for name, body := range map[string]string{
		"standard": vwView(viewID, "WORKSPACE", "TABLE", "", false),
		"index":    vwView(viewID, "WORKSPACE", "TABLE", "INDEX", true),
		"unlisted": vwView(viewID, "UNLISTED", "TABLE", "", true),
		"widget":   vwView(viewID, "WORKSPACE", "TABLE_WIDGET", "", true),
		"other id": vwView(viewUnknown, "WORKSPACE", "TABLE", "", true),
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, map[string]string{path: body}, metaAnswer(`{"success":true}`))
			if _, err := runMetaWrite(invokeViewsDelete, vdArgs()); err == nil || len(metaWrites(calls)) != 0 {
				t.Errorf("err = %v, writes = %+v", err, metaWrites(calls))
			}
		})
	}
	t.Run("unknown view", func(t *testing.T) {
		calls := &[]string{}
		serve(t, func(request *http.Request) (*http.Response, error) {
			*calls = append(*calls, request.Method)
			return jsonResponse(http.StatusNotFound, `{"message":"`+vwProvider+`"}`), nil
		})
		stubLimiter(t, cloudKey)
		_, err := runMetaWrite(invokeViewsDelete, vdArgs())
		if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), vwProvider) ||
			len(*calls) != 1 || (*calls)[0] != http.MethodGet {
			t.Errorf("err = %v, calls = %v", err, *calls)
		}
	})
	t.Run("unlisted reads like unknown", func(t *testing.T) {
		serveMetaWrite(t, map[string]string{path: vwView(viewID, "UNLISTED", "TABLE", "", true)}, metaAnswer(`{}`))
		_, unlisted := runMetaWrite(invokeViewsDelete, vdArgs())
		serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusNotFound, `{}`), nil })
		stubLimiter(t, cloudKey)
		_, unknown := runMetaWrite(invokeViewsDelete, vdArgs())
		if unlisted == nil || unknown == nil || unlisted.Error() != unknown.Error() {
			t.Errorf("unlisted = %v, unknown = %v", unlisted, unknown)
		}
	})
}

func TestViewsDeleteNeedsAConnectionWithoutObjectTargets(t *testing.T) {
	refuse(t)
	_, err := runMetaWrite(invokeViewsDelete, vdArgs(), "object/person")
	if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
		t.Errorf("err = %v, want invalid-request without the target", err)
	}
}

func TestViewsDeleteRefusesBadArgumentsBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string]string{
		"no id":      `{}`,
		"bad id":     `{"id":"x"}`,
		"path":       `{"id":"` + viewID + `/viewFields"}`,
		"extra":      `{"id":"` + viewID + `","force":true}`,
		"visibility": `{"id":"` + viewID + `","visibility":"UNLISTED"}`,
	} {
		t.Run(name, func(t *testing.T) {
			refuse(t)
			if _, err := runMetaWrite(invokeViewsDelete, args); !asInvalidOK(err) {
				t.Errorf("err = %v, want invalid-request", err)
			}
		})
	}
}

func TestViewsDeleteNeverRetriesAndReportsUncertainty(t *testing.T) {
	for outcome, write := range map[string]func() (*http.Response, error){
		"server error": func() (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{"message":"`+vwProvider+`"}`), nil
		},
		"timeout": func() (*http.Response, error) { return nil, &net.DNSError{IsTimeout: true, Err: vwProvider} },
		"reset":   func() (*http.Response, error) { return nil, errors.New(vwProvider + ": connection reset by peer") },
		"not json": func() (*http.Response, error) {
			return jsonResponse(http.StatusOK, `<html>`+vwProvider), nil
		},
		"empty answer":    func() (*http.Response, error) { return jsonResponse(http.StatusOK, ``), nil },
		"no success":      func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{}`), nil },
		"success false":   func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"success":false}`), nil },
		"success as text": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"success":"true"}`), nil },
		"names other view": func() (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"success":true,"id":"`+viewUnknown+`"}`), nil
		},
		"a view, not a confirmation": func() (*http.Response, error) {
			return jsonResponse(http.StatusOK, vwView(viewID, "WORKSPACE", "TABLE", "", true)), nil
		},
	} {
		t.Run(outcome, func(t *testing.T) {
			calls := serveMetaWrite(t, vdReads(vwView(viewID, "WORKSPACE", "TABLE", "", true)), write)
			out, err := runMetaWrite(invokeViewsDelete, vdArgs())
			if err == nil || out != "" || len(metaWrites(calls)) != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
				strings.Contains(err.Error(), vwProvider) {
				t.Errorf("err = %v, writes = %d", err, len(metaWrites(calls)))
			}
		})
	}
}

func TestViewsDeleteMapsPermissionWithoutProviderText(t *testing.T) {
	serveMetaWrite(t, vdReads(vwView(viewID, "WORKSPACE", "TABLE", "", true)), func() (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, `{"message":"`+vwProvider+`"}`), nil
	})
	_, err := runMetaWrite(invokeViewsDelete, vdArgs())
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Class != provider.ClassPermission ||
		!strings.Contains(err.Error(), "Views") || strings.Contains(err.Error(), vwProvider) {
		t.Errorf("err = %v", err)
	}
}

func TestViewsDeleteWorksWithoutReadableDataModel(t *testing.T) {
	var sent int
	serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodDelete:
			sent++
			return jsonResponse(http.StatusOK, `{"success":true}`), nil
		case request.URL.Path == viewsPath+"/"+viewID:
			return jsonResponse(http.StatusOK, vwView(viewID, "WORKSPACE", "TABLE", "", true)), nil
		}
		return jsonResponse(http.StatusForbidden, `{}`), nil
	})
	stubLimiter(t, cloudKey)
	out, err := runMetaWrite(invokeViewsDelete, vdArgs())
	if err != nil || sent != 1 || !strings.Contains(out, `"deleted":true`) || strings.Contains(out, `"object"`) {
		t.Errorf("out = %s, err = %v, sent = %d", out, err, sent)
	}
}

func TestViewsDeleteDescriptorIsGuarded(t *testing.T) {
	want := capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: viewDataSensitivity}
	if viewsDelete.Risk != want || !viewsDelete.RequiresToolAllowList || viewsDelete.Provider != Provider ||
		viewsDelete.Version != 1 {
		t.Errorf("descriptor = %+v", viewsDelete)
	}
	for _, text := range []string{"Permanently", "cannot be restored", "all members of the workspace"} {
		if !strings.Contains(viewsDelete.Description, text) {
			t.Errorf("description omits %q", text)
		}
	}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == viewsDelete.ID {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}

func TestViewsDeleteNeedsConfirmationAndTheToolsList(t *testing.T) {
	calls := serveMetaWrite(t, vdReads(vwView(viewID, "WORKSPACE", "TABLE", "", true)), metaAnswer(`{"success":true}`))
	cfg := coreConfig()
	all := []config.Effect{config.EffectRead, config.EffectCreate, config.EffectUpdate, config.EffectDelete}
	bare := cfg.Connections["crm"]
	bare.Effects = all
	cfg.Connections["crm"] = bare
	cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
		Effects: all, Tools: []string{viewsDelete.ID}}
	stubLimiter(t, internalKey)
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	request := application.InvokeRequest{Operation: viewsDelete.ID, Connection: "crm",
		Arguments: json.RawMessage(vdArgs()), Confirmed: true}
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Error("views.delete was offered by permissions alone")
	}
	request = application.InvokeRequest{Operation: viewsDelete.ID, Connection: "crm-internal", Arguments: json.RawMessage(vdArgs())}
	if _, err := core.Invoke(context.Background(), request); err == nil {
		t.Error("views.delete ran without confirmation")
	}
	if len(*calls) != 0 {
		t.Errorf("requests before approval: %+v", *calls)
	}
}
