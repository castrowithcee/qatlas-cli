package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/projectpath"
)

// boundLayout is a directory tree with two customer repositories, kunde-a and kunde-b, and a directory
// kunde-ab whose name only starts like kunde-a.
type boundLayout struct {
	base, kundeA, kundeAB, kundeB string
}

func newBoundLayout(t *testing.T) boundLayout {
	t.Helper()
	base := projectpath.Canonical(t.TempDir())
	l := boundLayout{
		base:    base,
		kundeA:  filepath.Join(base, "repos", "kunde-a"),
		kundeAB: filepath.Join(base, "repos", "kunde-ab"),
		kundeB:  filepath.Join(base, "repos", "kunde-b"),
	}
	for _, repo := range []string{l.kundeA, l.kundeAB, l.kundeB} {
		gitRepository(t, repo)
	}
	return l
}

func gitRepository(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdirAll(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// boundCore is a core with the connections kunde-a and kunde-b, each bound to its repository, and open,
// bound to nothing.
func boundCore(t *testing.T, l boundLayout, defaults map[string]string) (*Core, *int) {
	t.Helper()
	core, calls := testCore(t, []string{"kunde-a", "kunde-b", "open"}, defaults, true)
	for name, dir := range map[string]string{"kunde-a": l.kundeA, "kunde-b": l.kundeB} {
		connection := core.all.Connections[name]
		connection.Paths = []string{dir}
		core.all.Connections[name] = connection
	}
	return core, calls
}

func listedConnections(core *Core) []string {
	var names []string
	for _, connection := range core.Connections("", nil).Connections {
		names = append(names, connection.Name)
	}
	return names
}

func TestBoundConnectionsFollowTheProjectsOfTheCall(t *testing.T) {
	l := newBoundLayout(t)
	core, _ := boundCore(t, l, nil)

	// A worktree of kunde-a checked out elsewhere, a repository embedded in kunde-a, and a link to kunde-b.
	private := mkdirAll(t, l.kundeA, ".git", "worktrees", "wt")
	worktree := mkdirAll(t, l.base, "worktrees", "wt")
	for name, content := range map[string]string{
		filepath.Join(private, "commondir"): "../..\n",
		filepath.Join(private, "gitdir"):    filepath.Join(worktree, ".git") + "\n",
		filepath.Join(worktree, ".git"):     "gitdir: " + private + "\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	embedded := filepath.Join(l.kundeA, "vendor", "inner")
	gitRepository(t, embedded)
	link := filepath.Join(l.base, "link-b")
	if err := os.Symlink(l.kundeB, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}

	cases := []struct {
		name     string
		projects []string
		want     []string
	}{
		{"no project", nil, []string{"open"}},
		{"the bound repository", []string{l.kundeA}, []string{"kunde-a", "open"}},
		{"a subdirectory of the bound repository", []string{mkdirAll(t, l.kundeA, "src", "pkg")},
			[]string{"kunde-a", "open"}},
		{"a sibling that only shares the prefix", []string{l.kundeAB}, []string{"open"}},
		{"the parent of a bound path", []string{filepath.Join(l.base, "repos")}, []string{"open"}},
		{"an embedded repository inside the bound one", []string{mkdirAll(t, embedded, "cmd")},
			[]string{"kunde-a", "open"}},
		{"a worktree under another path", []string{mkdirAll(t, worktree, "src")}, []string{"kunde-a", "open"}},
		{"a symbolic link to a bound repository", []string{link}, []string{"kunde-b", "open"}},
		{"several projects", []string{l.kundeA, l.kundeB}, []string{"kunde-a", "kunde-b", "open"}},
	}
	for _, c := range cases {
		core.SetProjects(c.projects)
		if got := listedConnections(core); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: connections = %v, want %v", c.name, got, c.want)
		}
	}

	// A bound path given through a link applies to the directory it names.
	connection := core.all.Connections["kunde-b"]
	connection.Paths = []string{link}
	core.all.Connections["kunde-b"] = connection
	core.SetProjects([]string{l.kundeB})
	if got := listedConnections(core); !reflect.DeepEqual(got, []string{"kunde-b", "open"}) {
		t.Errorf("paths through a link: connections = %v", got)
	}
}

func TestABoundConnectionIsMissingEverywhereOutsideItsProject(t *testing.T) {
	l := newBoundLayout(t)
	core, calls := boundCore(t, l, map[string]string{"fake": "kunde-b"})
	core.SetProjects([]string{mkdirAll(t, l.kundeA, "src")})

	providers := core.Providers().Providers
	if len(providers) != 1 || providers[0].Connections != 2 || providers[0].Configured != 2 {
		t.Errorf("Providers() = %+v, want two usable and two configured connections", providers)
	}
	tools, err := core.Tools(SearchRequest{Provider: "fake"})
	if err != nil || len(tools.Tools) == 0 {
		t.Fatalf("Tools() = %+v, %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Connections != "kunde-a open" {
			t.Errorf("Tools() %s offered by %q, want kunde-a open", tool.ID, tool.Connections)
		}
	}
	searched, err := core.Search(SearchRequest{Query: "page"})
	if err != nil || len(searched.Operations) == 0 {
		t.Fatalf("Search() = %+v, %v", searched, err)
	}
	for _, hit := range searched.Operations {
		if strings.Join(hit.Connections, " ") != "kunde-a open" {
			t.Errorf("Search() %s offered by %v, want kunde-a open", hit.ID, hit.Connections)
		}
	}
	described, err := core.Describe(DescribeRequest{Operation: "fake.pages.get"})
	if err != nil || len(described.Connections) != 2 || described.Connections[0].Name != "kunde-a" ||
		described.Connections[1].Name != "open" {
		t.Errorf("Describe() = %+v, %v", described.Connections, err)
	}

	// Named explicitly, a connection of another project is unknown in every entry point, and neither its
	// path nor any other connection's path is named.
	checkUnknown := func(what string, err error) {
		t.Helper()
		var unknown *capability.UnknownConnectionError
		if !errors.As(err, &unknown) || ErrorCode(err) != "unknown-connection" {
			t.Fatalf("%s = %T %v, want unknown-connection", what, err, err)
		}
		if !strings.Contains(err.Error(), "bound to the paths of other projects") {
			t.Errorf("%s = %q, want the general hint on project paths", what, err)
		}
		if strings.Contains(err.Error(), l.base) {
			t.Errorf("%s = %q names a path", what, err)
		}
	}
	_, err = core.Search(SearchRequest{Connection: "kunde-b"})
	checkUnknown("Search(kunde-b)", err)
	_, err = core.Tools(SearchRequest{Connection: "kunde-b"})
	checkUnknown("Tools(kunde-b)", err)
	_, err = core.Describe(DescribeRequest{Operation: "fake.pages.get", Connection: "kunde-b"})
	checkUnknown("Describe(kunde-b)", err)
	_, err = core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.get", Connection: "kunde-b",
		Arguments: json.RawMessage(`{"id":"1"}`)})
	checkUnknown("Invoke(kunde-b)", err)
	var unknown *capability.UnknownConnectionError
	if _, err := core.Search(SearchRequest{Connection: "kunde-bb"}); !errors.As(err, &unknown) ||
		unknown.Suggestion == "kunde-b" {
		t.Errorf("Search(kunde-bb) = %v, want no suggestion of a connection of another project", err)
	}
	if *calls != 0 {
		t.Fatalf("a refused connection reached the provider: calls = %d", *calls)
	}

	// The default of the other project is no default here, so the candidates decide, and only the ones of
	// this project are candidates.
	_, err = core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.get",
		Arguments: json.RawMessage(`{"id":"1"}`)})
	var ambiguous *ConnectionAmbiguousError
	if !errors.As(err, &ambiguous) || !reflect.DeepEqual(ambiguous.Connections,
		[]ConnectionRef{{Name: "kunde-a"}, {Name: "open"}}) {
		t.Fatalf("Invoke() without connection = %v, want kunde-a and open as the only candidates", err)
	}
	response, err := core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.get",
		Connection: "kunde-a", Arguments: json.RawMessage(`{"id":"1"}`)})
	if err != nil || response.Connection != "kunde-a" || *calls != 1 {
		t.Fatalf("Invoke(kunde-a) = %+v, %v", response, err)
	}

	// Inside its project the default applies again.
	core.SetProjects([]string{l.kundeB})
	response, err = core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.get",
		Arguments: json.RawMessage(`{"id":"1"}`)})
	if err != nil || response.Connection != "kunde-b" {
		t.Fatalf("Invoke() inside kunde-b = %+v, %v, want the default kunde-b", response, err)
	}
}

