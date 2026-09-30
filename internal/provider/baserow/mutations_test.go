package baserow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const schemaBody = `[{"id":1,"name":"Name","type":"text","primary":true},` +
	`{"id":2,"name":"Auftraege","type":"link_row","link_row_table_id":12},` +
	`{"id":3,"name":"Lieferant","type":"link_row","link_row_table_id":99},` +
	`{"id":4,"name":"Summe","type":"formula","read_only":true},` +
	`{"id":5,"name":"Erstellt","type":"created_on"},` +
	`{"id":6,"name":"Nummer","type":"number"},` +
	`{"id":7,"name":"Ohne","type":"link_row"}]`

// mutationServer answers the field read with the schema and every row change with status and body; it records
// the bodies of the changes.
func mutationServer(status int, body string, bodies *[]string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/api/database/fields/") {
			return jsonResponse(200, schemaBody), nil
		}
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			*bodies = append(*bodies, string(data))
		}
		return jsonResponse(status, body), nil
	}
}

func changes(calls []call) []call {
	var out []call
	for _, c := range calls {
		if c.method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func TestMutationDescriptorsCarryFullRisk(t *testing.T) {
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		allowList   bool
	}{
		rowsCreate.ID: {capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		rowsUpdate.ID: {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		rowsDelete.ID: {capability.EffectDelete, capability.IdempotencyIdempotent, true},
		rowsMove.ID:   {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
	}
	for _, d := range []capability.Descriptor{rowsCreate, rowsUpdate, rowsDelete, rowsMove} {
		w := want[d.ID]
		if d.Risk.Effect != w.effect || d.Risk.Idempotency != w.idempotency || d.RequiresToolAllowList != w.allowList ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Errorf("%s risk = %+v allowList=%t", d.ID, d.Risk, d.RequiresToolAllowList)
		}
	}
}

func TestRowsCreateSendsOneSchemaCheckedRequestAndMasksTheAnswer(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"id":9,"order":"2","Name":"Acme","Auftraege":[{"id":3,"value":"A-1"}],`+
		`"Lieferant":[{"id":4,"value":"secret-supplier"}]}`, &bodies))
	result, err := env.invokeConfirmed(rowsCreate.ID, "write",
		`{"table_id":11,"fields":{"Name":"Acme","Nummer":12.50,"Auftraege":[3]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "secret-supplier") || !strings.Contains(result, `"A-1"`) || !strings.Contains(result, `"id":9`) {
		t.Fatalf("result = %s", result)
	}
	if len(calls) != 2 || calls[0].method != http.MethodGet || calls[0].path != "/api/database/fields/table/11/" ||
		calls[1].method != http.MethodPost || calls[1].path != "/api/database/rows/table/11/" ||
		calls[1].query.Get("user_field_names") != "true" || calls[1].auth != "Token "+tokenValue {
		t.Fatalf("calls = %+v", calls)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"Nummer":12.50`) || !strings.Contains(bodies[0], `"Auftraege":[3]`) {
		t.Fatalf("bodies = %v", bodies)
	}
}

func TestRowsUpdateSendsPatch(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"id":7,"order":"1","Name":"Neu"}`, &bodies))
	if _, err := env.invokeConfirmed(rowsUpdate.ID, "write", `{"table_id":11,"row_id":7,"fields":{"Name":"Neu"}}`); err != nil {
		t.Fatal(err)
	}
	got := changes(calls)
	if len(got) != 1 || got[0].method != http.MethodPatch || got[0].path != "/api/database/rows/table/11/7/" {
		t.Fatalf("calls = %+v", calls)
	}
	if _, err := env.invokeConfirmed(rowsUpdate.ID, "write", `{"table_id":11,"row_id":7,"fields":{}}`); err == nil {
		t.Fatal("an update without cells must be refused")
	}
}

func TestRowsDeleteAndMoveSendOneRequestWithoutReadingFields(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(204, ``, &bodies))
	result, err := env.invokeConfirmed(rowsDelete.ID, "write", `{"table_id":11,"row_id":7}`)
	if err != nil || result != `{"deleted":true,"row_id":7}` {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if len(calls) != 1 || calls[0].method != http.MethodDelete || calls[0].path != "/api/database/rows/table/11/7/" {
		t.Fatalf("calls = %+v", calls)
	}
	calls = nil
	env = newEnvironment(t, &calls, mutationServer(200, `{"id":7}`, &bodies))
	result, err = env.invokeConfirmed(rowsMove.ID, "write", `{"table_id":11,"row_id":7,"before_id":3}`)
	if err != nil || result != `{"moved":true,"row_id":7}` {
		t.Fatalf("move = %s, %v", result, err)
	}
	if len(calls) != 1 || calls[0].method != http.MethodPatch || calls[0].path != "/api/database/rows/table/11/7/move/" ||
		calls[0].query.Get("before_id") != "3" {
		t.Fatalf("calls = %+v", calls)
	}
	calls = nil
	if _, err := env.invokeConfirmed(rowsMove.ID, "write", `{"table_id":11,"row_id":7}`); err != nil ||
		calls[0].query.Has("before_id") {
		t.Fatalf("move to the end: %+v, %v", calls, err)
	}
}

func TestMutationsRefuseForeignTablesBeforeSecretAndIO(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"id":1}`, &bodies))
	for op, args := range map[string]string{
		rowsCreate.ID: `{"table_id":99,"fields":{"Name":"x"}}`,
		rowsUpdate.ID: `{"table_id":99,"row_id":1,"fields":{"Name":"x"}}`,
		rowsDelete.ID: `{"table_id":99,"row_id":1}`,
		rowsMove.ID:   `{"table_id":99,"row_id":1,"before_id":2}`,
	} {
		_, err := env.invokeConfirmed(op, "write", args)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "99") {
			t.Errorf("%s: err = %v", op, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, secret reads = %d, want none", len(calls), *env.reads)
	}
}

func TestSchemaChecksRefuseBeforeTheChange(t *testing.T) {
	cases := map[string]struct{ connection, fields string }{
		"unknown field":          {"write", `{"Gibtesnicht":1}`},
		"flagged read-only":      {"write", `{"Summe":1}`},
		"computed type":          {"write", `{"Erstellt":"2026-01-01"}`},
		"link to foreign table":  {"write", `{"Lieferant":[4]}`},
		"link without table":     {"write", `{"Ohne":[1]}`},
		"link not an array":      {"write", `{"Auftraege":3}`},
		"link with bad item":     {"write", `{"Auftraege":[true]}`},
		"link with null":         {"write", `{"Auftraege":null}`},
		"link with zero":         {"write", `{"Auftraege":[0]}`},
		"wildcard read-only":     {"writeall", `{"Summe":1}`},
		"wildcard unknown table": {"writeall", `{"Ohne":[1]}`},
	}
	for name, c := range cases {
		var calls []call
		var bodies []string
		env := newEnvironment(t, &calls, mutationServer(200, `{"id":1}`, &bodies))
		_, err := env.invokeConfirmed(rowsCreate.ID, c.connection, `{"table_id":11,"fields":`+c.fields+`}`)
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
		if strings.Contains(err.Error(), "Gibtesnicht") || strings.Contains(err.Error(), "Lieferant") ||
			strings.Contains(err.Error(), "99") {
			t.Errorf("%s: message quotes provider data: %v", name, err)
		}
		if len(calls) != 1 || calls[0].method != http.MethodGet || len(bodies) != 0 {
			t.Errorf("%s: calls = %+v, want only the field read", name, calls)
		}
	}
}

func TestWildcardAllowsLinksToAnyTable(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"id":1,"Lieferant":[{"id":4,"value":"kept"}]}`, &bodies))
	result, err := env.invokeConfirmed(rowsUpdate.ID, "writeall", `{"table_id":99,"row_id":1,"fields":{"Lieferant":[4]}}`)
	if err != nil || !strings.Contains(result, "kept") || len(changes(calls)) != 1 {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
}

