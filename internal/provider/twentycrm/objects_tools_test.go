package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func serveSchema(t *testing.T) *int {
	t.Helper()
	reads := 0
	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != schemaPath {
			t.Errorf("unexpected request %s", request.URL.Path)
		}
		reads++
		return jsonResponse(http.StatusOK, schemaBody), nil
	})
	stubLimiter(t, cloudKey)
	return &reads
}

func runObjectsTool(t *testing.T, handler capability.Handler, args string, targets ...string) (json.RawMessage, error) {
	t.Helper()
	red := &redact.Redactor{}
	result, err := handler(context.Background(), targetConnection(targets...), resolver(red), red, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	out, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(out), descriptionCanary) || strings.Contains(string(out), enumCanary) {
		t.Errorf("a description or enum value reached the output: %s", out)
	}
	return out, nil
}

func objectNames(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var list ObjectList
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, object := range list.Objects {
		names = append(names, object.Name)
	}
	return names
}

func TestObjectsListFollowsTheTargets(t *testing.T) {
	reads := serveSchema(t)
	out, err := runObjectsTool(t, invokeObjectsList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectNames(t, out); !reflect.DeepEqual(got, []string{"company", "person", "rocket"}) {
		t.Errorf("without targets = %v", got)
	}
	if strings.Contains(string(out), "workspaceMember") {
		t.Errorf("a system object is listed: %s", out)
	}
	var list ObjectList
	_ = json.Unmarshal(out, &list)
	if list.Objects[1].Plural != "people" || list.Objects[1].FieldCount == 0 {
		t.Errorf("person summary = %+v", list.Objects[1])
	}
	out, err = runObjectsTool(t, invokeObjectsList, `{}`, "object/person")
	if err != nil {
		t.Fatal(err)
	}
	if got := objectNames(t, out); !reflect.DeepEqual(got, []string{"person"}) {
		t.Errorf("with object/person = %v", got)
	}
	if *reads != 2 {
		t.Errorf("schema reads = %d, want one per call", *reads)
	}
}

func TestObjectsGetProjectsReachableFields(t *testing.T) {
	serveSchema(t)
	out, err := runObjectsTool(t, invokeObjectsGet, `{"object":"person"}`)
	if err != nil {
		t.Fatal(err)
	}
	var info ObjectInfo
	if err := json.Unmarshal(out, &info); err != nil {
		t.Fatal(err)
	}
	fields := map[string]FieldInfo{}
	for _, field := range info.Fields {
		fields[field.Name] = field
	}
	name := fields["name"]
	if info.Plural != "people" || name.Type != "object" || !name.Writable || len(name.Subfields) != 2 ||
		name.Subfields[0].Name != "firstName" {
		t.Errorf("person = %+v", info)
	}
	if f := fields["id"]; f.Writable || f.Required {
		t.Errorf("id = %+v", f)
	}
	if f := fields["company"]; f.Relation != "company" {
		t.Errorf("company relation = %+v", f)
	}
	if _, ok := fields["workspaceMember"]; ok {
		t.Error("a relation into a system object is shown")
	}

	serveSchema(t)
	out, err = runObjectsTool(t, invokeObjectsGet, `{"object":"person"}`, "object/person")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"relation"`) || strings.Contains(string(out), `"company"`) {
		t.Errorf("an unreachable relation target is shown: %s", out)
	}
}

func TestObjectsGetRefusesBeforeSecretAccess(t *testing.T) {
	refuse(t)
	for _, tt := range []struct {
		object  string
		targets []string
	}{
		{"workspaceMember", nil},
		{"workspaceMember", []string{"object/person"}},
		{"rocket", []string{"object/person"}},
		{"../x", nil},
	} {
		_, err := invokeObjectsGet(context.Background(), targetConnection(tt.targets...), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(`{"object":"`+tt.object+`"}`))
		if !asInvalidOK(err) {
			t.Errorf("object %q targets %v: err = %v, want invalid-request", tt.object, tt.targets, err)
		}
	}
}

func TestObjectsGetUnknownBoundObjectIsNotFound(t *testing.T) {
	reads := serveSchema(t)
	_, err := runObjectsTool(t, invokeObjectsGet, `{"object":"ghost"}`, "object/ghost")
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("err = %v, want not-found", err)
	}
	if *reads != 1 {
		t.Errorf("schema reads = %d, want 1", *reads)
	}
}

func TestToolGroupsCoverEveryTool(t *testing.T) {
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	if len(metadata.Groups) != 3 {
		t.Fatalf("groups = %+v", metadata.Groups)
	}
	got := map[string]string{}
	for _, tool := range metadata.Tools {
		got[tool.ID] = tool.Group
	}
	if len(got) != 15 {
		t.Fatalf("tools = %v", got)
	}
	for id, group := range got {
		want := companiesGroup
		if strings.HasPrefix(id, Provider+".objects.") {
			want = objectsGroup
		}
		if strings.HasPrefix(id, Provider+".records.") {
			want = recordsGroup
		}
		if group != want {
			t.Errorf("%s group = %q, want %q", id, group, want)
		}
	}
}