func TestUnknownConnectionHintOnlyWhereConnectionsAreBound(t *testing.T) {
	core, _ := testCore(t, []string{"open"}, nil, true)
	_, err := core.Search(SearchRequest{Connection: "missing"})
	if err == nil || strings.Contains(err.Error(), "other projects") {
		t.Fatalf("Search(missing) = %v, want no hint on project paths without any bound connection", err)
	}
}

// Without a connection and without a default, the one connection the project sees is chosen, for a reading
// and a changing tool alike; a connection bound to another project never is.
func TestTheOnlyConnectionVisibleInAProjectIsChosen(t *testing.T) {
	l := newBoundLayout(t)
	core, _ := testCore(t, []string{"kunde-a", "kunde-b"}, nil, true)
	for name, dir := range map[string]string{"kunde-a": l.kundeA, "kunde-b": l.kundeB} {
		connection := core.all.Connections[name]
		connection.Paths = []string{dir}
		core.all.Connections[name] = connection
	}
	for project, want := range map[string]string{l.kundeA: "kunde-a", l.kundeB: "kunde-b"} {
		core.SetProjects([]string{project})
		for _, operation := range []string{"fake.pages.get", "fake.pages.delete"} {
			response, err := core.Invoke(context.Background(), InvokeRequest{Operation: operation, Confirmed: true,
				Arguments: json.RawMessage(`{"id":"1"}`)})
			if err != nil || response.Connection != want {
				t.Errorf("Invoke(%s) in %s = %+v, %v, want %s", operation, filepath.Base(project), response, err, want)
			}
		}
	}
	// Both projects at once, as MCP roots may name them, leave a choice.
	core.SetProjects([]string{l.kundeA, l.kundeB})
	if _, err := core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.get",
		Arguments: json.RawMessage(`{"id":"1"}`)}); !errorAs(err, new(ConnectionAmbiguousError)) {
		t.Errorf("Invoke() over two visible connections = %v, want connection-ambiguous", err)
	}
	// Outside every bound project nothing is visible.
	core.SetProjects([]string{l.kundeAB})
	if _, err := core.Invoke(context.Background(), InvokeRequest{Operation: "fake.pages.get",
		Arguments: json.RawMessage(`{"id":"1"}`)}); !errorAs(err, new(ConnectionSelectionError)) {
		t.Errorf("Invoke() with no visible connection = %v, want connection-selection", err)
	}
}
