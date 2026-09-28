package vault

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// tokenVault returns an encrypted vault, unlocked here, that holds the credentials gh-a and gh-b.
func tokenVault(t *testing.T) *Vault {
	t.Helper()
	lowWorkFactor(t)
	v := New(t.TempDir())
	if err := v.Set("gh-a", "token", "synthetic-a", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	if err := v.Set("gh-b", "token", "synthetic-b", nil); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	return v
}

// vorbildScope is the vorbild of the coverage tests: github through gh-a, reading and creating, two tools,
// two targets, bound to base.
func vorbildScope(base string) Scope {
	return Scope{
		Connection: "vorbild", Credential: "gh-a", Provider: "github", Origin: "https://api.github.com",
		Permissions: []string{"read", "create"}, Targets: []string{"repos/o/a", "repos/o/b"},
		Tools: []string{"github.issues.list", "github.issues.create"}, Paths: []string{base},
	}
}

func approveScopes(t *testing.T, v *Vault, scopes ...Scope) {
	t.Helper()
	if err := v.Approve(scopes); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
}

func createToken(t *testing.T, v *Vault, name string, models ...string) Token {
	t.Helper()
	token, err := v.CreateToken(name, models, nil)
	if err != nil {
		t.Fatalf("CreateToken() = %v", err)
	}
	return token
}

// A token is 256 random bits behind a fixed prefix, kept encrypted in tokens.age, shown again on request,
// revoked one at a time, and dropped when encryption is switched off. A name is taken once.
func TestTokenLifecycle(t *testing.T) {
	v := tokenVault(t)
	expires := time.Now().Add(time.Hour)
	token, err := v.CreateToken("kunde-a", []string{"wiki", "gh", "wiki", ""}, &expires)
	if err != nil {
		t.Fatalf("CreateToken() = %v", err)
	}
	if !strings.HasPrefix(token.Value, TokenPrefix) || len(token.Value) != len(TokenPrefix)+43 {
		t.Errorf("token value %q is not the prefix and 256 bits, base64url", token.Value)
	}
	if !slices.Equal(token.Models, []string{"gh", "wiki"}) || token.Expires == nil || token.Created.IsZero() {
		t.Errorf("token = %+v, want sorted unique vorbilder, the expiry, and the creation time", token)
	}
	other := createToken(t, v, "kunde-b", "gh")
	if other.Value == token.Value {
		t.Fatalf("two tokens share a value")
	}
	if _, err := v.CreateToken("kunde-a", []string{"gh"}, nil); !errors.Is(err, ErrTokenExists) {
		t.Errorf("CreateToken() of a taken name = %v, want ErrTokenExists", err)
	}
	if _, err := v.CreateToken("no models", nil, nil); err == nil {
		t.Errorf("CreateToken() with a blank in the name succeeded")
	}
	if _, err := v.CreateToken("none", nil, nil); !errors.Is(err, ErrNoModels) {
		t.Errorf("CreateToken() without a vorbild = %v, want ErrNoModels", err)
	}

	data, err := os.ReadFile(filepath.Join(v.Dir(), tokensFile))
	if err != nil || strings.Contains(string(data), token.Value) || strings.Contains(string(data), "kunde-a") {
		t.Fatalf("tokens.age = %v, want it encrypted", err)
	}
	// Another process with the passphrase reads the same tokens again.
	again := New(filepath.Dir(v.Dir()))
	if _, err := again.Unlock("s3cret-phrase"); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	tokens, err := again.Tokens()
	if err != nil || len(tokens) != 2 || tokens[0].Name != "kunde-a" || tokens[0].Value != token.Value {
		t.Fatalf("Tokens() = %+v, %v, want both tokens, values included", tokens, err)
	}

	if err := again.RevokeToken("kunde-a"); err != nil {
		t.Fatalf("RevokeToken() = %v", err)
	}
	if err := again.RevokeToken("kunde-a"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("RevokeToken() twice = %v, want ErrTokenNotFound", err)
	}
	if tokens, _ := again.Tokens(); len(tokens) != 1 || tokens[0].Name != "kunde-b" {
		t.Errorf("Tokens() after RevokeToken() = %+v, want kunde-b alone", tokens)
	}

	if err := again.Decrypt("s3cret-phrase"); err != nil {
		t.Fatalf("Decrypt() = %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.Dir(), tokensFile)); !os.IsNotExist(err) {
		t.Errorf("tokens.age after Decrypt() = %v, want it removed", err)
	}
	if _, err := again.Tokens(); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("Tokens() of an unencrypted vault = %v, want ErrNotEncrypted", err)
	}
	if _, err := again.CreateToken("kunde-c", []string{"gh"}, nil); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("CreateToken() on an unencrypted vault = %v, want ErrNotEncrypted", err)
	}
}

