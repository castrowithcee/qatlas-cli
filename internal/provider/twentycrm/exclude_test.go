package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func excludeConnection(excludes []string, targets ...string) *config.Resolved {
	resolved := targetConnection(targets...)
	resolved.Exclude = excludes
	return resolved
}

func excludeMetadata(t *testing.T) config.TargetMetadata {
	t.Helper()
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	return metadata.Target
}

func TestExclusionsAreDeclaredAndValidatedWithoutQuotingTheValue(t *testing.T) {
	target := excludeMetadata(t)
	if !target.Excludes() {
		t.Fatal("the provider does not declare exclusions")
	}
	const canary = "Zz-canary-8841"
	if got := target.CheckExcludes([]string{"object/rocket", "object/person"}, nil); len(got) != 0 {
		t.Errorf("valid exclusions = %+v", got)
	}
	// An exclusion beside targets that matches none of them is valid.
	if got := target.CheckExcludes([]string{"object/rocket"}, []string{"object/person"}); len(got) != 0 {
		t.Errorf("unrelated exclusion = %+v", got)
	}
	for _, bad := range []string{"", "*", "object/*", "person", "object/workspaceMember", "object/" + canary} {
		if got := target.CheckExcludes([]string{bad}, nil); len(got) != 1 {
			t.Errorf("CheckExcludes(%q) = %+v", bad, got)
		}
	}
	if got := target.CheckExcludes([]string{"object/person"}, []string{"object/person"}); len(got) != 1 {
		t.Errorf("exclusion equal to a target = %+v", got)
	}
	// The reader repeats the checks of the core and never quotes a value.
	for _, bad := range []string{"object/" + canary, "object/workspaceMember", canary} {
		_, err := boundScope(excludeConnection([]string{bad}))
		if err == nil || strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), "workspaceMember") {
			t.Errorf("boundScope(%q) = %v", bad, err)
		}
	}
	if _, err := boundScope(excludeConnection([]string{"object/rocket", "object/rocket"})); err == nil {
		t.Error("a duplicate exclusion was accepted")
	}
}

func TestExclusionWinsOverTargetsAndOnlyNarrows(t *testing.T) {
	bound, err := boundScope(excludeConnection([]string{"object/person"}, "object/person", "object/company"))
	if err != nil {
		t.Fatal(err)
	}
	if bound.allows("person") || !bound.allows("company") || bound.allows("rocket") {
		t.Errorf("allows: person %v, company %v, rocket %v", bound.allows("person"), bound.allows("company"),
			bound.allows("rocket"))
	}
	if !bound.bound() {
		t.Error("the targets no longer bind")
	}
	// An exclusion that hits no target is valid and changes nothing.
	other, err := boundScope(excludeConnection([]string{"object/rocket"}, "object/person"))
	if err != nil || !other.allows("person") || other.allows("rocket") ||
		!reflect.DeepEqual(other.objects, []string{"person"}) {
		t.Errorf("scope = %+v, %v", other, err)
	}
	only, err := boundScope(excludeConnection([]string{"object/person"}))
	if err != nil || only.bound() || only.allows("person") || !only.allows("company") || only.allows("workspaceMember") {
		t.Errorf("exclusions only: %+v, %v", only, err)
	}
}

func TestExclusionsOnlyConnectionKeepsTheWorkspaceToolsAndGate(t *testing.T) {
	target := excludeMetadata(t)
	for id := range workspaceTools {
		if reached, _ := target.Reach(id, nil); !reached {
			t.Errorf("%s is not reached without targets", id)
		}
	}
	if err := requireWorkspaceScope(excludeConnection([]string{"object/person"})); err != nil {
		t.Errorf("gate refused a connection with exclusions only: %v", err)
	}
	if err := requireWorkspaceScope(excludeConnection([]string{"object/rocket"}, "object/person")); err == nil {
		t.Error("gate admitted a connection with targets")
	}
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusForbidden, "{}"), nil })
	red := &redact.Redactor{}
	_, err := invokeMetaObjectsList(context.Background(), excludeConnection([]string{"object/person"}), resolver(red), red,
		json.RawMessage(`{}`))
	if err != nil && strings.Contains(err.Error(), "workspace-wide tools") {
		t.Errorf("workspace tool refused: %v", err)
	}
}

func excludeClient(t *testing.T, excludes ...string) *Client {
	t.Helper()
	red := &redact.Redactor{}
	c, err := open(context.Background(), excludeConnection(excludes), resolver(red), red, freeLimiter())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestExcludedObjectIsAbsentFromTheCatalogProjection(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, schemaBody), nil })
	c := excludeClient(t, "object/company")
	list, err := c.ListObjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, object := range list.Objects {
		names = append(names, object.Name)
	}
	if !reflect.DeepEqual(names, []string{"person", "rocket"}) {
		t.Errorf("objects = %v", names)
	}
	_, err = c.GetObject(context.Background(), "company")
	var refusal *provider.InvalidRequestError
	if !asInvalid(err, &refusal) || refusal.Message != errObjectUnavailable {
		t.Errorf("GetObject(company) = %v", err)
	}
	info, err := c.GetObject(context.Background(), "person")
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(info); strings.Contains(string(raw), `"company"`) {
		t.Errorf("a relation into the excluded object stays: %s", raw)
	}
	cat, _ := c.workspaceCatalog(context.Background(), "test")
	if _, err := cat.resolve(c.scope, "company"); err == nil {
		t.Error("resolve() reached an excluded object")
	}
}

