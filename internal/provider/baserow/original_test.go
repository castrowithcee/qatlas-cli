package baserow

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func originalOf(t *testing.T, err error) *provider.Original {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want a failure")
	}
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Original == nil {
		t.Fatalf("err = %v, want a provider error with Original", err)
	}
	return failure.Original
}

func TestReadFailureCarriesBaserowOriginal(t *testing.T) {
	validation := `{"error":"ERROR_REQUEST_BODY_VALIDATION","detail":{"fields":[{"error":"` + bodyCanary +
		`","code":"required"}],"name":[{"error":"bad name","code":"invalid"}]}}`
	cases := []struct {
		status int
		body   string
		want   []provider.OriginalEntry
	}{
		{401, `{"error":"ERROR_TOKEN_INVALID","detail":"` + bodyCanary + `"}`,
			[]provider.OriginalEntry{{Code: "ERROR_TOKEN_INVALID", Message: bodyCanary}}},
		{403, `{"error":"ERROR_NO_PERMISSION_TO_TABLE","detail":"no"}`,
			[]provider.OriginalEntry{{Code: "ERROR_NO_PERMISSION_TO_TABLE", Message: "no"}}},
		{404, `{"error":"ERROR_ROW_DOES_NOT_EXIST","detail":"gone"}`,
			[]provider.OriginalEntry{{Code: "ERROR_ROW_DOES_NOT_EXIST", Message: "gone"}}},
		{400, validation, []provider.OriginalEntry{{Code: "ERROR_REQUEST_BODY_VALIDATION"},
			{Path: "fields", Code: "required", Message: bodyCanary}, {Path: "name", Code: "invalid", Message: "bad name"}}},
		{422, validation, []provider.OriginalEntry{{Code: "ERROR_REQUEST_BODY_VALIDATION"},
			{Path: "fields", Code: "required", Message: bodyCanary}, {Path: "name", Code: "invalid", Message: "bad name"}}},
	}
	for _, c := range cases {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(c.status, c.body), nil
		})
		_, err := env.invoke(rowsGet.ID, "one", `{"table_id":11,"row_id":1}`)
		original := originalOf(t, err)
		if original.Status != c.status || !reflect.DeepEqual(original.Entries, c.want) {
			t.Errorf("status %d: original = %+v, want entries %+v", c.status, original, c.want)
		}
		if strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("status %d: message leaked the provider text: %v", c.status, err)
		}
	}
}

func TestMutationFailureCarriesBaserowOriginal(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500} {
		var calls []call
		var bodies []string
		body := `{"error":"ERROR_X","detail":{"Name":[{"error":"` + bodyCanary + `","code":"blank"}]}}`
		env := newEnvironment(t, &calls, mutationServer(status, body, &bodies))
		_, err := env.invokeConfirmed(rowsUpdate.ID, "write", `{"table_id":11,"row_id":7,"fields":{"Name":"x"}}`)
		original := originalOf(t, err)
		want := []provider.OriginalEntry{{Code: "ERROR_X"}, {Path: "Name", Code: "blank", Message: bodyCanary}}
		if original.Status != status || !reflect.DeepEqual(original.Entries, want) {
			t.Errorf("status %d: original = %+v", status, original)
		}
		if strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("status %d: message leaked the provider text: %v", status, err)
		}
	}
}

func TestMediaFailureKeepsOnlyTheStatus(t *testing.T) {
	for _, status := range []int{302, 403, 404, 429, 500} {
		fake := newFileFake()
		fake.mediaStatus = status
		fake.content = []byte(`{"error":"` + bodyCanary + `"}`)
		serveFiles(t, fake)
		_, err := getWith(t, filesConnection("", ""), `,"index":0`)
		original := originalOf(t, err)
		if original.Status != status || len(original.Entries) != 0 {
			t.Errorf("media %d: original = %+v, want the status only", status, original)
		}
	}
}
