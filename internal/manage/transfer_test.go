package manage

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	idWikiSvc  = "11111111111111111111111111111111"
	idWikiConn = "22222222222222222222222222222222"
	idWikiCred = "33333333333333333333333333333333"
	idOther    = "44444444444444444444444444444444"
)

// transferSource has two connections that need one service and credential each, a forward credential, an
// unrelated service and credential, defaults and a provider note.
const transferSource = `version: 3
services:
    wiki:
        id: ` + idWikiSvc + `
        provider: docs
        base_url: https://wiki.example.test
    mail-svc:
        provider: mail
        base_url: https://mail.example.test
    spare:
        provider: mail
        base_url: https://spare.example.test
credentials:
    wiki-env:
        id: ` + idWikiCred + `
        provider: docs
        type: env
        values:
            key-id: WIKI_KEY_ID
            key-secret: WIKI_KEY_SECRET
    mail-key:
        provider: mail
        type: keyring
    payload:
        type: vault
        forward: true
        fields:
            - user
        description: login for the form
    spare-key:
        provider: mail
        type: vault
connections:
    wiki:
        id: ` + idWikiConn + `
        service: wiki
        credential: wiki-env
        effects:
            - read
        forward_secrets:
            - payload
    mail:
        service: mail-svc
        credential: mail-key
defaults:
    connections:
        docs: wiki
        mail: mail
    secret_store: vault
provider_notes:
    docs: the wiki
`

type transferWorld struct {
	t       *testing.T
	dir     string
	path    string
	store   *config.Store
	svc     *Service
	secrets *fakeSecrets
}

