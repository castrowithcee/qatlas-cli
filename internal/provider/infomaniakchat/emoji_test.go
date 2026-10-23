package infomaniakchat

import (
	"net/http"
	"strings"
	"testing"
)

const emojiA = "emja0000000000000000000a1"

func emojiHandler(t *testing.T, teams string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/users/me/teams":
			return jsonResponse(200, teams), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/emoji":
			return jsonResponse(200, `[{"id":"`+emojiA+`","name":"party","creator_id":"owner0000000000000000000a",`+
				`"create_at":1735689600000,"update_at":5,"image":"leak-canary"},{"id":"../bad","name":"x"}]`), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func TestEmojiListGateRefusesChannelTargetsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, emojiHandler(t, bothTeams))
	_, err := env.invoke(emojiList.ID, "botch", `{}`)
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 || strings.Contains(err.Error(), chanA) ||
		!strings.Contains(err.Error(), emojiCatalog) {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
}

func TestEmojiListRefusesUnboundTeamWithoutEmojiRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, emojiHandler(t, bothTeams))
	_, err := env.invoke(emojiList.ID, "emojiread", `{}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), teamB) || len(calls) != 1 ||
		calls[0].path != "/api/v4/users/me/teams" {
		t.Fatalf("err = %v, calls = %+v", err, calls)
	}
}

func TestEmojiListReturnsOnlyTheAllowedFieldsAndPages(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, emojiHandler(t, bothTeams))
	result, err := env.invoke(emojiList.ID, "botall", `{"page":3,"per_page":2}`)
	if err != nil || strings.Contains(result, "bad") || strings.Contains(result, "leak-canary") ||
		!strings.Contains(result, `"updated_at":"1970-01-01T00:00:00Z"`) || !strings.Contains(result, `"count":1`) ||
		!strings.Contains(result, `"created_at":"2025-01-01T00:00:00Z"`) ||
		!strings.Contains(result, `"has_more":true`) || len(calls) != 2 || calls[1].path != "/api/v4/emoji" ||
		calls[1].query.Get("page") != "2" || calls[1].query.Get("per_page") != "2" ||
		calls[1].query.Get("sort") != "name" || len(calls[1].query) != 3 {
		t.Fatalf("result = %s, err = %v, calls = %+v", result, err, calls)
	}
}