// Anyone can encrypt a document to the public recipient; a tokens.age the vault did not write itself is
// refused whole, and no token of it approves anything.
func TestForgedTokensAreRefused(t *testing.T) {
	v := tokenVault(t)
	recipient, err := v.Recipient()
	if err != nil {
		t.Fatal(err)
	}
	forged := `{"schema":1,"tokens":[{"name":"forged","value":"qat_forged","models":["vorbild"],` +
		`"created":"2026-01-01T00:00:00Z"}],"mac":"00"}`
	data, err := encryptToRecipient(forged, []byte(recipient))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Dir(), tokensFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Tokens(); !errors.Is(err, ErrTokensTampered) {
		t.Errorf("Tokens() of a forged file = %v, want ErrTokensTampered", err)
	}
	if _, err := v.ApproveWithToken("qat_forged", nil, nil, time.Now()); !errors.Is(err, ErrTokensTampered) {
		t.Errorf("ApproveWithToken() with a forged token = %v, want ErrTokensTampered", err)
	}
}

// Each dimension of a change is checked against the vorbild on its own, and a change that leaves it in any
// one of them stays open whole.
func TestApproveWithTokenCoverage(t *testing.T) {
	v := tokenVault(t)
	base := t.TempDir()
	inside := filepath.Join(base, "kunde-a")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	outsideLink := filepath.Join(base, "elsewhere")
	if err := os.Symlink(t.TempDir(), outsideLink); err != nil {
		t.Fatal(err)
	}
	vorbild := vorbildScope(base)
	approveScopes(t, v, vorbild)
	token := createToken(t, v, "agent", "vorbild")

	unbound := vorbild
	unbound.Connection, unbound.Tools, unbound.Targets, unbound.Paths = "open-vorbild", nil, nil, nil

	cases := []struct {
		name   string
		change func(*Scope)
		gaps   []string
	}{
		{"same reach", func(*Scope) {}, nil},
		{"narrower everywhere", func(s *Scope) {
			s.Permissions, s.Tools, s.Targets, s.Paths = []string{"read"}, []string{"github.issues.list"},
				[]string{"repos/o/b"}, []string{inside}
		}, nil},
		{"no tools at all", func(s *Scope) { s.Tools = []string{} }, nil},
		{"other provider", func(s *Scope) { s.Provider = "gitlab" }, []string{GapService}},
		{"other endpoint", func(s *Scope) { s.Origin = "https://github.example.test/api" }, []string{GapService}},
		{"other credential", func(s *Scope) { s.Credential = "gh-b" }, []string{GapCredential}},
		{"more permissions", func(s *Scope) { s.Permissions = []string{"read", "delete"} }, []string{GapPermissions}},
		{"tool outside the list", func(s *Scope) { s.Tools = []string{"github.repos.delete"} }, []string{GapTools}},
		{"no tools list", func(s *Scope) { s.Tools = nil }, []string{GapTools}},
		{"target outside", func(s *Scope) { s.Targets = []string{"repos/o/c"} }, []string{GapTargets}},
		{"no targets", func(s *Scope) { s.Targets = nil }, []string{GapTargets}},
		{"path outside", func(s *Scope) { s.Paths = []string{t.TempDir()} }, []string{GapPaths}},
		{"path through a link out", func(s *Scope) { s.Paths = []string{outsideLink} }, []string{GapPaths}},
		{"no paths", func(s *Scope) { s.Paths = nil }, []string{GapPaths}},
		{"partly outside", func(s *Scope) {
			s.Permissions, s.Targets = []string{"read"}, []string{"repos/o/a", "repos/o/z"}
		}, []string{GapTargets}},
		{"outside twice", func(s *Scope) {
			s.Permissions, s.Tools = []string{"read", "update"}, []string{"github.issues.list", "github.x"}
		}, []string{GapPermissions, GapTools}},
	}
	for i, tc := range cases {
		change := vorbild
		change.Connection = "agent-" + strings.ReplaceAll(tc.name, " ", "-")
		tc.change(&change)
		result, err := v.ApproveWithToken(token.Value, []Scope{vorbild, change}, []string{change.Connection},
			time.Now())
		if err != nil {
			t.Fatalf("%s: ApproveWithToken() = %v", tc.name, err)
		}
		if result.Token != "agent" || len(result.Changes) != 1 {
			t.Fatalf("%s: result = %+v, want one change decided by agent", tc.name, result)
		}
		got := result.Changes[0]
		if got.Kind != ChangeCreate || got.Approved != (tc.gaps == nil) || !slices.Equal(got.Gaps, tc.gaps) {
			t.Errorf("%d %s: change = %+v, want gaps %v", i, tc.name, got, tc.gaps)
		}
		if err := v.CheckApproval(change); (err == nil) != (tc.gaps == nil) {
			t.Errorf("%s: CheckApproval() = %v, want approved only when covered", tc.name, err)
		}
	}

	// A vorbild without tools, targets, and paths admits any of them, within its service, credential, and
	// permissions.
	approveScopes(t, v, unbound)
	wide := createToken(t, v, "wide", "open-vorbild")
	change := unbound
	change.Connection, change.Tools, change.Targets, change.Paths = "wide-change", []string{"github.x"},
		[]string{"repos/any/where"}, []string{t.TempDir()}
	result, err := v.ApproveWithToken(wide.Value, []Scope{vorbild, unbound, change}, []string{"wide-change"}, time.Now())
	if err != nil || len(result.Changes) != 1 || !result.Changes[0].Approved {
		t.Fatalf("ApproveWithToken() under an open vorbild = %+v, %v, want it approved", result, err)
	}
	change.Permissions = []string{"read", "execute"}
	result, _ = v.ApproveWithToken(wide.Value, []Scope{vorbild, unbound, change}, []string{"wide-change"}, time.Now())
	if len(result.Changes) != 1 || result.Changes[0].Kind != ChangeUpdate ||
		!slices.Equal(result.Changes[0].Gaps, []string{GapPermissions}) {
		t.Errorf("an update beyond the permissions = %+v, want it open for its permissions", result)
	}
}

