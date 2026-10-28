package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	releaseverify "github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
)

type fixture struct {
	tag               string
	prerelease, draft bool
	unsigned          bool
	badSignature      bool
}

// multiReleaseServer serves the paginated release list, single releases by tag, and their assets.
func multiReleaseServer(t *testing.T, signer ssh.Signer, fixtures []fixture, pageRequests *int) *httptest.Server {
	t.Helper()
	archive := releaseArchive(t, []byte("new-binary"), []byte("new-manpage"))
	byTag := map[string]fixture{}
	for _, f := range fixtures {
		byTag[f.tag] = f
	}
	var server *httptest.Server
	describe := func(f fixture) map[string]any {
		assets := []map[string]string{
			{"name": assetName(f.tag, "linux", "amd64"), "url": server.URL + "/asset/" + f.tag},
			{"name": "checksums.txt", "url": server.URL + "/checksums/" + f.tag},
		}
		if !f.unsigned {
			assets = append(assets, map[string]string{"name": "checksums.txt.sig", "url": server.URL + "/signature/" + f.tag})
		}
		return map[string]any{"tag_name": f.tag, "prerelease": f.prerelease, "draft": f.draft, "assets": assets}
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/releases":
			if pageRequests != nil {
				*pageRequests++
			}
			perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			out := []map[string]any{}
			for i := (page - 1) * perPage; i < page*perPage && i < len(fixtures); i++ {
				out = append(out, describe(fixtures[i]))
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(path, "/releases/tags/"):
			f, ok := byTag[strings.TrimPrefix(path, "/releases/tags/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(describe(f))
		case strings.HasPrefix(path, "/asset/"):
			_, _ = w.Write(archive)
		case strings.HasPrefix(path, "/checksums/"):
			_, _ = w.Write(checksumsOf(archive, strings.TrimPrefix(path, "/checksums/")))
		case strings.HasPrefix(path, "/signature/"):
			tag := strings.TrimPrefix(path, "/signature/")
			sum := checksumsOf(archive, tag)
			if byTag[tag].badSignature {
				sum = append([]byte("x"), sum...)
			}
			_, _ = w.Write(releasetest.Sign(t, signer, releaseverify.Namespace, "sha512", sum))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func checksumsOf(archive []byte, tag string) []byte {
	sum := sha256.Sum256(archive)
	return []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName(tag, "linux", "amd64")))
}

func tags(entries []Entry) string {
	var out []string
	for _, e := range entries {
		s := e.Version
		if e.Prerelease {
			s += "!"
		}
		if e.Current {
			s += "*"
		}
		if e.Older {
			s += "<"
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func TestListSelectionRule(t *testing.T) {
	signer := releasetest.NewSigner(t)
	var fixtures []fixture
	// Stable v0.30.0 to v0.43.0: fourteen releases, only the ten newest are listed.
	for minor := 43; minor >= 30; minor-- {
		fixtures = append(fixtures, fixture{tag: fmt.Sprintf("v0.%d.0", minor)})
	}
	fixtures = append(fixtures,
		fixture{tag: "v0.44.0-rc.1", prerelease: true},              // newer than the newest stable
		fixture{tag: "v0.42.5-rc.1", prerelease: true},              // older than the newest stable
		fixture{tag: "v0.45.0", unsigned: true},                     // unsigned
		fixture{tag: "v0.45.0-rc.1", prerelease: true, draft: true}, // draft
		fixture{tag: "v0.29.0"},                                     // below the minimum
		fixture{tag: "v0.45.0-beta.1", prerelease: true, unsigned: true},
	)
	server := multiReleaseServer(t, signer, fixtures, nil)
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v0.35.0", SigningKeys: []ssh.PublicKey{signer.PublicKey()}}
	entries, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "v0.44.0-rc.1! v0.43.0 v0.42.0 v0.41.0 v0.40.0 v0.39.0 v0.38.0 v0.37.0 v0.36.0 v0.35.0* v0.34.0<"
	if got := tags(entries); got != want {
		t.Fatalf("List() =\n%s\nwant\n%s", got, want)
	}
}

func TestListMarksRunningVersionOutsideTheTen(t *testing.T) {
	signer := releasetest.NewSigner(t)
	var fixtures []fixture
	for minor := 43; minor >= 30; minor-- {
		fixtures = append(fixtures, fixture{tag: fmt.Sprintf("v0.%d.0", minor)})
	}
	server := multiReleaseServer(t, signer, fixtures, nil)
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v0.31.0"}
	entries, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 11 || !entries[10].Current || entries[10].Version != "v0.31.0" || entries[9].Older {
		t.Fatalf("List() = %s", tags(entries))
	}
	// A running version that is no signed release from the minimum on is not listed.
	client.Version = "v0.29.5"
	entries, err = client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Current || e.Older {
			t.Fatalf("List() = %s", tags(entries))
		}
	}
}

func TestListReadsEveryPage(t *testing.T) {
	old := releasePageSize
	releasePageSize = 3
	t.Cleanup(func() { releasePageSize = old })
	signer := releasetest.NewSigner(t)
	var fixtures []fixture
	for minor := 38; minor >= 30; minor-- {
		fixtures = append(fixtures, fixture{tag: fmt.Sprintf("v0.%d.0", minor)})
	}
	pages := 0
	server := multiReleaseServer(t, signer, fixtures, &pages)
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v0.30.0"}
	entries, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 9 || entries[0].Version != "v0.38.0" || !entries[8].Current {
		t.Fatalf("List() = %s", tags(entries))
	}
	// Nine releases on pages of three: the third page is full, so a fourth (empty) page ends the read.
	if pages != 4 {
		t.Fatalf("read %d pages, want 4", pages)
	}
}

func TestListStopsAtThePageLimit(t *testing.T) {
	old := releasePageSize
	releasePageSize = 1
	t.Cleanup(func() { releasePageSize = old })
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v0.30.0"}})
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client(), Version: "v0.30.0"}
	if _, err := client.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pages != maxReleasePages {
		t.Fatalf("read %d pages, want %d", pages, maxReleasePages)
	}
}

func newTargetClient(t *testing.T, server *httptest.Server, signer ssh.Signer, version string) (*Client, string) {
	t.Helper()
	prefix, executable := installedPrefix(t)
	_ = prefix
	return &Client{
		BaseURL: server.URL, HTTPClient: server.Client(), Version: version,
		GOOS: "linux", GOARCH: "amd64", Executable: executable,
		SigningKeys: []ssh.PublicKey{signer.PublicKey()},
		BeforeReplace: func(_ context.Context, rel Release) error {
			if !rel.Signed {
				return errors.New("unsigned release reached the replacement")
			}
			return nil
		},
	}, executable
}

func TestUpdateToInstallsAPrerelease(t *testing.T) {
	signer := releasetest.NewSigner(t)
	// The prerelease is not the newest stable release, and not in the list's reach either.
	server := multiReleaseServer(t, signer, []fixture{{tag: "v0.40.0"}, {tag: "v0.41.0-rc.1", prerelease: true}}, nil)
	client, executable := newTargetClient(t, server, signer, "v0.35.0")
	target, err := client.Target(context.Background(), "v0.41.0-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	if !target.Prerelease || target.Current {
		t.Fatalf("Target() = %+v", target)
	}
	result, err := client.UpdateTo(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || !result.Signed || result.Latest != "v0.41.0-rc.1" || result.Current != "v0.35.0" || !result.UpdateAvailable {
		t.Fatalf("UpdateTo() = %+v", result)
	}
	assertFile(t, executable, "new-binary")
}

func TestUpdateToAcceptsAReleaseOutsideTheList(t *testing.T) {
	signer := releasetest.NewSigner(t)
	var fixtures []fixture
	for minor := 43; minor >= 30; minor-- {
		fixtures = append(fixtures, fixture{tag: fmt.Sprintf("v0.%d.0", minor)})
	}
	server := multiReleaseServer(t, signer, fixtures, nil)
	client, executable := newTargetClient(t, server, signer, "v0.30.0")
	target, err := client.Target(context.Background(), "v0.32.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateTo(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	assertFile(t, executable, "new-binary")
}

func TestTargetRefusals(t *testing.T) {
	signer := releasetest.NewSigner(t)
	server := multiReleaseServer(t, signer, []fixture{
		{tag: "v0.40.0"}, {tag: "v0.41.0", unsigned: true}, {tag: "v0.29.0"}, {tag: "v0.34.0"},
		{tag: "v0.42.0", draft: true},
	}, nil)
	tests := map[string]struct{ tag, want string }{
		"unsigned":      {"v0.41.0", "not signed"},
		"below minimum": {"v0.29.0", "oldest version"},
		"draft":         {"v0.42.0", "does not exist"},
		"missing":       {"v0.99.0", "look up release"},
		"not a version": {"latest", "not a release version"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client, executable := newTargetClient(t, server, signer, "v0.35.0")
			_, err := client.Target(context.Background(), tt.tag)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Target(%s) error = %v, want %q", tt.tag, err, tt.want)
			}
			if name != "missing" {
				var refused *TargetError
				if !errors.As(err, &refused) {
					t.Errorf("error %T is not a TargetError", err)
				}
			}
			assertFile(t, executable, "old-binary")
		})
	}
}

func TestTargetMarksAnOlderVersionAndUpdateToRefusesIt(t *testing.T) {
	signer := releasetest.NewSigner(t)
	server := multiReleaseServer(t, signer, []fixture{{tag: "v0.34.0"}, {tag: "v0.36.0"}}, nil)
	client, executable := newTargetClient(t, server, signer, "v0.35.0")
	client.BeforeReplace = func(context.Context, Release) error {
		t.Error("BeforeReplace ran although nothing is replaced")
		return nil
	}
	older, err := client.Target(context.Background(), "v0.34.0")
	if err != nil || !older.Older || older.Current {
		t.Fatalf("Target(v0.34.0) = %+v, %v", older, err)
	}
	newer, err := client.Target(context.Background(), "v0.36.0")
	if err != nil || newer.Older {
		t.Fatalf("Target(v0.36.0) = %+v, %v", newer, err)
	}
	var refused *TargetError
	if _, err := client.UpdateTo(context.Background(), older); !errors.As(err, &refused) {
		t.Fatalf("UpdateTo(older) error = %v, want a TargetError", err)
	}
	assertFile(t, executable, "old-binary")
}

func TestTargetOfTheRunningVersionChangesNothing(t *testing.T) {
	signer := releasetest.NewSigner(t)
	server := multiReleaseServer(t, signer, []fixture{{tag: "v0.35.0"}}, nil)
	client, executable := newTargetClient(t, server, signer, "v0.35.0")
	client.BeforeReplace = func(context.Context, Release) error {
		t.Error("BeforeReplace ran although nothing is replaced")
		return nil
	}
	target, err := client.Target(context.Background(), "v0.35.0")
	if err != nil || !target.Current {
		t.Fatalf("Target() = %+v, %v", target, err)
	}
	result, err := client.UpdateTo(context.Background(), target)
	if err != nil || result.Updated || result.UpdateAvailable {
		t.Fatalf("UpdateTo() = %+v, %v", result, err)
	}
	assertFile(t, executable, "old-binary")
}

func TestUpdateToRefusesAnInvalidSignatureWithoutChanges(t *testing.T) {
	signer := releasetest.NewSigner(t)
	server := multiReleaseServer(t, signer, []fixture{{tag: "v0.41.0-rc.1", prerelease: true, badSignature: true}}, nil)
	client, executable := newTargetClient(t, server, signer, "v0.35.0")
	client.BeforeReplace = func(context.Context, Release) error {
		t.Error("BeforeReplace ran although nothing is replaced")
		return nil
	}
	target, err := client.Target(context.Background(), "v0.41.0-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateTo(context.Background(), target); err == nil || !strings.Contains(err.Error(), "signature is invalid") {
		t.Fatalf("UpdateTo() error = %v", err)
	}
	assertFile(t, executable, "old-binary")
}

func TestPrepareVerifiesWithoutInstallingAndInstallPreparedInstalls(t *testing.T) {
	signer := releasetest.NewSigner(t)
	server := multiReleaseServer(t, signer, []fixture{{tag: "v0.34.0"}, {tag: "v0.41.0", badSignature: true}, {tag: "v0.35.0"}}, nil)
	client, executable := newTargetClient(t, server, signer, "v0.35.0")
	var before, after int
	client.BeforeReplace = func(context.Context, Release) error { before++; return nil }
	client.AfterReplace = func(context.Context, Release) { after++ }

	bad, err := client.Target(context.Background(), "v0.41.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Prepare(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "signature is invalid") {
		t.Fatalf("Prepare() with a bad signature error = %v", err)
	}
	current, _ := client.Target(context.Background(), "v0.35.0")
	if _, err := client.Prepare(context.Background(), current); err == nil {
		t.Fatal("Prepare() of the running version succeeded")
	}

	older, err := client.Target(context.Background(), "v0.34.0")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := client.Prepare(context.Background(), older)
	if err != nil || prepared.Version() != "v0.34.0" {
		t.Fatalf("Prepare() = %+v, %v", prepared, err)
	}
	assertFile(t, executable, "old-binary")
	if before != 0 || after != 0 {
		t.Fatalf("hooks ran during Prepare: %d, %d", before, after)
	}
	result, err := client.InstallPrepared(context.Background(), prepared)
	if err != nil || !result.Updated || !result.Signed || result.Latest != "v0.34.0" || result.Current != "v0.35.0" {
		t.Fatalf("InstallPrepared() = %+v, %v", result, err)
	}
	assertFile(t, executable, "new-binary")
	if before != 1 || after != 1 {
		t.Fatalf("hooks = %d, %d", before, after)
	}
}
