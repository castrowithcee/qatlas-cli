package penpot

import (
	"net/http"
	"strings"
	"testing"
)

func teamsBody() string {
	return `[{"id":"` + teamA + `","name":"Kunde A","isDefault":true},{"id":"` + teamB + `","name":"Kunde B"},` +
		`{"id":"` + teamForeign + `","name":"Fremd"}]`
}

func TestTeamsListShowsOnlyBoundTeams(t *testing.T) {
	for connection, want := range map[string][]string{"one": {"Kunde A"}, "two": {"Kunde A", "Kunde B"}} {
		var calls []call
		env := newEnvironment(t, &calls, func(call) (*http.Response, error) { return jsonResponse(200, teamsBody()), nil })
		result, err := env.invoke(teamsList.ID, connection, `{}`)
		if err != nil {
			t.Fatalf("%s: %v", connection, err)
		}
		for _, name := range []string{"Kunde A", "Kunde B", "Fremd"} {
			wanted := false
			for _, w := range want {
				wanted = wanted || w == name
			}
			if strings.Contains(result, name) != wanted {
				t.Errorf("%s: %q in result = %t, want %t: %s", connection, name, !wanted, wanted, result)
			}
		}
		if len(calls) != 1 || calls[0].command() != cmdTeams || calls[0].method != http.MethodPost {
			t.Errorf("%s: calls = %+v", connection, calls)
		}
	}
}

func TestTeamsListBoundsStringsAndCount(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(call) (*http.Response, error) {
		return jsonResponse(200, `[{"id":"`+teamA+`","name":"`+strings.Repeat("ä", 600)+`"}]`), nil
	})
	result, err := env.invoke(teamsList.ID, "one", `{}`)
	if err != nil || strings.Contains(result, strings.Repeat("ä", 200)) {
		t.Fatalf("result = %.200s, err = %v", result, err)
	}
	// more teams than the bound allows cannot occur through a bound connection; the cap is a hard bound anyway
	if maxTeamsListed < maxTeams {
		t.Fatal("the team list cap must not be below the configurable team count")
	}
}