// A vorbild is never changed or deleted with a token, counts as it is approved rather than as it is
// configured, and covers nothing once it is renamed, deleted, or reads a credential stored anew. A covered
// delete removes the approval.
func TestApproveWithTokenVorbildRules(t *testing.T) {
	v := tokenVault(t)
	vorbild := vorbildScope(t.TempDir())
	vorbild.Paths = nil
	covered := vorbild
	covered.Connection, covered.Permissions = "covered", []string{"read"}
	approveScopes(t, v, vorbild, covered)
	token := createToken(t, v, "agent", "vorbild")

	// The vorbild widened by hand and still open: the token sees it as approved.
	widened := vorbild
	widened.Permissions = []string{"read", "create", "delete"}
	wants := covered
	wants.Connection, wants.Permissions = "wants-delete", []string{"delete"}
	result, err := v.ApproveWithToken(token.Value, []Scope{widened, covered, wants}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	decided := map[string]TokenChange{}
	for _, change := range result.Changes {
		decided[change.Connection] = change
	}
	if c := decided["vorbild"]; c.Approved || !slices.Equal(c.Gaps, []string{GapVorbild}) {
		t.Errorf("a change of the vorbild itself = %+v, want it open as a vorbild", c)
	}
	if c := decided["wants-delete"]; c.Approved || !slices.Equal(c.Gaps, []string{GapPermissions}) {
		t.Errorf("a change within the widened, unapproved vorbild = %+v, want it open", c)
	}

	// A covered connection deleted from the configuration loses its approval.
	result, err = v.ApproveWithToken(token.Value, []Scope{vorbild}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Connection != "covered" ||
		result.Changes[0].Kind != ChangeDelete || !result.Changes[0].Approved {
		t.Errorf("deleting a covered connection = %+v, want it approved", result.Changes)
	}
	if approvals, _ := v.Approvals(); len(approvals) != 1 {
		t.Errorf("approvals after a covered delete = %v, want the vorbild's alone", approvals)
	}

	// Renamed: the vorbild is gone under its name, so it covers nothing, and neither its delete nor the
	// connection under its new name is approved.
	renamed := vorbild
	renamed.Connection = "vorbild-renamed"
	result, _ = v.ApproveWithToken(token.Value, []Scope{renamed}, nil, time.Now())
	if !slices.Equal(result.Lapsed, []string{"vorbild"}) || len(result.Changes) != 2 {
		t.Fatalf("after a rename = %+v, want the vorbild lapsed and two open changes", result)
	}
	for _, c := range result.Changes {
		want := map[string]string{"vorbild": GapVorbild, "vorbild-renamed": GapNoVorbild}[c.Connection]
		if c.Approved || !slices.Equal(c.Gaps, []string{want}) {
			t.Errorf("after a rename, %s = %+v, want it open for %s", c.Connection, c, want)
		}
	}

	// The credential stored anew: the vorbild's approval reads an entry that is gone.
	if _, err := v.Delete("gh-a", "token", nil); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("gh-a", "token", "synthetic-a-2", nil); err != nil {
		t.Fatal(err)
	}
	result, _ = v.ApproveWithToken(token.Value, []Scope{vorbild, covered}, nil, time.Now())
	if !slices.Equal(result.Lapsed, []string{"vorbild"}) {
		t.Errorf("after the credential was stored anew = %+v, want the vorbild lapsed", result)
	}
	for _, c := range result.Changes {
		if c.Approved {
			t.Errorf("after the credential was stored anew, %s was approved", c.Connection)
		}
	}
}

// An unknown, revoked, or expired token approves nothing and writes nothing; an expired one is named.
func TestApproveWithTokenRefusesInvalidTokens(t *testing.T) {
	v := tokenVault(t)
	vorbild := vorbildScope(t.TempDir())
	approveScopes(t, v, vorbild)
	change := vorbild
	change.Connection = "change"
	current := []Scope{vorbild, change}

	past := time.Now().Add(time.Hour)
	expiring, err := v.CreateToken("expiring", []string{"vorbild"}, &past)
	if err != nil {
		t.Fatal(err)
	}
	result, err := v.ApproveWithToken(expiring.Value, current, nil, time.Now().Add(2*time.Hour))
	if !errors.Is(err, ErrTokenExpired) || result.Token != "expiring" || len(result.Changes) != 0 {
		t.Errorf("an expired token = %+v, %v, want ErrTokenExpired naming it", result, err)
	}
	if _, err := v.ApproveWithToken("qat_unknown", current, nil, time.Now()); !errors.Is(err, ErrTokenUnknown) {
		t.Errorf("an unknown token = %v, want ErrTokenUnknown", err)
	}
	if _, err := v.ApproveWithToken("", current, nil, time.Now()); !errors.Is(err, ErrTokenUnknown) {
		t.Errorf("an empty token = %v, want ErrTokenUnknown", err)
	}
	revoked := createToken(t, v, "revoked", "vorbild")
	if err := v.RevokeToken("revoked"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.ApproveWithToken(revoked.Value, current, nil, time.Now()); !errors.Is(err, ErrTokenUnknown) {
		t.Errorf("a revoked token = %v, want ErrTokenUnknown", err)
	}
	if err := v.CheckApproval(change); !errors.Is(err, ErrApprovalRequired) {
		t.Errorf("CheckApproval() after refused tokens = %v, want the change still open", err)
	}
	// Still valid a moment before its expiry.
	if result, err := v.ApproveWithToken(expiring.Value, current, nil, time.Now()); err != nil ||
		len(result.Changes) != 1 || !result.Changes[0].Approved {
		t.Errorf("a token before its expiry = %+v, %v, want the change approved", result, err)
	}
}

// OpenWithKey opens the vault a vault process holds with its key alone, and a token approval through it is
// what the passphrase would find afterwards.
func TestOpenWithKeyApprovesForTheVaultOnDisk(t *testing.T) {
	v := tokenVault(t)
	vorbild := vorbildScope(t.TempDir())
	approveScopes(t, v, vorbild)
	token := createToken(t, v, "agent", "vorbild")
	change := vorbild
	change.Connection = "change"

	opened, err := OpenWithKey(v.Dir(), v.identity.key)
	if err != nil {
		t.Fatalf("OpenWithKey() = %v", err)
	}
	if result, err := opened.ApproveWithToken(token.Value, []Scope{vorbild, change}, nil, time.Now()); err != nil ||
		len(result.Changes) != 1 || !result.Changes[0].Approved {
		t.Fatalf("ApproveWithToken() = %+v, %v", result, err)
	}
	again := New(filepath.Dir(v.Dir()))
	if _, err := again.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	if err := again.CheckApproval(change); err != nil {
		t.Errorf("CheckApproval() after a token approval = %v, want it approved", err)
	}
	other, _ := generateIdentity()
	if _, err := OpenWithKey(v.Dir(), other.key); err == nil {
		t.Errorf("OpenWithKey() with another key succeeded")
	}
}

// The token check runs in constant time over every stored token and is safe for concurrent use.
func TestMatchTokenConcurrently(t *testing.T) {
	tokens := []Token{{Name: "a", Value: "qat_a"}, {Name: "b", Value: "qat_b"}, {Name: "c", Value: "qat_c"}}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, want := range tokens {
				if got, ok := matchToken(tokens, want.Value); !ok || got.Name != want.Name {
					t.Errorf("matchToken(%s) = %+v, %v", want.Name, got, ok)
				}
			}
			if _, ok := matchToken(tokens, "qat_"); ok {
				t.Errorf("matchToken() of a prefix matched")
			}
		}()
	}
	wg.Wait()
}

// The token of a run comes from the environment first, else from the nearest agent.env at or above the
// working directory, which alone decides, else from the home directory.
func TestFindAgentTokenOrder(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "repos", "kunde-a")
	deep := filepath.Join(project, "src", "pkg")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(dir, content string) string {
		t.Helper()
		path := filepath.Join(dir, ".qatlas", "local", "agent.env")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	find := func(getenv func(string) string) (string, string) {
		t.Helper()
		value, source, err := FindAgentToken(deep, home, getenv)
		if err != nil {
			t.Fatalf("FindAgentToken() = %v", err)
		}
		return value, source
	}

	if value, source := find(env(nil)); value != "" || source != "" {
		t.Errorf("without any token = %q from %q, want none", value, source)
	}
	homeFile := write(home, "QATLAS_AGENT_TOKEN=qat_home\n")
	if value, source := find(env(nil)); value != "qat_home" || source != homeFile {
		t.Errorf("with the home file alone = %q from %q, want the home token", value, source)
	}
	upper := write(filepath.Join(root, "repos"), "# comment\nOTHER=x\n QATLAS_AGENT_TOKEN = \"qat_repos\" \n")
	if value, source := find(env(nil)); value != "qat_repos" || source != upper {
		t.Errorf("with a file above = %q from %q, want the nearer one", value, source)
	}
	nearest := write(project, "OTHER=x\n")
	if value, source := find(env(nil)); value != "" || source != nearest {
		t.Errorf("with a nearest file without the line = %q from %q, want none, decided there", value, source)
	}
	write(project, "QATLAS_AGENT_TOKEN=qat_project\n")
	if value, _ := find(env(nil)); value != "qat_project" {
		t.Errorf("with the project file = %q, want the project token", value)
	}
	if value, source := find(env(map[string]string{AgentTokenEnv: "qat_env"})); value != "qat_env" ||
		source != AgentTokenEnv {
		t.Errorf("with the environment = %q from %q, want the environment first", value, source)
	}
	if value, _ := find(env(map[string]string{AgentTokenEnv: "  "})); value != "qat_project" {
		t.Errorf("with a blank environment variable = %q, want the files", value)
	}
}
