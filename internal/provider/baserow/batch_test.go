package baserow

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func TestBatchDescriptorsCarryFullRisk(t *testing.T) {
	for _, d := range []capability.Descriptor{rowsBatchCreate, rowsBatchUpdate, rowsBatchDelete} {
		if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" ||
			d.Risk.Effect == "" || d.Risk.Idempotency == "" || d.RequiresToolAllowList != (d.ID == rowsBatchDelete.ID) {
			t.Errorf("%s risk = %+v allowList=%t", d.ID, d.Risk, d.RequiresToolAllowList)
		}
	}
}

func TestBatchCreateAndUpdateSendOneRequestAndMask(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"items":[{"id":9,"order":"1","Name":"A",`+
		`"Lieferant":[{"id":4,"value":"secret-supplier"}]},{"id":10,"order":"2","Name":"B"}]}`, &bodies))
	result, err := env.invokeConfirmed(rowsBatchCreate.ID, "write", `{"table_id":11,"rows":[{"Name":"A"},{"Name":"B"}]}`)
	if err != nil || strings.Contains(result, "secret-supplier") || !strings.Contains(result, `"count":2`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	got := changes(calls)
	if len(got) != 1 || got[0].method != http.MethodPost || got[0].path != "/api/database/rows/table/11/batch/" ||
		got[0].query.Get("user_field_names") != "true" {
		t.Fatalf("calls = %+v", calls)
	}
	if bodies[0] != `{"items":[{"Name":"A"},{"Name":"B"}]}` {
		t.Fatalf("body = %s", bodies[0])
	}
	calls, bodies = nil, nil
	result, err = env.invokeConfirmed(rowsBatchUpdate.ID, "write",
		`{"table_id":11,"rows":[{"row_id":9,"fields":{"Name":"A"}},{"row_id":10,"fields":{"Name":"B"}}]}`)
	got = changes(calls)
	if err != nil || len(got) != 1 || got[0].method != http.MethodPatch || got[0].path != "/api/database/rows/table/11/batch/" ||
		bodies[0] != `{"items":[{"Name":"A","id":9},{"Name":"B","id":10}]}` {
		t.Fatalf("update = %s, %v, calls = %+v, bodies = %v", result, err, calls, bodies)
	}
}

func TestBatchDeleteSendsOneRequestWithoutReadingFields(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(204, ``, &bodies))
	result, err := env.invokeConfirmed(rowsBatchDelete.ID, "write", `{"table_id":11,"row_ids":[7,8]}`)
	if err != nil || result != `{"count":2,"deleted":true}` || len(calls) != 1 || calls[0].method != http.MethodPost ||
		calls[0].path != "/api/database/rows/table/11/batch-delete/" || bodies[0] != `{"items":[7,8]}` {
		t.Fatalf("result = %s, %v, calls = %+v, bodies = %v", result, err, calls, bodies)
	}
	if _, err := env.invokeConfirmed(rowsBatchDelete.ID, "writenodelete", `{"table_id":11,"row_ids":[7]}`); err == nil {
		t.Fatal("batchdelete ran without the tool allow-list")
	}
}

func TestBatchLimitsAndRefusalsHappenBeforeIO(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"items":[]}`, &bodies))
	many := strings.TrimSuffix(strings.Repeat(`{"Name":"x"},`, 201), ",")
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	big := `"` + strings.Repeat("a", 6000) + `"`
	bigRows := strings.TrimSuffix(strings.Repeat(`{"Name":`+big+`},`, 199), ",")
	for _, c := range []struct{ op, args string }{
		{rowsBatchCreate.ID, `{"table_id":11,"rows":[` + many + `]}`},
		{rowsBatchCreate.ID, `{"table_id":11,"rows":[]}`},
		{rowsBatchCreate.ID, `{"table_id":11,"rows":[` + bigRows + `,{"Name":"` + strings.Repeat("b", 1<<20) + `"}]}`},
		{rowsBatchCreate.ID, `{"table_id":99,"rows":[{"Name":"x"}]}`},
		{rowsBatchUpdate.ID, `{"table_id":11,"rows":[{"row_id":1,"fields":{"Name":"a"}},{"row_id":1,"fields":{"Name":"b"}}]}`},
		{rowsBatchUpdate.ID, `{"table_id":11,"rows":[{"row_id":1,"fields":{"id":5}}]}`},
		{rowsBatchUpdate.ID, `{"table_id":11,"rows":[{"row_id":1,"fields":{}}]}`},
		{rowsBatchDelete.ID, `{"table_id":11,"row_ids":[` + strings.Join(ids, ",") + `]}`},
		{rowsBatchDelete.ID, `{"table_id":11,"row_ids":[3,3]}`},
		{rowsBatchDelete.ID, `{"table_id":11,"row_ids":[0]}`},
		{rowsBatchDelete.ID, `{"table_id":99,"row_ids":[1]}`},
	} {
		if _, err := env.invokeConfirmed(c.op, "write", c.args); err == nil {
			t.Errorf("%s accepted %.60s", c.op, c.args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestBatchSchemaRefusalsSendNoChange(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"items":[]}`, &bodies))
	for _, rows := range []string{`[{"Name":"a"},{"Summe":1}]`, `[{"Nope":1}]`, `[{"Lieferant":[1]}]`} {
		if _, err := env.invokeConfirmed(rowsBatchCreate.ID, "write", `{"table_id":11,"rows":`+rows+`}`); err == nil {
			t.Errorf("accepted %s", rows)
		}
	}
	if _, err := env.invokeConfirmed(rowsBatchUpdate.ID, "write",
		`{"table_id":11,"rows":[{"row_id":9,"fields":{"Lieferant":[1]}}]}`); err == nil {
		t.Error("batch update accepted a link to a foreign table")
	}
	if len(changes(calls)) != 0 {
		t.Fatalf("changes = %+v", changes(calls))
	}
}

func TestBatchUncertainFailuresAreNotRepeated(t *testing.T) {
	for _, status := range []int{500, 200} {
		var calls []call
		var bodies []string
		answer := bodyCanary
		env := newEnvironment(t, &calls, mutationServer(status, answer, &bodies))
		_, err := env.invokeConfirmed(rowsBatchCreate.ID, "write", `{"table_id":11,"rows":[{"Name":"a"}]}`)
		if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
			strings.Contains(err.Error(), bodyCanary) || len(changes(calls)) != 1 {
			t.Fatalf("status %d: err = %v, calls = %+v", status, err, calls)
		}
	}
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, mutationServer(200, `{"items":[{"id":1}]}`, &bodies))
	_, err := env.invokeConfirmed(rowsBatchCreate.ID, "write", `{"table_id":11,"rows":[{"Name":"a"},{"Name":"b"}]}`)
	if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "may have taken effect") {
		t.Fatalf("count mismatch: %v", err)
	}
}

func TestBatchPermissionHintNamesTheRight(t *testing.T) {
	for op, args := range map[string]struct{ right, args string }{
		rowsBatchCreate.ID: {"create", `{"table_id":11,"rows":[{"Name":"a"}]}`},
		rowsBatchUpdate.ID: {"update", `{"table_id":11,"rows":[{"row_id":1,"fields":{"Name":"a"}}]}`},
		rowsBatchDelete.ID: {"delete", `{"table_id":11,"row_ids":[1]}`},
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