// newTransferWorld is an installation whose configuration file holds text; "" leaves it without one.
func newTransferWorld(t *testing.T, text string) *transferWorld {
	t.Helper()
	dir := t.TempDir()
	w := &transferWorld{t: t, dir: dir, path: filepath.Join(dir, "config.yaml"), secrets: newFakeSecrets()}
	if text != "" {
		if err := os.WriteFile(w.path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.store = config.NewStore(w.path, setupCatalog{})
	w.svc = New(w.store, w.secrets, connlog.SurfaceCLI, &redact.Redactor{}).WithVaultProcessSupport(false)
	return w
}

func (w *transferWorld) raw() []byte {
	w.t.Helper()
	data, err := os.ReadFile(w.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		w.t.Fatal(err)
	}
	return data
}

func (w *transferWorld) load() *config.Config {
	w.t.Helper()
	cfg, err := w.store.Load()
	if err != nil {
		w.t.Fatal(err)
	}
	return cfg
}

// exported exports sel from a world over transferSource and returns the file path.
func exported(t *testing.T, sel ExportSelection) (string, *ExportResult) {
	t.Helper()
	src := newTransferWorld(t, transferSource)
	out := filepath.Join(t.TempDir(), "export.yaml")
	res, err := src.svc.ExportConfig(sel, out)
	if err != nil {
		t.Fatalf("ExportConfig() = %v", err)
	}
	return out, res
}

func mustPlan(t *testing.T, w *transferWorld, file string, mode ImportMode) *ImportPlan {
	t.Helper()
	plan, err := w.svc.PlanImport(file, mode)
	if err != nil {
		t.Fatalf("PlanImport(%s) = %v", mode, err)
	}
	return plan
}

func entry(plan *ImportPlan, section, name string) ImportEntry {
	for _, e := range plan.Entries {
		if e.Section == section && e.Name == name {
			return e
		}
	}
	return ImportEntry{}
}

func TestExportPullsDependenciesAndNamesThem(t *testing.T) {
	path, res := exported(t, ExportSelection{Connections: []string{"wiki"}})
	if !reflect.DeepEqual(res.Connections, []string{"wiki"}) ||
		!reflect.DeepEqual(res.Services, []string{"wiki"}) ||
		!reflect.DeepEqual(res.Credentials, []string{"payload", "wiki-env"}) {
		t.Fatalf("result = %+v", res)
	}
	want := []ExportDependency{
		{Kind: "credential", Name: "payload", For: []string{"wiki"}},
		{Kind: "credential", Name: "wiki-env", For: []string{"wiki"}},
		{Kind: "service", Name: "wiki", For: []string{"wiki"}},
	}
	if !reflect.DeepEqual(res.Dependencies, want) {
		t.Errorf("Dependencies = %+v, want %+v", res.Dependencies, want)
	}
	if !reflect.DeepEqual(res.SecretsNotIncluded, []string{"payload"}) {
		t.Errorf("SecretsNotIncluded = %v, want only the vault credential", res.SecretsNotIncluded)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, absent := range []string{"mail-svc", "spare", "defaults:", "provider_notes:", "logs:", "vault:"} {
		if strings.Contains(text, absent) {
			t.Errorf("export contains %q:\n%s", absent, text)
		}
	}
	if !strings.HasPrefix(text, "format: "+TransferFormat+"\nformat_version: 1\n") {
		t.Errorf("export does not start with the format marker:\n%s", text)
	}
}

func TestExportExplicitEntriesAreNoDependencies(t *testing.T) {
	_, res := exported(t, ExportSelection{Services: []string{"wiki"}, Credentials: []string{"wiki-env"}, Connections: []string{"wiki"}})
	for _, d := range res.Dependencies {
		if d.Name == "wiki" || d.Name == "wiki-env" {
			t.Errorf("%+v is selected, not a dependency", d)
		}
	}
}

func TestExportAllAndOptionalSections(t *testing.T) {
	path, res := exported(t, ExportSelection{All: true})
	if len(res.Services) != 3 || len(res.Credentials) != 4 || len(res.Connections) != 2 || len(res.Dependencies) != 0 {
		t.Errorf("result = %+v", res)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "defaults:") || strings.Contains(string(data), "provider_notes:") {
		t.Errorf("all without the options took defaults or notes:\n%s", data)
	}

	path, res = exported(t, ExportSelection{All: true, Defaults: true, ProviderNotes: true})
	data, _ = os.ReadFile(path)
	for _, want := range []string{"defaults:", "secret_store: vault", "provider_notes:", "docs: the wiki"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("export lacks %q:\n%s", want, data)
		}
	}
	if !reflect.DeepEqual(res.Defaults, []string{"docs", "mail"}) || !reflect.DeepEqual(res.ProviderNotes, []string{"docs"}) {
		t.Errorf("result = %+v", res)
	}
}

func TestExportDefaultsLimitedToSelectedConnections(t *testing.T) {
	path, res := exported(t, ExportSelection{Connections: []string{"wiki"}, Defaults: true})
	if !reflect.DeepEqual(res.Defaults, []string{"docs"}) || !reflect.DeepEqual(res.DefaultsOmitted, []string{"mail"}) {
		t.Errorf("Defaults = %v, omitted = %v", res.Defaults, res.DefaultsOmitted)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "mail:") && strings.Contains(string(data), "connections:\n        mail") {
		t.Errorf("a default for an unexported connection was written:\n%s", data)
	}
	// The file must import on its own.
	dst := newTransferWorld(t, "")
	mustPlan(t, dst, path, ImportModeCreate)
}

func TestExportUnknownNamesAndEmptySelection(t *testing.T) {
	src := newTransferWorld(t, transferSource)
	_, err := src.svc.ExportConfig(ExportSelection{Connections: []string{"nope"}, Services: []string{"also-nope"}}, "")
	var nt *config.NotThereError
	if !errors.As(err, &nt) || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), `"also-nope"`) {
		t.Errorf("unknown names = %v, want both named", err)
	}
	if _, err := src.svc.ExportConfig(ExportSelection{}, ""); err == nil {
		t.Error("an empty selection was accepted")
	}
	if _, err := src.svc.ExportConfig(ExportSelection{All: true, Services: []string{"wiki"}}, ""); err == nil {
		t.Error("all together with names was accepted")
	}
}

