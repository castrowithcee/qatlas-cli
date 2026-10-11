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

const (
	vpID      = "99999999-0000-4000-8000-000000000001"
	vpField   = "99999999-0000-4000-8000-0000000000f1"
	vpOther   = "99999999-0000-4000-8000-0000000000e1"
	vpValue   = "value-canary-" + "9f3a"
	vpMissing = "99999999-0000-4000-8000-0000000000d1"
)

type vpCase struct {
	kind    viewPartDeleteKind
	handler capability.Handler
	hasProp bool
}

func vpCases() []vpCase {
	out := []vpCase{}
	for i := range viewPartDeleteKinds {
		kind := viewPartDeleteKinds[i]
		out = append(out, vpCase{kind: kind, handler: kind.invoke, hasProp: kind.kind != "group"})
	}
	return out
}

// vpWire is a part as Twenty serves it, with canaries in the fields the tools must never output.
func vpWire(c vpCase, id, view, extra string) string {
	field := ""
	if c.hasProp {
		field = `,"fieldMetadataId":"` + vpField + `"`
	}
	return `{"id":"` + id + `","viewId":"` + view + `"` + field + `,"value":"` + vpValue + `","fieldValue":"` + vpValue +
		`","workspaceId":"` + appCn + `","applicationId":"` + appCn + `"` + extra + `}`
}

type vpFake struct {
	calls []metaWriteCall
	parts map[string]string
	write func() (*http.Response, error)
}

