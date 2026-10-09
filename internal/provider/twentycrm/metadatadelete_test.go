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

func inactive(body string) string {
	return strings.Replace(body, `"isActive":true`, `"isActive":false`, 1)
}

func TestMetaDeleteSendsOneDelete(t *testing.T) {
	for name, test := range map[string]struct {
		handler capability.Handler
		path    string
		read    string
		answer  string
		id      string
	}{
		"object": {invokeMetaObjectsDelete, metadataObjectsPath + "/" + metaObjID,
			inactive(metaObjectBody(metaObjID, true, false)), `{"data":{"deleteOneObject":{"id":"` + metaObjID + `"}}}`, metaObjID},
		"field": {invokeMetaFieldsDelete, metadataFieldsPath + "/" + metaFieldID,
			inactive(metaFieldBody("TEXT", true, false)), `{"data":{"deleteOneField":{"id":"` + metaFieldID + `"}}}`, metaFieldID},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, map[string]string{test.path: test.read}, metaAnswer(test.answer))
			out, err := runMetaWrite(test.handler, `{"id":"`+test.id+`"}`)
			if err != nil {
				t.Fatal(err)
			}
			sent := metaWrites(calls)
			if len(sent) != 1 || sent[0].Method != http.MethodDelete || sent[0].Path != test.path || sent[0].Body != "" ||
				!strings.Contains(out, test.id) || !strings.Contains(out, `"deleted":true`) {
				t.Errorf("writes = %+v, out = %s", sent, out)
			}
		})
	}
}

func TestMetaDeleteRefusesBeforeDelete(t *testing.T) {
	objPath := metadataObjectsPath + "/" + metaObjID
	fieldPath := metadataFieldsPath + "/" + metaFieldID
	for name, test := range map[string]struct {
		handler capability.Handler
		path    string
		read    string
		id      string
	}{
		"standard object": {invokeMetaObjectsDelete, objPath, inactive(metaObjectBody(metaObjID, false, false)), metaObjID},
		"system object":   {invokeMetaObjectsDelete, objPath, inactive(metaObjectBody(metaObjID, true, true)), metaObjID},
		"active object":   {invokeMetaObjectsDelete, objPath, metaObjectBody(metaObjID, true, false), metaObjID},
		"other object":    {invokeMetaObjectsDelete, objPath, inactive(metaObjectBody(metaOtherID, true, false)), metaObjID},
		"standard field":  {invokeMetaFieldsDelete, fieldPath, inactive(metaFieldBody("TEXT", false, false)), metaFieldID},
		"system field":    {invokeMetaFieldsDelete, fieldPath, inactive(metaFieldBody("TEXT", true, true)), metaFieldID},
		"relation field":  {invokeMetaFieldsDelete, fieldPath, inactive(metaFieldBody("RELATION", true, false)), metaFieldID},
		"morph field":     {invokeMetaFieldsDelete, fieldPath, inactive(metaFieldBody("MORPH_RELATION", true, false)), metaFieldID},
		"active field":    {invokeMetaFieldsDelete, fieldPath, metaFieldBody("TEXT", true, false), metaFieldID},
		"unknown state object": {invokeMetaObjectsDelete, objPath,
			strings.Replace(metaObjectBody(metaObjID, true, false), `"isActive":true,`, "", 1), metaObjID},
		"unknown state field": {invokeMetaFieldsDelete, fieldPath,
			strings.Replace(metaFieldBody("TEXT", true, false), `"isActive":true,`, "", 1), metaFieldID},
	} {
		t.Run(name, func(t *testing.T) {
			calls := serveMetaWrite(t, map[string]string{test.path: test.read}, metaAnswer(`{}`))
			_, err := runMetaWrite(test.handler, `{"id":"`+test.id+`"}`)
			if err == nil || len(metaWrites(calls)) != 0 {
				t.Errorf("err = %v, writes = %+v", err, metaWrites(calls))
			}
		})
	}
}