func TestExportPreviewWritesNothing(t *testing.T) {
	src := newTransferWorld(t, transferSource)
	before := src.raw()
	if _, err := src.svc.ExportConfig(ExportSelection{All: true}, ""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, src.raw()) {
		t.Error("the preview changed the configuration")
	}
	entries, _ := os.ReadDir(src.dir)
	for _, e := range entries {
		if e.Name() != "config.yaml" {
			t.Errorf("the preview left %s", e.Name())
		}
	}
}

func TestExportFileModeAndNoOverwrite(t *testing.T) {
	src := newTransferWorld(t, transferSource)
	out := filepath.Join(t.TempDir(), "export.yaml")
	if _, err := src.svc.ExportConfig(ExportSelection{All: true}, out); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", info.Mode().Perm())
		}
	}
	first, _ := os.ReadFile(out)
	_, err := src.svc.ExportConfig(ExportSelection{Connections: []string{"mail"}}, out)
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("second export = %v, want an existing-file error", err)
	}
	if again, _ := os.ReadFile(out); !bytes.Equal(first, again) {
		t.Error("an existing target file was changed")
	}
	// A dangling link is no free name either.
	link := filepath.Join(t.TempDir(), "link.yaml")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), link); err == nil {
		if _, err := src.svc.ExportConfig(ExportSelection{All: true}, link); !errors.Is(err, fs.ErrExist) {
			t.Errorf("export through a link = %v, want an existing-file error", err)
		}
	}
}

