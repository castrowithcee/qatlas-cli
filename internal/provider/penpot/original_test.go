package penpot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func exportFailure(t *testing.T, handler func(call) (*http.Response, error)) *provider.Error {
	t.Helper()
	var calls []call
	serve(t, &calls, handler)
	dir := t.TempDir()
	red := &redact.Redactor{}
	_, err := invokeFilesExport(context.Background(), resolvedFor([]string{"team/" + teamA}, "", dir),
		resolver(red, nil), red,
		json.RawMessage(`{"project_id":"`+projectA1+`","file_id":"`+fileA1+`","local_path":`+
			strconv.Quote(filepath.Join(dir, "o.penpot"))+`}`))
	var failure *provider.Error
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "SECRET-HINT") {
		t.Fatalf("message carries provider text: %v", err)
	}
	return failure
}

func TestRPCErrorOriginal(t *testing.T) {
	for _, status := range []int{401, 403, 404, 400, 422, 500} {
		failure := exportFailure(t, func(c call) (*http.Response, error) {
			if c.command() == cmdExportFile {
				return jsonResponse(status, `{"type":"validation","code":"bad-thing","hint":"SECRET-HINT",`+
					`"explain":"EXPLAIN","data":{"x":1}}`), nil
			}
			return filesHandler(0)(c)
		})
		o := failure.Original
		if o == nil || o.Status != status || len(o.Entries) != 1 {
			t.Fatalf("%d: original = %+v", status, o)
		}
		if e := o.Entries[0]; e.Type != "validation" || e.Code != "bad-thing" || e.Message != "SECRET-HINT" {
			t.Errorf("%d: entry = %+v", status, e)
		}
	}
}

func TestStreamErrorOriginal(t *testing.T) {
	failure := exportFailure(t, func(c call) (*http.Response, error) {
		if c.command() == cmdExportFile {
			return sseResponse(200, "event: error\ndata: {\"~:type\":\"~:validation\",\"~:code\":\"~:bad\","+
				"\"~:hint\":\"SECRET-HINT\",\"~:explain\":\"EXPLAIN\"}\n\n"), nil
		}
		return filesHandler(0)(c)
	})
	o := failure.Original
	if o == nil || o.Status != 200 || len(o.Entries) != 1 {
		t.Fatalf("original = %+v", o)
	}
	if e := o.Entries[0]; e.Type != "validation" || e.Code != "bad" || e.Message != "SECRET-HINT" {
		t.Errorf("entry = %+v", e)
	}
}

func TestExportStorageErrorOriginalIsStatusOnly(t *testing.T) {
	failure := exportFailure(t, func(c call) (*http.Response, error) {
		if c.path == exportPath+exportObj {
			return jsonResponse(404, `{"hint":"SECRET-HINT"}`), nil
		}
		return exportHandler(nil)(c)
	})
	if o := failure.Original; o == nil || o.Status != 404 || len(o.Entries) != 0 {
		t.Errorf("original = %+v", o)
	}
}
