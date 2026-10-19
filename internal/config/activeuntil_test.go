package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	yaml "go.yaml.in/yaml/v3"
)

var clockNow = time.Date(2030, 1, 2, 9, 0, 0, 0, time.UTC)

func timedConfig(t *testing.T, limit string) *Config {
	t.Helper()
	cfg, err := Decode(strings.NewReader(strings.Replace(minimal,
		"    credential: reader\n", "    credential: reader\n    permissions: [read]\n    active_until: "+limit+"\n", 1)),
		testProviders)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestActiveUntilRefusesLikeDisabledOnlyFromTheLimitOn(t *testing.T) {
	cfg := timedConfig(t, "2030-01-02T10:00:00Z")
	read := ToolMetadata{ID: "bookstack.pages.list", Effect: EffectRead}
	update := ToolMetadata{ID: "bookstack.pages.update", Effect: EffectUpdate}
	for at, want := range map[time.Time]Refusal{
		clockNow:                              "",
		clockNow.Add(time.Hour - time.Second): "",
		clockNow.Add(time.Hour):               RefusalDisabled,
		clockNow.Add(24 * time.Hour):          RefusalDisabled,
	} {
		if got := cfg.ConnectionRefusal("wiki", read, at); got != want {
			t.Errorf("refusal at %v = %q, want %q", at, got, want)
		}
		if got := cfg.ConnectionAllows("wiki", read, at); got != (want == "") {
			t.Errorf("allows at %v = %v", at, got)
		}
	}
	// Only a tool the connection would offer is reported as expired.
	if got := cfg.ConnectionRefusal("wiki", update, clockNow.Add(2*time.Hour)); got != RefusalEffect {
		t.Errorf("refusal of a refused tool = %q, want %q", got, RefusalEffect)
	}
	if got := cfg.IdleWarning("wiki"); got != "" {
		t.Errorf("an expired connection warns: %s", got)
	}
}

func TestActiveUntilRoundTripsCanonicalAndLoadsOnceExpired(t *testing.T) {
	// A past instant, and a date, are valid when read; the date is written as RFC 3339.
	cfg := timedConfig(t, "2001-02-03T04:05:06+02:00")
	if got := cfg.Connections["wiki"].ActiveUntil; got != "2001-02-03T04:05:06+02:00" {
		t.Fatalf("ActiveUntil = %q, read as written", got)
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "2001-02-03T02:05:06Z") {
		t.Fatalf("the limit was not written canonical:\n%s", encoded)
	}
	back, err := Decode(strings.NewReader(string(encoded)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Connections["wiki"].Off(clockNow) || back.Clone().Connections["wiki"].ActiveUntil == "" {
		t.Fatal("the limit was lost or not past")
	}
	if dated := timedConfig(t, "2001-02-03"); dated.Connections["wiki"].Off(clockNow) != true {
		t.Fatal("a date in the past does not expire the connection")
	}

	unlimited, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = yaml.Marshal(unlimited)
	if strings.Contains(string(encoded), "active_until") {
		t.Fatalf("a connection without a limit wrote the field:\n%s", encoded)
	}
}

func TestActiveUntilWithUnknownFormatIsAProblem(t *testing.T) {
	_, err := Decode(strings.NewReader(strings.Replace(minimal,
		"    credential: reader\n", "    credential: reader\n    active_until: \"next week\"\n", 1)), testProviders)
	problems := Problems(err)
	if len(problems) != 1 || problems[0].Code != CodeInvalidActiveUntil || problems[0].Hint() == "" {
		t.Fatalf("problems = %v, want one %s with a hint", problems, CodeInvalidActiveUntil)
	}
	var unreadable Connection
	unreadable.ActiveUntil = "next week"
	if !unreadable.Off(clockNow) {
		t.Fatal("a limit that does not read keeps the connection on")
	}
}

func TestExpiryFormatIsParsedOnce(t *testing.T) {
	if _, err := ParseExpiryTime("2001-02-03"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseExpiryTime("x"); !errors.Is(err, ErrExpiryFormat) {
		t.Fatalf("err = %v", err)
	}
	if _, err := ParseExpiry("2001-02-03", clockNow); !errors.Is(err, ErrExpiryPast) {
		t.Fatalf("err = %v", err)
	}
}