func TestExportHoldsNoSecretValues(t *testing.T) {
	src := newTransferWorld(t, transferSource)
	const (
		keyringValue = "keyring-secret-value-7f3a"
		vaultValue   = "vault-secret-value-91bc"
		passphrase   = "export-test-passphrase-55"
	)
	src.secrets.entries["mail-key.api-key"] = keyringValue
	src.secrets.entries["payload.user"] = keyringValue + "-payload"
	v := vault.New(src.dir)
	offer := func(string) (string, error) { return passphrase, nil }
	if err := v.Set("spare-key", "api-key", vaultValue, offer); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Unlock(passphrase); err != nil {
		t.Fatal(err)
	}
	token, err := v.CreateToken("helper", []string{"wiki"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIKI_KEY_ID", "env-secret-value-id-1d2e")
	t.Setenv("WIKI_KEY_SECRET", "env-secret-value-key-3c4d")

	out := filepath.Join(t.TempDir(), "export.yaml")
	if _, err := src.svc.ExportConfig(ExportSelection{All: true, Defaults: true, ProviderNotes: true}, out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	for _, secret := range []string{keyringValue, vaultValue, passphrase, token.Value, "env-secret-value"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("the export contains %q", secret)
		}
	}
	// Variable names are the one thing an env credential carries.
	if !bytes.Contains(data, []byte("WIKI_KEY_ID")) {
		t.Error("the env credential lost its variable names")
	}
}

func TestRoundTripIntoEmptyInstallation(t *testing.T) {
	path, _ := exported(t, ExportSelection{All: true, Defaults: true, ProviderNotes: true})
	for name, text := range map[string]string{"no file": "", "empty config": "version: 3\n"} {
		t.Run(name, func(t *testing.T) {
			dst := newTransferWorld(t, text)
			plan := mustPlan(t, dst, path, ImportModeCreate)
			created, replaced, skipped := plan.Count()
			if created != 13 || replaced != 0 || skipped != 0 {
				t.Errorf("Count() = %d %d %d, want 13 0 0 (%+v)", created, replaced, skipped, plan.Entries)
			}
			res, err := dst.svc.ApplyImport(plan)
			if err != nil {
				t.Fatal(err)
			}
			if res.Created != 13 {
				t.Errorf("result = %+v", res)
			}
			got := dst.load()
			src, _ := newTransferWorld(t, transferSource).store.Load()
			for _, name := range []string{"wiki", "mail-svc", "spare"} {
				a, b := got.Services[name], src.Services[name]
				a.ID, b.ID = "", ""
				if !reflect.DeepEqual(a, b) {
					t.Errorf("service %s = %+v, want %+v", name, a, b)
				}
			}
			for name, cred := range src.Credentials {
				a := got.Credentials[name]
				a.ID, cred.ID = "", ""
				if !reflect.DeepEqual(a, cred) {
					t.Errorf("credential %s = %+v, want %+v", name, a, cred)
				}
			}
			if got.Connections["wiki"].ID != idWikiConn || got.Services["wiki"].ID != idWikiSvc || got.Credentials["wiki-env"].ID != idWikiCred {
				t.Error("free ids of the file were not kept")
			}
			if !reflect.DeepEqual(got.Connections["wiki"].ForwardSecrets, []string{"payload"}) ||
				!reflect.DeepEqual(got.Defaults, src.Defaults) || !reflect.DeepEqual(got.ProviderNotes, src.ProviderNotes) {
				t.Errorf("defaults or notes or forward secrets differ: %+v", got)
			}
			if !reflect.DeepEqual(plan.CredentialsWithoutSecret, []string{"mail-key", "payload", "spare-key"}) {
				t.Errorf("CredentialsWithoutSecret = %v", plan.CredentialsWithoutSecret)
			}
		})
	}
}

// targetConfig has a service, credential and connection named like the source's, with other contents.
const targetConfig = `version: 3
services:
    wiki:
        id: ` + idOther + `
        provider: docs
        base_url: https://target-wiki.example.test
    own:
        provider: mail
        base_url: https://own.example.test
credentials:
    wiki-env:
        id: 55555555555555555555555555555555
        provider: docs
        type: env
        values:
            key-id: TARGET_ID
            key-secret: TARGET_SECRET
    own-key:
        provider: mail
        type: keyring
connections:
    own:
        service: own
        credential: own-key
defaults:
    connections:
        mail: own
`

func TestImportCreateRefusesExistingNames(t *testing.T) {
	path, _ := exported(t, ExportSelection{Connections: []string{"wiki"}})
	dst := newTransferWorld(t, targetConfig)
	before := dst.raw()
	_, err := dst.svc.PlanImport(path, ImportModeCreate)
	var exists *ImportExistsError
	if !errors.As(err, &exists) || !reflect.DeepEqual(exists.Entries, []string{"services.wiki", "credentials.wiki-env"}) {
		t.Fatalf("PlanImport(create) = %v, want the two existing names", err)
	}
	if !bytes.Equal(before, dst.raw()) {
		t.Error("a refused plan changed the configuration")
	}
}

func TestImportMergeSkipsExistingAndUsesTargetEntries(t *testing.T) {
	path, _ := exported(t, ExportSelection{All: true, Defaults: true, ProviderNotes: true})
	dst := newTransferWorld(t, targetConfig)
	plan := mustPlan(t, dst, path, ImportModeMerge)
	if e := entry(plan, ImportServices, "wiki"); e.Action != ImportActionSkip {
		t.Errorf("service wiki = %+v, want skip", e)
	}
	conn := entry(plan, ImportConnections, "wiki")
	if conn.Action != ImportActionCreate || !reflect.DeepEqual(conn.UsesTarget, []string{"service wiki", "credential wiki-env"}) {
		t.Errorf("connection wiki = %+v, want create using the target's entries", conn)
	}
	if e := entry(plan, ImportDefaults, "mail"); e.Action != ImportActionSkip {
		t.Errorf("default mail = %+v, want skip (target points elsewhere)", e)
	}
	if e := entry(plan, ImportDefaults, "docs"); e.Action != ImportActionCreate {
		t.Errorf("default docs = %+v, want create", e)
	}
	if _, err := dst.svc.ApplyImport(plan); err != nil {
		t.Fatal(err)
	}
	got := dst.load()
	if got.Services["wiki"].BaseURL != "https://target-wiki.example.test" || got.Services["wiki"].ID != idOther {
		t.Errorf("the target's service changed: %+v", got.Services["wiki"])
	}
	if got.Defaults.Connections["mail"] != "own" || got.Defaults.Connections["docs"] != "wiki" {
		t.Errorf("defaults = %v", got.Defaults.Connections)
	}
	if got.Connections["wiki"].Service != "wiki" || len(got.Connections) != 3 {
		t.Errorf("connections = %v", got.Connections)
	}
}

func TestImportMergeRefusesTargetEntryThatDoesNotFit(t *testing.T) {
	path, _ := exported(t, ExportSelection{Connections: []string{"wiki"}})
	// The target's service of that name belongs to another provider than the file's credential.
	text := strings.Replace(targetConfig, "provider: docs\n        base_url: https://target-wiki.example.test", "provider: mail\n        base_url: https://target-wiki.example.test", 1)
	dst := newTransferWorld(t, text)
	before := dst.raw()
	_, err := dst.svc.PlanImport(path, ImportModeMerge)
	var refused *ImportRefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("PlanImport() = %v, want a refusal for an invalid result", err)
	}
	if !bytes.Equal(before, dst.raw()) {
		t.Error("the configuration changed")
	}
}

func TestImportOverwriteReplacesAndKeepsTargetIDs(t *testing.T) {
	path, _ := exported(t, ExportSelection{All: true})
	dst := newTransferWorld(t, targetConfig)
	plan := mustPlan(t, dst, path, ImportModeOverwrite)
	svc := entry(plan, ImportServices, "wiki")
	if svc.Action != ImportActionReplace || svc.ID != idOther || !svc.NewID {
		t.Errorf("service wiki = %+v, want replace keeping the target's id", svc)
	}
	cred := entry(plan, ImportCredentials, "wiki-env")
	if cred.Action != ImportActionReplace || cred.ID != "55555555555555555555555555555555" || !cred.NewID {
		t.Errorf("credential wiki-env = %+v", cred)
	}
	if !reflect.DeepEqual(plan.CredentialsKeepSecret, []string{"wiki-env"}) {
		t.Errorf("CredentialsKeepSecret = %v", plan.CredentialsKeepSecret)
	}
	if _, err := dst.svc.ApplyImport(plan); err != nil {
		t.Fatal(err)
	}
	got := dst.load()
	if got.Services["wiki"].BaseURL != "https://wiki.example.test" || got.Services["wiki"].ID != idOther {
		t.Errorf("service wiki = %+v", got.Services["wiki"])
	}
	if got.Credentials["wiki-env"].Values["key-id"] != "WIKI_KEY_ID" {
		t.Errorf("credential wiki-env = %+v", got.Credentials["wiki-env"])
	}
	if _, ok := got.Connections["own"]; !ok || got.Services["own"].Provider != "mail" {
		t.Error("an entry outside the file was touched")
	}
	if len(dst.secrets.sets) != 0 || len(dst.secrets.entries) != 0 {
		t.Error("the import touched secrets")
	}
}

func TestImportOverwriteNamesConnectionsOutsideTheFile(t *testing.T) {
	path, _ := exported(t, ExportSelection{Services: []string{"wiki"}})
	text := targetConfig + "" // the target connection below uses the service that gets replaced
	text = strings.Replace(text, "connections:\n    own:", "connections:\n    uses-wiki:\n        service: wiki\n        credential: wiki-env\n        effects:\n            - read\n    own:", 1)
	dst := newTransferWorld(t, text)
	plan := mustPlan(t, dst, path, ImportModeOverwrite)
	if e := entry(plan, ImportServices, "wiki"); !reflect.DeepEqual(e.UsedBy, []string{"uses-wiki"}) {
		t.Errorf("UsedBy = %v", e.UsedBy)
	}
}

func TestImportIDRule(t *testing.T) {
	path, _ := exported(t, ExportSelection{All: true})
	// The target already has the file's service id under another name: that id is taken for services only.
	text := `version: 3
services:
    other-name:
        id: ` + idWikiSvc + `
        provider: docs
        base_url: https://other.example.test
credentials:
    holds-conn-id:
        id: ` + idWikiConn + `
        type: keyring
`
	dst := newTransferWorld(t, text)
	plan := mustPlan(t, dst, path, ImportModeCreate)
	svc := entry(plan, ImportServices, "wiki")
	if !svc.NewID || svc.ID == idWikiSvc || len(svc.ID) != 32 {
		t.Errorf("service wiki = %+v, want a new id", svc)
	}
	if conn := entry(plan, ImportConnections, "wiki"); conn.NewID || conn.ID != idWikiConn {
		t.Errorf("connection wiki = %+v, want the free id of its own kind kept", conn)
	}
	if cred := entry(plan, ImportCredentials, "wiki-env"); cred.NewID || cred.ID != idWikiCred {
		t.Errorf("credential wiki-env = %+v, want the free id kept", cred)
	}
	if noID := entry(plan, ImportServices, "mail-svc"); !noID.NewID || len(noID.ID) != 32 {
		t.Errorf("service without id = %+v, want an assigned id", noID)
	}
	if _, err := dst.svc.ApplyImport(plan); err != nil {
		t.Fatal(err)
	}
	got := dst.load()
	if got.Services["wiki"].ID != svc.ID || got.Services["other-name"].ID != idWikiSvc {
		t.Errorf("ids on disk = %+v", got.Services)
	}
}

func TestPlanWritesNothing(t *testing.T) {
	path, _ := exported(t, ExportSelection{All: true, Defaults: true})
	dst := newTransferWorld(t, targetConfig)
	before := dst.raw()
	for _, mode := range []ImportMode{ImportModeMerge, ImportModeOverwrite} {
		mustPlan(t, dst, path, mode)
	}
	if !bytes.Equal(before, dst.raw()) {
		t.Error("planning changed the configuration")
	}
	entries, _ := os.ReadDir(dst.dir)
	for _, e := range entries {
		if e.Name() != "config.yaml" {
			t.Errorf("planning left %s", e.Name())
		}
	}
	fresh := newTransferWorld(t, "")
	mustPlan(t, fresh, path, ImportModeCreate)
	if _, err := os.Stat(fresh.path); !errors.Is(err, fs.ErrNotExist) {
		t.Error("planning created the configuration file")
	}
}

func TestApplyAfterParallelChangeConflicts(t *testing.T) {
	path, _ := exported(t, ExportSelection{All: true})
	for name, text := range map[string]string{"existing file": targetConfig, "no file": ""} {
		t.Run(name, func(t *testing.T) {
			dst := newTransferWorld(t, text)
			plan := mustPlan(t, dst, path, ImportModeMerge)
			// Another writer saves between plan and commit.
			other := dst.store.New()
			if text != "" {
				other = dst.load()
			}
			if err := other.SetCredential("parallel", config.Credential{Type: config.CredentialTypeKeyring}); err != nil {
				t.Fatal(err)
			}
			if err := dst.store.Save(other); err != nil {
				t.Fatal(err)
			}
			after := dst.raw()
			_, err := dst.svc.ApplyImport(plan)
			if !errors.Is(err, config.ErrConflict) {
				t.Fatalf("ApplyImport() = %v, want a conflict", err)
			}
			if !bytes.Equal(after, dst.raw()) {
				t.Error("a conflict changed the configuration")
			}
			// A new plan on the changed state goes through.
			if _, err := dst.svc.ApplyImport(mustPlan(t, dst, path, ImportModeMerge)); err != nil {
				t.Errorf("ApplyImport(new plan) = %v", err)
			}
			if _, ok := dst.load().Credentials["parallel"]; !ok {
				t.Error("the parallel change was lost")
			}
		})
	}
}

func TestApplyWithoutChangeWritesNothing(t *testing.T) {
	path, _ := exported(t, ExportSelection{Connections: []string{"wiki"}})
	dst := newTransferWorld(t, transferSource)
	before := dst.raw()
	plan := mustPlan(t, dst, path, ImportModeMerge)
	if c, r, _ := plan.Count(); c+r != 0 {
		t.Fatalf("plan = %+v", plan.Entries)
	}
	if _, err := dst.svc.ApplyImport(plan); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, dst.raw()) {
		t.Error("an empty import rewrote the configuration")
	}
}