func serveParts(t *testing.T, c vpCase, parts map[string]string, write func() (*http.Response, error)) *vpFake {
	t.Helper()
	fake := &vpFake{parts: parts, write: write}
	views := map[string]string{
		colView:     colViewJSON(colView, "WORKSPACE", "TABLE", true, ""),
		colUnlisted: colViewJSON(colUnlisted, "UNLISTED", "TABLE", true, ""),
		colWidget:   colViewJSON(colWidget, "WORKSPACE", "TABLE_WIDGET", true, ""),
		colStandard: colViewJSON(colStandard, "WORKSPACE", "TABLE", false, ""),
		colIndex:    colViewJSON(colIndex, "WORKSPACE", "TABLE", true, "INDEX"),
	}
	serve(t, func(request *http.Request) (*http.Response, error) {
		fake.calls = append(fake.calls, metaWriteCall{Method: request.Method, Path: request.URL.Path})
		if request.Method != http.MethodGet {
			return fake.write()
		}
		path := request.URL.Path
		switch {
		case strings.HasPrefix(path, viewsPath+"/"):
			if body, ok := views[strings.TrimPrefix(path, viewsPath+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		case strings.HasPrefix(path, c.kind.path+"/"):
			if body, ok := parts[strings.TrimPrefix(path, c.kind.path+"/")]; ok {
				return jsonResponse(http.StatusOK, body), nil
			}
		}
		return jsonResponse(http.StatusNotFound, `{"message":"`+vwProvider+`"}`), nil
	})
	stubLimiter(t, cloudKey)
	return fake
}

func (f *vpFake) writes() []metaWriteCall {
	out := []metaWriteCall{}
	for _, call := range f.calls {
		if call.Method != http.MethodGet {
			out = append(out, call)
		}
	}
	return out
}

func vpArgs(id string) string { return `{"id":"` + id + `"}` }

func TestViewPartDeleteReadsPartAndViewThenSendsOneFixedDelete(t *testing.T) {
	for _, c := range vpCases() {
		t.Run(c.kind.kind, func(t *testing.T) {
			fake := serveParts(t, c, map[string]string{vpID: vpWire(c, vpID, colView, "")}, metaAnswer(`{"success":true}`))
			out, err := runMetaWrite(c.handler, vpArgs(strings.ToUpper(vpID)))
			if err != nil {
				t.Fatal(err)
			}
			sent := fake.writes()
			if len(sent) != 1 || sent[0].Method != http.MethodDelete || sent[0].Path != c.kind.path+"/"+vpID ||
				len(fake.calls) != 3 || fake.calls[0].Path != c.kind.path+"/"+vpID || fake.calls[1].Path != viewsPath+"/"+colView {
				t.Fatalf("calls = %+v", fake.calls)
			}
			var got ViewPartDeleted
			if json.Unmarshal([]byte(out), &got) != nil || got.ID != vpID || got.Kind != c.kind.kind ||
				got.ViewID != colView || !got.Deleted || (c.hasProp && got.FieldID != vpField) || (!c.hasProp && got.FieldID != "") {
				t.Errorf("result = %s", out)
			}
			for _, canary := range []string{vpValue, appCn, creatorCn, "workspaceId"} {
				if strings.Contains(out, canary) {
					t.Errorf("%s reached the output: %s", canary, out)
				}
			}
		})
	}
}

func TestViewPartDeleteRefusesBeforeTheDelete(t *testing.T) {
	for _, c := range vpCases() {
		t.Run(c.kind.kind, func(t *testing.T) {
			parts := map[string]string{
				"11111111-0000-4000-8000-000000000001": vpWire(c, "11111111-0000-4000-8000-000000000001", colStandard, ""),
				"11111111-0000-4000-8000-000000000002": vpWire(c, "11111111-0000-4000-8000-000000000002", colIndex, ""),
				"11111111-0000-4000-8000-000000000003": vpWire(c, "11111111-0000-4000-8000-000000000003", colUnlisted, ""),
				"11111111-0000-4000-8000-000000000004": vpWire(c, "11111111-0000-4000-8000-000000000004", colWidget, ""),
				"11111111-0000-4000-8000-000000000005": vpWire(c, "11111111-0000-4000-8000-000000000005", colUnknown, ""),
				"11111111-0000-4000-8000-000000000006": vpWire(c, "11111111-0000-4000-8000-000000000006", colView,
					`,"deletedAt":"2026-01-01T00:00:00Z"`),
				"11111111-0000-4000-8000-000000000007": vpWire(c, vpOther, colView, ""),
			}
			fake := serveParts(t, c, parts, metaAnswer(`{"success":true}`))
			_, unknown := runMetaWrite(c.handler, vpArgs(vpMissing))
			if classOf(unknown) != provider.ClassNotFound || strings.Contains(unknown.Error(), vwProvider) {
				t.Fatalf("unknown = %v", unknown)
			}
			for name, want := range map[string]struct {
				id     string
				notice string
			}{
				"standard view": {"11111111-0000-4000-8000-000000000001", "custom views"},
				"index view":    {"11111111-0000-4000-8000-000000000002", "custom views"},
				"unlisted view": {"11111111-0000-4000-8000-000000000003", ""},
				"widget view":   {"11111111-0000-4000-8000-000000000004", ""},
				"unknown view":  {"11111111-0000-4000-8000-000000000005", ""},
				"deleted part":  {"11111111-0000-4000-8000-000000000006", ""},
				"part of other": {"11111111-0000-4000-8000-000000000007", "different"},
			} {
				_, err := runMetaWrite(c.handler, vpArgs(want.id))
				if err == nil {
					t.Errorf("%s: no error", name)
					continue
				}
				if want.notice == "" && err.Error() != unknown.Error() &&
					!strings.Contains(err.Error(), "does not hold this view "+c.kind.kind) {
					t.Errorf("%s: err = %v, want like unknown", name, err)
				}
				if want.notice != "" && !strings.Contains(err.Error(), want.notice) {
					t.Errorf("%s: err = %v", name, err)
				}
			}
			if len(fake.writes()) != 0 {
				t.Errorf("writes = %+v", fake.writes())
			}
		})
	}
}

func TestViewPartDeleteNeedsAConnectionWithoutObjectTargets(t *testing.T) {
	for _, c := range vpCases() {
		refuse(t)
		_, err := runMetaWrite(c.handler, vpArgs(vpID), "object/person")
		if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
			t.Errorf("%s: err = %v, want invalid-request without the target", c.kind.kind, err)
		}
	}
}

func TestViewPartDeleteRefusesBadArgumentsBeforeAnyRequest(t *testing.T) {
	for _, c := range vpCases() {
		for name, args := range map[string]string{
			"no id":  `{}`,
			"bad id": `{"id":"x"}`,
			"path":   `{"id":"` + vpID + `/x"}`,
			"extra":  `{"id":"` + vpID + `","force":true}`,
		} {
			t.Run(c.kind.kind+" "+name, func(t *testing.T) {
				refuse(t)
				if _, err := runMetaWrite(c.handler, args); !asInvalidOK(err) {
					t.Errorf("err = %v, want invalid-request", err)
				}
			})
		}
	}
}

func TestViewPartDeleteNeverRetriesAndReportsUncertainty(t *testing.T) {
	for _, c := range vpCases() {
		for outcome, write := range map[string]func() (*http.Response, error){
			"server error": func() (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+vwProvider+`"}`), nil
			},
			"timeout":       func() (*http.Response, error) { return nil, &net.DNSError{IsTimeout: true, Err: vwProvider} },
			"reset":         func() (*http.Response, error) { return nil, errors.New(vwProvider + ": connection reset") },
			"empty answer":  func() (*http.Response, error) { return jsonResponse(http.StatusOK, ``), nil },
			"no success":    func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{}`), nil },
			"success false": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `{"success":false}`), nil },
			"names other": func() (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"success":true,"id":"`+vpOther+`"}`), nil
			},
		} {
			t.Run(c.kind.kind+" "+outcome, func(t *testing.T) {
				fake := serveParts(t, c, map[string]string{vpID: vpWire(c, vpID, colView, "")}, write)
				out, err := runMetaWrite(c.handler, vpArgs(vpID))
				if err == nil || out != "" || len(fake.writes()) != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
					strings.Contains(err.Error(), vwProvider) {
					t.Errorf("err = %v, writes = %d", err, len(fake.writes()))
				}
			})
		}
	}
}

