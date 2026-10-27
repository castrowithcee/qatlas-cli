package config

import (
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func excludeConfig(t *testing.T, targets []string, exclude ...string) *Config {
	t.Helper()
	cfg := problemsBase(t)
	withExcluder(cfg, "x", exclude...)
	conn := cfg.Connections["ex"]
	conn.Targets = targets
	cfg.Connections["ex"] = conn
	return cfg
}

func excludeCodes(cfg *Config) []string {
	var codes []string
	for _, p := range Problems(cfg.Validate()) {
		codes = append(codes, p.Code)
	}
	return codes
}

func TestExcludeRoundTrip(t *testing.T) {
	cfg := excludeConfig(t, []string{"repos/a/b"}, "repos/a/c", "repos/a/d")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	first, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "exclude:") {
		t.Fatalf("exclude was not written:\n%s", first)
	}
	back, err := Decode(strings.NewReader(string(first)), filesProviders{testProviders})
	if err != nil {
		t.Fatal(err)
	}
	conn := back.Connections["ex"]
	if !reflect.DeepEqual(conn.ExcludeValues(), []string{"repos/a/c", "repos/a/d"}) {
		t.Errorf("ExcludeValues = %v", conn.Exclude)
	}
	if !reflect.DeepEqual(conn.TargetValues(), []string{"repos/a/b"}) {
		t.Errorf("TargetValues = %v, must not contain exclusions", conn.TargetValues())
	}
	second, _ := yaml.Marshal(back)
	if string(first) != string(second) {
		t.Errorf("rewrite is not byte-stable")
	}
	plain, _ := yaml.Marshal(problemsBase(t))
	if strings.Contains(string(plain), "exclude") {
		t.Error("a connection without exclusions wrote the field")
	}
}