func TestOversizedRequestIsRefusedBeforeSecretAndIO(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"id":1}`, &bodies))
	_, err := env.invokeConfirmed(rowsCreate.ID, "write",
		`{"table_id":11,"fields":{"Name":"`+strings.Repeat("a", maxRequestBytes)+`"}}`)
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %d, secret reads = %d", err, len(calls), *env.reads)
	}
}

func TestMutationsNeedConfirmationToolListAndRights(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(204, ``, &bodies))
	_, err := env.invoke(rowsUpdate.ID, "write", `{"table_id":11,"row_id":7,"fields":{"Name":"x"}}`)
	if err == nil {
		t.Fatal("a change ran without confirm")
	}
	if _, err := env.invokeConfirmed(rowsDelete.ID, "writenodelete", `{"table_id":11,"row_id":7}`); err == nil {
		t.Fatal("delete ran without the tool allow-list")
	}
	if _, err := env.invokeConfirmed(rowsCreate.ID, "one", `{"table_id":11,"fields":{}}`); err == nil {
		t.Fatal("a read-only connection created a row")
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestChangeFailuresAreClassifiedAndUncertainOnesSaySo(t *testing.T) {
	cases := []struct {
		status    int
		class     provider.Class
		uncertain bool
		hint      string
	}{
		{400, provider.ClassProviderError, false, ""},
		{401, provider.ClassAuth, false, ""},
		{403, provider.ClassPermission, false, "update right"},
		{404, provider.ClassNotFound, false, ""},
		{429, provider.ClassRateLimited, false, ""},
		{500, provider.ClassProviderError, true, ""},
		{503, provider.ClassUnreachable, true, ""},
		{504, provider.ClassTimeout, true, ""},
	}
	for _, c := range cases {
		var calls []call
		var bodies []string
		env := newEnvironment(t, &calls, mutationServer(c.status, `{"error":"`+bodyCanary+tokenValue+`"}`, &bodies))
		_, err := env.invokeConfirmed(rowsUpdate.ID, "write", `{"table_id":11,"row_id":7,"fields":{"Name":"x"}}`)
		if classOf(err) != c.class {
			t.Errorf("status %d: class = %q", c.status, classOf(err))
			continue
		}
		text := err.Error()
		if strings.Contains(text, bodyCanary) || strings.Contains(text, tokenValue) {
			t.Errorf("status %d: leaked: %v", c.status, err)
		}
		if strings.Contains(text, "may have taken effect") != c.uncertain {
			t.Errorf("status %d: uncertain = %t, err = %v", c.status, !c.uncertain, err)
		}
		if c.hint != "" && !strings.Contains(text, c.hint) {
			t.Errorf("status %d: missing hint %q in %v", c.status, c.hint, err)
		}
		if len(changes(calls)) != 1 {
			t.Errorf("status %d: change requests = %d, want exactly 1", c.status, len(changes(calls)))
		}
	}
}

func TestPermissionHintNamesTheRightOfTheTool(t *testing.T) {
	for op, args := range map[string]struct{ right, args string }{
		rowsCreate.ID: {"create", `{"table_id":11,"fields":{}}`},
		rowsDelete.ID: {"delete", `{"table_id":11,"row_id":7}`},
		rowsMove.ID:   {"update", `{"table_id":11,"row_id":7}`},
	} {
		var calls []call
		var bodies []string
		env := newEnvironment(t, &calls, mutationServer(403, bodyCanary, &bodies))
		_, err := env.invokeConfirmed(op, "write", args.args)
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), "check its "+args.right+" right") ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", op, err)
		}
	}
}

func TestUnreadableAnswerAndTransportFailureAreUncertainWithoutRetry(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `<html>`+bodyCanary, &bodies))
	_, err := env.invokeConfirmed(rowsCreate.ID, "write", `{"table_id":11,"fields":{"Name":"x"}}`)
	if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "may have taken effect") ||
		strings.Contains(err.Error(), bodyCanary) || len(changes(calls)) != 1 {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
	for _, failure := range []error{context.DeadlineExceeded, errors.New(bodyCanary)} {
		calls = nil
		env = newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if strings.HasPrefix(r.URL.Path, "/api/database/fields/") {
				return jsonResponse(200, schemaBody), nil
			}
			return nil, failure
		})
		_, err = env.invokeConfirmed(rowsDelete.ID, "write", `{"table_id":11,"row_id":7}`)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
			strings.Contains(err.Error(), bodyCanary) || len(calls) != 1 {
			t.Fatalf("err = %v, calls = %+v", err, calls)
		}
	}
}

func TestBadIdentifiersAreRefused(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"id":1}`, &bodies))
	for op, args := range map[string]string{
		rowsDelete.ID: `{"table_id":11,"row_id":0}`,
		rowsMove.ID:   `{"table_id":11,"row_id":1,"before_id":-1}`,
	} {
		if _, err := env.invokeConfirmed(op, "write", args); err == nil {
			t.Errorf("%s accepted %s", op, args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d", len(calls), *env.reads)
	}
}

func (e *environment) invokeConfirmed(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: []byte(arguments), Confirmed: true,
	})
	return string(response.Result), err
}