func TestViewPartDeleteMapsPermissionWithoutProviderText(t *testing.T) {
	for _, c := range vpCases() {
		serveParts(t, c, map[string]string{vpID: vpWire(c, vpID, colView, "")}, func() (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, `{"message":"`+vwProvider+`"}`), nil
		})
		_, err := runMetaWrite(c.handler, vpArgs(vpID))
		var failure *provider.Error
		if !errors.As(err, &failure) || failure.Class != provider.ClassPermission ||
			!strings.Contains(err.Error(), "Views") || strings.Contains(err.Error(), vwProvider) {
			t.Errorf("%s: err = %v", c.kind.kind, err)
		}
	}
}

func TestViewPartDeleteDescriptorsAreGuarded(t *testing.T) {
	want := capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: viewDataSensitivity}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, c := range vpCases() {
		d := c.kind.descriptor
		if d.Risk != want || !d.RequiresToolAllowList || d.Provider != Provider || d.Version != 1 {
			t.Errorf("descriptor = %+v", d)
		}
		for _, text := range []string{"Permanently", "cannot be restored", "all members of the workspace"} {
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
}

func TestViewPartDeleteNeedsConfirmationAndTheToolsList(t *testing.T) {
	for _, c := range vpCases() {
		fake := serveParts(t, c, map[string]string{vpID: vpWire(c, vpID, colView, "")}, metaAnswer(`{"success":true}`))
		cfg := coreConfig()
		all := []config.Permission{config.PermissionRead, config.PermissionCreate, config.PermissionUpdate, config.PermissionDelete}
		bare := cfg.Connections["crm"]
		bare.Permissions = all
		cfg.Connections["crm"] = bare
		cfg.Connections["crm-internal"] = config.Connection{Service: "crm-selfhosted", Credential: "crm-selfhosted-reader",
			Permissions: all, Tools: []string{c.kind.descriptor.ID}}
		stubLimiter(t, internalKey)
		red := &redact.Redactor{}
		core := application.New(registry(t), cfg, resolver(red), red)
		id := c.kind.descriptor.ID
		request := application.InvokeRequest{Operation: id, Connection: "crm", Arguments: json.RawMessage(vpArgs(vpID)), Confirmed: true}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s was offered by permissions alone", id)
		}
		request = application.InvokeRequest{Operation: id, Connection: "crm-internal", Arguments: json.RawMessage(vpArgs(vpID))}
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s ran without confirmation", id)
		}
		if len(fake.calls) != 0 {
			t.Errorf("requests before approval: %+v", fake.calls)
		}
	}
}