func TestImportRefusesForeignFiles(t *testing.T) {
	good, _ := exported(t, ExportSelection{Connections: []string{"mail"}})
	goodText, _ := os.ReadFile(good)
	dst := newTransferWorld(t, targetConfig)
	before := dst.raw()
	big := bytes.Repeat([]byte("#"), maxTransferSize+1)
	tests := []struct {
		name, text, want string
	}{
		{"other format", strings.Replace(string(goodText), TransferFormat, "something-else", 1), "not a qatlas configuration export"},
		{"no format", "services: {}\n", "not a qatlas configuration export"},
		{"future version", strings.Replace(string(goodText), "format_version: 1", "format_version: 2", 1), "format version 2"},
		{"unknown section", string(goodText) + "secrets:\n    mail-key:\n        api-key: LEAKED-VALUE-1\n", "unknown key"},
		{"unknown key in entry", strings.Replace(string(goodText), "provider: mail\n", "provider: mail\n        token: LEAKED-VALUE-2\n", 1), "unknown key"},
		{"former effects name", strings.Replace(string(goodText), "service: mail-svc", "service: mail-svc\n        permissions: [read]", 1), "permissions"},
		{"second document", string(goodText) + "---\nformat: x\n", "exactly one document"},
		{"secret values in keyring credential", strings.Replace(string(goodText), "type: keyring", "type: keyring\n        values:\n            api-key: LEAKED-VALUE-3", 1), "values"},
		{"dangling reference", strings.Replace(string(goodText), "credential: mail-key", "credential: gone", 1), "unknown credential"},
		{"too large", string(big), "larger than"},
		{"empty", "", "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "in.yaml")
			if err := os.WriteFile(file, []byte(tt.text), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := dst.svc.PlanImport(file, ImportModeMerge)
			var refused *ImportRefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("PlanImport() = %v, want a refusal containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "LEAKED-VALUE") {
				t.Errorf("the error quotes a value of the file: %v", err)
			}
			if !bytes.Equal(before, dst.raw()) {
				t.Error("a refused file changed the configuration")
			}
		})
	}
	if _, err := dst.svc.PlanImport(filepath.Join(t.TempDir(), "missing.yaml"), ImportModeMerge); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file = %v", err)
	}
	if _, err := dst.svc.PlanImport(t.TempDir(), ImportModeMerge); err == nil {
		t.Error("a directory was accepted")
	}
	if _, err := dst.svc.PlanImport(good, "replace-all"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}

func TestImportRefusesDuplicateIDsInFile(t *testing.T) {
	good, _ := exported(t, ExportSelection{All: true})
	text, _ := os.ReadFile(good)
	text = bytes.Replace(text, []byte("    mail-svc:\n"), []byte("    mail-svc:\n        id: "+idWikiSvc+"\n"), 1)
	file := filepath.Join(t.TempDir(), "in.yaml")
	if err := os.WriteFile(file, text, 0o600); err != nil {
		t.Fatal(err)
	}
	dst := newTransferWorld(t, "")
	if _, err := dst.svc.PlanImport(file, ImportModeCreate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("PlanImport() = %v, want a duplicate id refusal", err)
	}
}

func TestImportErrorsAreRedacted(t *testing.T) {
	dst := newTransferWorld(t, "")
	dst.svc.redactor.Add("hunter2-secret")
	file := filepath.Join(t.TempDir(), "hunter2-secret.yaml")
	if _, err := dst.svc.PlanImport(file, ImportModeMerge); err == nil || strings.Contains(err.Error(), "hunter2-secret") {
		t.Errorf("PlanImport() = %v, want the registered value redacted", err)
	}
}