func TestMetaDeleteRefusesInvalidInput(t *testing.T) {
	for _, handler := range []capability.Handler{invokeMetaObjectsDelete, invokeMetaFieldsDelete} {
		for _, args := range []string{`{}`, `{"id":"x"}`, `{"id":"` + metaObjID + `","label":"a"}`,
			`{"id":"` + metaObjID + `","confirm":true}`} {
			refuse(t)
			if _, err := runMetaWrite(handler, args); !asInvalidOK(err) {
				t.Errorf("args %s: err = %v", args, err)
			}
		}
	}
}

func TestMetaDeleteNeverRetriesAndReportsUncertainty(t *testing.T) {
	for name, handler := range map[string]struct {
		h    capability.Handler
		path string
		read string
	}{
		"object": {invokeMetaObjectsDelete, metadataObjectsPath + "/" + metaObjID, inactive(metaObjectBody(metaObjID, true, false))},
		"field":  {invokeMetaFieldsDelete, metadataFieldsPath + "/" + metaFieldID, inactive(metaFieldBody("TEXT", true, false))},
	} {
		id := metaObjID
		if name == "field" {
			id = metaFieldID
		}
		for outcome, write := range map[string]func() (*http.Response, error){
			"server error": func() (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+metaWriteCn+`"}`), nil
			},
			"timeout":  func() (*http.Response, error) { return nil, &net.DNSError{IsTimeout: true, Err: metaWriteCn} },
			"reset":    func() (*http.Response, error) { return nil, errors.New(metaWriteCn + ": connection reset by peer") },
			"not json": func() (*http.Response, error) { return jsonResponse(http.StatusOK, `<html>`+metaWriteCn), nil },
			"foreign": func() (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"id":"`+metaOtherID+`"}`), nil
			},
		} {
			t.Run(name+" "+outcome, func(t *testing.T) {
				calls := serveMetaWrite(t, map[string]string{handler.path: handler.read}, write)
				_, err := runMetaWrite(handler.h, `{"id":"`+id+`"}`)
				if err == nil || len(metaWrites(calls)) != 1 || !strings.Contains(err.Error(), "may have taken effect") ||
					strings.Contains(err.Error(), metaWriteCn) {
					t.Errorf("err = %v, writes = %d", err, len(metaWrites(calls)))
				}
			})
		}
	}
}

func TestMetaDeleteNeedsWorkspaceScopeAndMapsPermission(t *testing.T) {
	refuse(t)
	_, err := runMetaWrite(invokeMetaObjectsDelete, `{"id":"`+metaObjID+`"}`, "object/person")
	if !asInvalidOK(err) || strings.Contains(err.Error(), "person") {
		t.Errorf("err = %v", err)
	}
	serveMetaWrite(t, map[string]string{metadataFieldsPath + "/" + metaFieldID: inactive(metaFieldBody("TEXT", true, false))},
		func() (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, `{"message":"`+metaWriteCn+`"}`), nil
		})
	_, err = runMetaWrite(invokeMetaFieldsDelete, `{"id":"`+metaFieldID+`"}`)
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Class != provider.ClassPermission || !strings.Contains(err.Error(), "Data model") ||
		strings.Contains(err.Error(), metaWriteCn) {
		t.Errorf("err = %v", err)
	}
}

func TestMetaDeleteDescriptorsAreGuarded(t *testing.T) {
	for _, d := range []capability.Descriptor{metaObjectsDelete, metaFieldsDelete} {
		risk := d.Risk
		if !d.RequiresToolAllowList || risk.Effect != capability.EffectDelete ||
			risk.Confirmation != capability.ConfirmationRequired || !risk.OpenWorld ||
			risk.DataSensitivity != metadataSensitivity || risk.Idempotency == "" {
			t.Errorf("%s = %+v", d.ID, d)
		}
		if !strings.Contains(d.Description, "every user and integration") || !strings.Contains(d.Description, "final") {
			t.Errorf("%s omits the workspace-wide or final effect", d.ID)
		}
	}
}