func TestExcludedObjectIsRefusedBeforeSecretAccessAndRequests(t *testing.T) {
	refuse(t)
	for id, handler := range companyHandlers() {
		_, err := handler(context.Background(), excludeConnection([]string{"object/company"}), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(`{"id":"`+companyID+`","name":"x"}`))
		var refusal *provider.InvalidRequestError
		if !asInvalid(err, &refusal) || refusal.Message != errObjectUnavailable {
			t.Errorf("%s = %v", id, err)
		}
	}
	for _, handler := range []capability.Handler{invokeObjectsGet, invokeRecordsList, invokeRecordsGet} {
		_, err := handler(context.Background(), excludeConnection([]string{"object/person"}), countingResolver(t),
			&redact.Redactor{}, json.RawMessage(`{"object":"person","id":"`+companyID+`"}`))
		var refusal *provider.InvalidRequestError
		if !asInvalid(err, &refusal) || refusal.Message != errObjectUnavailable {
			t.Errorf("err = %v", err)
		}
	}
}

func TestUnresolvableExclusionFailsTheCallWithoutTheName(t *testing.T) {
	const name = "ghostObjectCanary"
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, schemaBody), nil })
	c := excludeClient(t, "object/"+name, "object/rocket")
	for _, call := range []func() error{
		func() error { _, err := c.ListObjects(context.Background()); return err },
		func() error { _, err := c.GetObject(context.Background(), "person"); return err },
		func() error { _, err := c.recordObject(context.Background(), "test", "person"); return err },
	} {
		err := call()
		if err == nil || strings.Contains(err.Error(), name) {
			t.Errorf("err = %v, want a refusal without the name", err)
		}
	}
	// An empty catalog is what an unusable key gets.
	serve(t, func(*http.Request) (*http.Response, error) { return jsonResponse(http.StatusOK, `{}`), nil })
	if _, err := excludeClient(t, "object/rocket").ListObjects(context.Background()); classOf(err) != provider.ClassAuth {
		t.Errorf("empty catalog = %v", err)
	}
}

func TestConnectionTestReadsNoExcludedObject(t *testing.T) {
	var paths []string
	serve(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == schemaPath {
			return jsonResponse(http.StatusOK, schemaBody), nil
		}
		return jsonResponse(http.StatusOK, `{"data":{},"pageInfo":{"hasNextPage":false}}`), nil
	})
	stubLimiter(t, cloudKey)
	class, err := TestConnection(context.Background(), excludeConnection([]string{"object/company"}), resolver(nil), nil)
	if err != nil || class != provider.ClassOK || !reflect.DeepEqual(paths, []string{schemaPath}) {
		t.Errorf("class = %q, err = %v, paths = %v", class, err, paths)
	}
	// An exclusion elsewhere leaves the company probe in place.
	paths = nil
	class, err = TestConnection(context.Background(), excludeConnection([]string{"object/rocket"}), resolver(nil), nil)
	if err != nil || class != provider.ClassOK || !reflect.DeepEqual(paths, []string{"/rest/companies", schemaPath}) {
		t.Errorf("class = %q, err = %v, paths = %v", class, err, paths)
	}
	// An unresolvable exclusion fails the test closed.
	class, err = TestConnection(context.Background(), excludeConnection([]string{"object/ghost"}), resolver(nil), nil)
	if err == nil && class == provider.ClassOK {
		t.Error("an unresolvable exclusion passed the connection test")
	}
}

func TestWithoutExclusionsTheRequestsStayTheSame(t *testing.T) {
	var paths []string
	serve(t, func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		return jsonResponse(http.StatusOK, schemaBody), nil
	})
	c, _ := client(t)
	for i := 0; i < 2; i++ {
		if _, err := c.ListObjects(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(paths, []string{schemaPath}) {
		t.Errorf("paths = %v", paths)
	}
}

func TestBrowseListIgnoresExclusions(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":[{"id":"`+metaObjID+`","nameSingular":"deal","namePlural":"deals",`+
			`"labelSingular":"Deal"}],"pageInfo":{"hasNextPage":false}}`), nil
	})
	red := &redact.Redactor{}
	page, err := browseList(context.Background(), excludeConnection([]string{"object/ghost"}), resolver(red), red,
		config.TargetListRequest{Kind: "object"})
	if err != nil || len(page.Entries) != 1 {
		t.Errorf("page = %+v, err = %v", page, err)
	}
}
