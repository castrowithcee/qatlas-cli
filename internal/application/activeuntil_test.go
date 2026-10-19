package application

import (
	"testing"
	"time"
)

var limit = time.Date(2030, 1, 2, 10, 0, 0, 0, time.UTC)

func limitConnection(core *Core, name string) {
	connection := core.all.Connections[name]
	connection.ActiveUntil = limit.Format(time.RFC3339)
	core.all.Connections[name] = connection
}

func clockAt(core *Core, at time.Time) { core.SetClock(func() time.Time { return at }) }

func TestConnectionIsActiveUntilItsLimitAndRefusesAfterwards(t *testing.T) {
	core, calls := testCore(t, []string{"on", "timed"}, nil, true)
	limitConnection(core, "timed")

	clockAt(core, limit.Add(-time.Second))
	if _, err := invokeGet(core, "timed"); err != nil {
		t.Fatalf("before the limit: %v", err)
	}
	for _, c := range core.Connections("", nil).Connections {
		if c.State != "" {
			t.Errorf("%s: state %q before the limit", c.Name, c.State)
		}
		if want := map[string]string{"on": "", "timed": "2030-01-02T10:00:00Z"}[c.Name]; c.ActiveUntil != want {
			t.Errorf("%s: active_until %q, want %q", c.Name, c.ActiveUntil, want)
		}
	}

	clockAt(core, limit)
	before := *calls
	_, err := invokeGet(core, "timed")
	refusalOf(t, err, "timed")
	if *calls != before {
		t.Fatal("handler called through an expired connection")
	}
	for _, c := range core.Connections("", nil).Connections {
		if want := map[string]string{"on": "", "timed": StateDisabled}[c.Name]; c.State != want || c.Unusable != nil {
			t.Errorf("%s: state %q unusable %v, want state %q", c.Name, c.State, c.Unusable, want)
		}
	}
	if provider := core.Providers().Providers[0]; provider.Connections != 1 || provider.Configured != 2 {
		t.Errorf("provider counts = %+v, want 1 offering and 2 configured", provider)
	}
	_, err = core.Describe(DescribeRequest{Operation: "fake.pages.get", Connection: "timed"})
	refusalOf(t, err, "timed")
	if only, err := core.Search(SearchRequest{Connection: "timed"}); err != nil || len(only.Operations) != 0 {
		t.Errorf("Search(timed) = %+v, %v, want no tool", only, err)
	}
	if got := core.config.IdleWarning("timed"); got != "" {
		t.Errorf("an expired connection warns: %s", got)
	}
}

func TestDefaultOnExpiredConnectionIsRefusedNeverSkipped(t *testing.T) {
	for name, defaults := range map[string]map[string]string{
		"tool default":     {"fake.pages.get": "timed", "fake": "on"},
		"provider default": {"fake": "timed"},
	} {
		core, calls := testCore(t, []string{"on", "timed"}, defaults, true)
		limitConnection(core, "timed")
		clockAt(core, limit.Add(time.Hour))
		_, err := invokeGet(core, "")
		if *calls != 0 {
			t.Fatalf("%s: handler called", name)
		}
		refusalOf(t, err, "timed")
	}

	core, _ := testCore(t, []string{"timed"}, nil, true)
	limitConnection(core, "timed")
	clockAt(core, limit.Add(time.Hour))
	_, err := invokeGet(core, "")
	refusalOf(t, err, "timed")
}