func TestExcludeValidity(t *testing.T) {
	for name, tc := range map[string]struct {
		targets []string
		exclude []string
		want    []string
	}{
		"valid":                {[]string{"repos/a/b"}, []string{"repos/a/c"}, nil},
		"outside all target":   {[]string{"repos/a/b"}, []string{"repos/z/z"}, nil},
		"wildcard target":      {[]string{"*"}, []string{"repos/a/c"}, nil},
		"empty":                {[]string{"repos/a/b"}, []string{""}, []string{CodeEmptyExclude}},
		"blank":                {[]string{"repos/a/b"}, []string{"  "}, []string{CodeEmptyExclude}},
		"duplicate":            {[]string{"repos/a/b"}, []string{"repos/a/c", "repos/a/c"}, []string{CodeDuplicateExclude}},
		"is target":            {[]string{"repos/a/b", "repos/a/c"}, []string{"repos/a/c"}, []string{CodeExcludeIsTarget}},
		"provider wildcard":    {[]string{"repos/a/b"}, []string{"*"}, []string{CodeExcludeWildcard}},
		"wildcard segment":     {[]string{"repos/a/b"}, []string{"repos/*/c"}, []string{CodeExcludeWildcard}},
		"form spells the star": {[]string{"repos/a/b"}, []string{"repos/a/*"}, nil},
		"star for placeholder": {[]string{"repos/a/b"}, []string{"repos/*/*"}, []string{CodeExcludeWildcard}},
		"star of other kind":   {[]string{"repos/a/b"}, []string{"orgs/*"}, []string{CodeExcludeWildcard}},
		"kind not excludable":  {[]string{"repos/a/b"}, []string{"orgs/x"}, []string{CodeInvalidExclude}},
		"no form":              {[]string{"repos/a/b"}, []string{"repos/a"}, []string{CodeInvalidExclude}},
		"surrounding space":    {[]string{"repos/a/b"}, []string{" repos/a/c"}, []string{CodeInvalidExclude}},
	} {
		t.Run(name, func(t *testing.T) {
			got := excludeCodes(excludeConfig(t, tc.targets, tc.exclude...))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("codes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExcludeStarAloneIsAWildcardEvenWhereAFormSpellsIt(t *testing.T) {
	meta := TargetMetadata{Kinds: []TargetKind{{Name: "all", Forms: []string{"*"}, Excludable: true}}}
	got := meta.CheckExcludes([]string{"*"}, nil)
	if len(got) != 1 || got[0].Fault != ExcludeWildcard {
		t.Errorf("findings = %v, want one wildcard", got)
	}
}

func TestExcludeFailsClosedWithoutDeclaredSupport(t *testing.T) {
	cfg := problemsBase(t)
	conn := cfg.Connections["gh"]
	conn.Exclude = []string{"repos/a/c"}
	cfg.Connections["gh"] = conn
	if got := excludeCodes(cfg); !reflect.DeepEqual(got, []string{CodeExcludeUnsupported}) {
		t.Errorf("provider without support: %v", got)
	}
	// A provider the catalog does not know declares nothing either.
	cfg = problemsBase(t)
	cfg.Services["gh"] = Service{Provider: "nowhere", BaseURL: "https://x.example.invalid"}
	conn.Exclude = []string{"repos/a/c"}
	cfg.Connections["gh"] = conn
	found := false
	for _, code := range excludeCodes(cfg) {
		found = found || code == CodeExcludeUnsupported
	}
	if !found {
		t.Error("unknown provider did not make the exclusions invalid")
	}
	// Without exclusions an existing connection stays valid.
	if err := problemsBase(t).Validate(); err != nil {
		t.Error(err)
	}
}

func TestExcludeAndRequiredTarget(t *testing.T) {
	cfg := excludeConfig(t, nil, "repos/a/c")
	if got := excludeCodes(cfg); !reflect.DeepEqual(got, []string{CodeTargetRequired}) {
		t.Errorf("exclude without target: %v", got)
	}
	if got := excludeCodes(excludeConfig(t, []string{"*"}, "repos/a/c")); got != nil {
		t.Errorf("wildcard target and exclude: %v", got)
	}
}

func TestExcludeProblemsQuoteNoValue(t *testing.T) {
	secret := "repos/SECRETOWNER/SECRETREPO"
	for _, exclude := range [][]string{{secret, secret}, {secret + "/*"}, {"orgs/" + secret}, {" " + secret}} {
		err := excludeConfig(t, []string{secret}, exclude...).Validate()
		if err != nil && strings.Contains(err.Error(), "SECRET") {
			t.Errorf("error quotes a value: %v", err)
		}
		for _, p := range Problems(err) {
			if strings.Contains(p.Hint(), "SECRET") {
				t.Errorf("hint quotes a value")
			}
		}
	}
}

func TestExcludeLabelsAreValidAndKept(t *testing.T) {
	cfg := excludeConfig(t, []string{"repos/a/b"}, "repos/a/c")
	conn := cfg.Connections["ex"]
	conn.TargetLabels = map[string]string{"repos/a/b": "Main", "repos/a/c": "Old", "repos/a/z": "Gone"}
	if err := cfg.SetConnection("ex", conn); err != nil {
		t.Fatal(err)
	}
	got := cfg.Connections["ex"].TargetLabels
	if len(got) != 2 || got["repos/a/c"] != "Old" || got["repos/a/b"] != "Main" {
		t.Errorf("labels after SetConnection = %v", got)
	}
	if err := cfg.Validate(); err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(cfg.Connections["ex"].TargetValues(), []string{"repos/a/b"}) {
		t.Error("exclusion entered TargetValues")
	}
}

func TestExcludeDoesNotChangeReachOrFixesTarget(t *testing.T) {
	meta, _ := testProviders.ProviderMetadata("excluder")
	targets := []string{"repos/a/b"}
	cfg := excludeConfig(t, targets, "repos/a/c")
	resolved, err := cfg.Resolve("ex", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved.Exclude, []string{"repos/a/c"}) || !reflect.DeepEqual(resolved.Targets, targets) {
		t.Errorf("resolved = %v / %v", resolved.Exclude, resolved.Targets)
	}
	for _, kind := range []string{"repo", "org"} {
		if meta.Target.FixesTarget(kind, resolved.Targets) != meta.Target.FixesTarget(kind, targets) {
			t.Errorf("FixesTarget(%s) differs", kind)
		}
	}
	r1, h1 := meta.Target.Reach("t", resolved.Targets)
	r2, h2 := meta.Target.Reach("t", targets)
	if r1 != r2 || h1 != h2 {
		t.Error("Reach differs")
	}
}

func TestChangedConnectionsSeesExclude(t *testing.T) {
	before := excludeConfig(t, []string{"repos/a/b"}, "repos/a/c")
	after := before.Clone()
	conn := after.Connections["ex"]
	conn.Exclude = []string{"repos/a/d"}
	after.Connections["ex"] = conn
	changes := ChangedConnections(before, after)
	if len(changes) != 1 || changes[0].Name != "ex" || changes[0].Kind != ConnectionChanged {
		t.Errorf("changes = %v", changes)
	}
}

func TestCloneCopiesExcludeDeeply(t *testing.T) {
	cfg := excludeConfig(t, []string{"repos/a/b"}, "repos/a/c")
	clone := cfg.Clone()
	clone.Connections["ex"].Exclude[0] = "changed"
	if cfg.Connections["ex"].Exclude[0] != "repos/a/c" {
		t.Error("Clone shares the exclude list")
	}
	values := cfg.Connections["ex"].ExcludeValues()
	values[0] = "changed"
	if cfg.Connections["ex"].Exclude[0] != "repos/a/c" {
		t.Error("ExcludeValues shares the list")
	}
}

func TestExcludeDoesNotChangeIdleWarning(t *testing.T) {
	plain := excludeConfig(t, []string{"repos/a/b"})
	with := excludeConfig(t, []string{"repos/a/b"}, "repos/a/c")
	if a, b := plain.IdleWarning("ex"), with.IdleWarning("ex"); a != b {
		t.Errorf("IdleWarning = %q with exclusions, %q without", b, a)
	}
	for _, cfg := range []*Config{plain, with} {
		conn := cfg.Connections["ex"]
		conn.Effects = []Effect{}
		cfg.Connections["ex"] = conn
	}
	if a, b := plain.IdleWarning("ex"), with.IdleWarning("ex"); a != b {
		t.Errorf("idle IdleWarning = %q with exclusions, %q without", b, a)
	}
}
