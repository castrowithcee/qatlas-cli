package i18n

import "testing"

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestParse(t *testing.T) {
	cases := map[string]struct {
		lang Lang
		ok   bool
	}{
		"de":          {German, true},
		"de_DE.UTF-8": {German, true},
		"de-AT":       {German, true},
		"DE_de@euro":  {German, true},
		"en_US.UTF-8": {English, true},
		"en":          {English, true},
		"fr_FR":       {English, false},
		"C":           {English, false},
		"POSIX":       {English, false},
		"":            {English, false},
	}
	for in, want := range cases {
		lang, ok := Parse(in)
		if lang != want.lang || ok != want.ok {
			t.Errorf("Parse(%q) = %q, %v, want %q, %v", in, lang, ok, want.lang, want.ok)
		}
	}
}

func TestDetectFromEnvOrder(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		want Lang
	}{
		{"nothing set", nil, English},
		{"LANG only", map[string]string{"LANG": "de_DE.UTF-8"}, German},
		{"LC_MESSAGES beats LANG", map[string]string{"LC_MESSAGES": "de_DE", "LANG": "en_US.UTF-8"}, German},
		{"LC_ALL beats both", map[string]string{"LC_ALL": "en_GB", "LC_MESSAGES": "de_DE", "LANG": "de_DE"}, English},
		{"empty LC_ALL is skipped", map[string]string{"LC_ALL": "", "LANG": "de_DE.UTF-8"}, German},
		{"C locale ends the search", map[string]string{"LC_ALL": "C", "LANG": "de_DE.UTF-8"}, English},
		{"unsupported language", map[string]string{"LANG": "fr_FR.UTF-8"}, English},
		{"blank value", map[string]string{"LANG": "  "}, English},
	}
	for _, c := range cases {
		if got := detectFromEnv(env(c.vars)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLookupFallsBackToEnglish(t *testing.T) {
	type key string
	catalogs := Catalogs[key]{
		English: {"a": "apple", "b": "bread"},
		German:  {"a": "Apfel"},
	}
	cases := []struct {
		lang Lang
		key  key
		want string
	}{
		{German, "a", "Apfel"},
		{German, "b", "bread"},
		{English, "a", "apple"},
		{Lang("fr"), "a", "apple"},
		{German, "missing", "missing"},
	}
	for _, c := range cases {
		if got := Lookup(catalogs, c.lang, c.key); got != c.want {
			t.Errorf("Lookup(%q, %q) = %q, want %q", c.lang, c.key, got, c.want)
		}
	}
}
