package infomaniakchat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const (
	ownUser   = "userown0000000000000000a1"
	otherUser = "userother00000000000000b2"
	catCustom = "catcustom0000000000000001"
	catOther  = "catcustom0000000000000002"
	hiddenCh  = "chanhidden0000000000000h9"
	catBase   = "/api/v4/users/me/teams/" + teamA + "/channels/categories"
)

var catFavorites = "favorites_" + ownUser + "_" + teamA

func categoryWith(id, user, team, name, kind string, channels ...string) string {
	quoted := make([]string, len(channels))
	for i, c := range channels {
		quoted[i] = `"` + c + `"`
	}
	return `{"id":"` + id + `","user_id":"` + user + `","team_id":"` + team + `","display_name":"` + name +
		`","type":"` + kind + `","sorting":"manual","muted":true,"collapsed":true,"channel_ids":[` +
		strings.Join(quoted, ",") + `]}`
}

// categoryFixture is a fake instance: teamA holds chanA (public) and chanC (private) as reachable channels,
// chanE is archived, chanD is direct, chanB belongs to teamB, and hiddenCh is unknown to the team listing.
type categoryFixture struct {
	t          *testing.T
	categories map[string]string
	listing    string
	mutate     func(*http.Request) (*http.Response, error)
}

func newCategoryFixture(t *testing.T, mutate func(*http.Request) (*http.Response, error)) *categoryFixture {
	f := &categoryFixture{t: t, mutate: mutate, categories: map[string]string{
		catCustom:    categoryWith(catCustom, ownUser, teamA, "Mine", "custom", chanA, chanD, chanE, hiddenCh),
		catFavorites: categoryWith(catFavorites, ownUser, teamA, "Favorites", "favorites", chanC, chanB),
		catOther:     categoryWith(catOther, otherUser, teamA, "Theirs", "custom", chanA),
		"catteamb":   categoryWith("catteamb", ownUser, teamB, "B", "custom", chanB),
	}}
	f.listing = `{"order":["` + catFavorites + `","` + catCustom + `"],"categories":[` +
		f.categories[catCustom] + `,` + f.categories[catFavorites] + `,` + f.categories[catOther] + `,` +
		f.categories["catteamb"] + `]}`
	return f
}

func (f *categoryFixture) handle(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		if f.mutate == nil {
			f.t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return f.mutate(r)
	}
	switch r.URL.Path {
	case "/api/v4/users/me":
		return jsonResponse(200, `{"id":"`+ownUser+`"}`), nil
	case "/api/v4/users/me/teams/" + teamA + "/channels":
		return jsonResponse(200, "["+strings.Join([]string{channelWith(chanA, teamA, "O", ""),
			channelWith(chanC, teamA, "P", ""), channelWith(chanE, teamA, "O", archivedAt),
			channelWith(chanD, "", "D", ""), channelWith(chanB, teamB, "O", "")}, ",")+"]"), nil
	case catBase:
		return jsonResponse(200, f.listing), nil
	case "/api/v4/channels/" + chanA:
		return jsonResponse(200, channelWith(chanA, teamA, "O", "")), nil
	case "/api/v4/channels/" + chanE:
		return jsonResponse(200, channelWith(chanE, teamA, "O", archivedAt)), nil
	case "/api/v4/channels/" + chanB:
		return jsonResponse(200, channelWith(chanB, teamB, "O", "")), nil
	case "/api/v4/channels/" + chanD:
		return jsonResponse(200, channelWith(chanD, "", "D", "")), nil
	}
	for id, body := range f.categories {
		if r.URL.Path == catBase+"/"+id {
			return jsonResponse(200, body), nil
		}
	}
	f.t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	return nil, nil
}

func (f *categoryFixture) env(calls *[]call) *environment {
	return newEnvironment(f.t, calls, f.handle)
}

func changes(calls []call) int {
	return len(calls) - countMethod(calls, http.MethodGet)
}

func TestCategoriesListShowsOnlyReachableChannelsOfTheOwnUserInTheTeam(t *testing.T) {
	var calls []call
	env := newCategoryFixture(t, nil).env(&calls)
	result, err := env.invoke(categoriesList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page CategoriesPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Total != 2 || page.Count != 2 ||
		page.Categories[0].ID != catFavorites || page.Categories[1].ID != catCustom {
		t.Fatalf("result = %s, %v, want the two own categories of teamA in kChat's order", result, err)
	}
	for _, hidden := range []string{chanD, chanE, chanB, hiddenCh, catOther, "catteamb"} {
		if strings.Contains(result, hidden) {
			t.Fatalf("result = %s, want %s absent", result, hidden)
		}
	}
	if got := page.Categories[1].ChannelIDs; len(got) != 1 || got[0] != chanA {
		t.Fatalf("channel_ids = %v, want only %s", got, chanA)
	}
	if changes(calls) != 0 {
		t.Fatalf("calls = %+v, want reads only", calls)
	}

	// With a channel allow-list, chanC is no longer reachable.
	result, err = env.invoke(categoriesList.ID, "channel", `{"team_id":"`+teamA+`"}`)
	if err != nil || strings.Contains(result, chanC) {
		t.Fatalf("result = %s, %v, want chanC hidden by the allow-list", result, err)
	}

	calls, *env.reads = nil, 0
	if _, err := env.invoke(categoriesList.ID, "team", `{"team_id":"`+teamB+`"}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
}

func TestCategoriesListReadsAnArrayWrappedAnswer(t *testing.T) {
	f := newCategoryFixture(t, nil)
	f.listing = "[" + f.listing + "]"
	var calls []call
	result, err := f.env(&calls).invoke(categoriesList.ID, "team", `{"team_id":"`+teamA+`"}`)
	if err != nil || !strings.Contains(result, `"total":2`) {
		t.Fatalf("result = %s, %v", result, err)
	}
}

func TestCategoriesCreateSendsOneTypedPostForReachableChannels(t *testing.T) {
	f := newCategoryFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != catBase {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, categoryWith("catnew00000000000000001", ownUser, teamA, "Customers", "custom",
			chanA, chanC)), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"team_id":"` + teamA + `","display_name":"Customers","channel_ids":["` + chanA + `","` + chanC + `"]}`
	if _, err := env.invoke(categoriesCreate.ID, "creator", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(categoriesCreate.ID, "creator", args)
	if err != nil || !strings.Contains(result, `"display_name":"Customers"`) || !strings.Contains(result, chanC) {
		t.Fatalf("result = %s, %v", result, err)
	}
	post := calls[len(calls)-1]
	want := `{"channel_ids":["` + chanA + `","` + chanC + `"],"display_name":"Customers","team_id":"` + teamA +
		`","type":"custom","user_id":"` + ownUser + `"}`
	if changes(calls) != 1 || post.method != http.MethodPost || post.body != want {
		t.Fatalf("calls = %+v, want one typed POST last: %s", calls, want)
	}
}

func TestCategoriesCreateRefusesChannelsOutsideTheBoundaryBeforeThePost(t *testing.T) {
	var calls []call
	env := newCategoryFixture(t, nil).env(&calls)
	base := `{"team_id":"` + teamA + `","display_name":"X","channel_ids":["`

	// Outside the allow-list: refused before the secret is read.
	if _, err := env.confirmed(categoriesCreate.ID, "channel", base+chanC+`"]}`); !isInvalidRequest(err) ||
		len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
	// Malformed and repeated IDs are refused locally; unreachable channels by the live set, never by a POST.
	for _, ids := range []string{"../" + chanA, chanA + `","` + chanA} {
		if _, err := env.confirmed(categoriesCreate.ID, "creator", base+ids+`"]}`); !isInvalidRequest(err) ||
			len(calls) != 0 {
			t.Fatalf("ids %q: err = %v, calls = %+v", ids, err, calls)
		}
	}
	for _, id := range []string{chanB, chanD, chanE, hiddenCh} {
		calls = nil
		_, err := env.confirmed(categoriesCreate.ID, "creator", base+id+`"]}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), id) || changes(calls) != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", id, err, calls)
		}
	}
	// A foreign team and a bad name never reach the instance.
	calls, *env.reads = nil, 0
	for _, args := range []string{`{"team_id":"` + teamB + `","display_name":"X"}`,
		`{"team_id":"` + teamA + `","display_name":""}`, `{"team_id":"` + teamA + `","display_name":"a\u0007b"}`} {
		if _, err := env.confirmed(categoriesCreate.ID, "creator", args); !isInvalidRequest(err) || len(calls) != 0 ||
			*env.reads != 0 {
			t.Fatalf("args %s: err = %v, calls = %+v", args, err, calls)
		}
	}
}

func TestCategoriesCreateRejectsAnAnswerForAnotherTeamUserOrType(t *testing.T) {
	for name, answer := range map[string]string{
		"team": categoryWith("catnew00000000000000001", ownUser, teamB, "X", "custom"),
		"user": categoryWith("catnew00000000000000001", otherUser, teamA, "X", "custom"),
		"type": categoryWith("catnew00000000000000001", ownUser, teamA, "X", "favorites"),
	} {
		f := newCategoryFixture(t, func(*http.Request) (*http.Response, error) { return jsonResponse(200, answer), nil })
		var calls []call
		_, err := f.env(&calls).confirmed(categoriesCreate.ID, "creator", `{"team_id":"`+teamA+`","display_name":"X"}`)
		if classOf(err) != "invalid-provider-response" || !strings.Contains(err.Error(), "may have been created") {
			t.Fatalf("%s: err = %v (%s)", name, err, classOf(err))
		}
	}
}

func TestCategoriesUpdateKeepsUnreachableChannelsAndOtherPropertiesAndHidesThem(t *testing.T) {
	var put string
	f := newCategoryFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != catBase+"/"+catCustom {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, put), nil
	})
	var calls []call
	env := f.env(&calls)
	put = categoryWith(catCustom, ownUser, teamA, "Renamed", "custom", chanC, chanD, chanE, hiddenCh)
	args := `{"team_id":"` + teamA + `","category_id":"` + catCustom + `","display_name":"Renamed","channel_ids":["` +
		chanC + `"]}`
	if _, err := env.invoke(categoriesUpdate.ID, "editor", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	result, err := env.confirmed(categoriesUpdate.ID, "editor", args)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	for _, hidden := range []string{chanD, chanE, hiddenCh, chanB} {
		if strings.Contains(result, hidden) {
			t.Fatalf("result = %s, want %s never shown", result, hidden)
		}
	}
	if !strings.Contains(result, `"display_name":"Renamed"`) || !strings.Contains(result, chanC) {
		t.Fatalf("result = %s", result)
	}
	last := calls[len(calls)-1]
	want := `{"id":"` + catCustom + `","user_id":"` + ownUser + `","team_id":"` + teamA +
		`","display_name":"Renamed","type":"custom","sorting":"manual","muted":true,"collapsed":true,` +
		`"channel_ids":["` + chanC + `","` + chanD + `","` + chanE + `","` + hiddenCh + `"]}`
	if changes(calls) != 1 || last.method != http.MethodPut || last.body != want {
		t.Fatalf("calls = %+v, want exactly one PUT last with body %s", calls, want)
	}
}

func TestCategoriesUpdateWithAnAllowListWritesBackChannelsOutsideIt(t *testing.T) {
	f := newCategoryFixture(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, f2(r)), nil
	})
	f.categories[catCustom] = categoryWith(catCustom, ownUser, teamA, "Mine", "custom", chanC, chanA)
	var calls []call
	env := f.env(&calls)
	args := `{"team_id":"` + teamA + `","category_id":"` + catCustom + `","channel_ids":["` + chanA + `"]}`
	result, err := env.confirmed(categoriesUpdate.ID, "editorch", args)
	if err != nil || strings.Contains(result, chanC) {
		t.Fatalf("result = %s, %v, want chanC absent from the answer", result, err)
	}
	if last := calls[len(calls)-1]; changes(calls) != 1 ||
		!strings.Contains(last.body, `"channel_ids":["`+chanA+`","`+chanC+`"]`) ||
		!strings.Contains(last.body, `"display_name":"Mine"`) {
		t.Fatalf("calls = %+v, want chanC written back unchanged", calls)
	}

	// A channel outside the allow-list cannot be named, refused before the secret is read.
	calls, *env.reads = nil, 0
	args = `{"team_id":"` + teamA + `","category_id":"` + catCustom + `","channel_ids":["` + chanC + `"]}`
	if _, err := env.confirmed(categoriesUpdate.ID, "editorch", args); !isInvalidRequest(err) || len(calls) != 0 ||
		*env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want a local refusal", err, calls, *env.reads)
	}
}

// f2 answers an update with the category as the request wrote it.
func f2(r *http.Request) string {
	var body categoryJSON
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	out, _ := json.Marshal(body)
	return string(out)
}

func TestCategoriesUpdateRefusesBeforeThePut(t *testing.T) {
	var calls []call
	env := newCategoryFixture(t, nil).env(&calls)
	prefix := `{"team_id":"` + teamA + `","category_id":"`
	// Local refusals: no secret, no request.
	for name, args := range map[string]string{
		"no field":      prefix + catCustom + `"}`,
		"slash":         prefix + `a/b"}`,
		"dots":          prefix + `../x","display_name":"X"}`,
		"query":         prefix + `x?y=1","display_name":"X"}`,
		"empty":         prefix + `","display_name":"X"}`,
		"foreign team":  `{"team_id":"` + teamB + `","category_id":"` + catCustom + `","display_name":"X"}`,
		"bad name":      prefix + catCustom + `","display_name":""}`,
		"repeated":      prefix + catCustom + `","channel_ids":["` + chanA + `","` + chanA + `"]}`,
		"malformed id":  prefix + catCustom + `","channel_ids":["../x"]}`,
		"upper case id": prefix + `CAT","display_name":"X"}`,
	} {
		if _, err := env.confirmed(categoriesUpdate.ID, "editor", args); !isInvalidRequest(err) || len(calls) != 0 ||
			*env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d", name, err, calls, *env.reads)
		}
	}
	// A system category keeps its name.
	_, err := env.confirmed(categoriesUpdate.ID, "editor", prefix+catFavorites+`","display_name":"Renamed"}`)
	if !isInvalidRequest(err) || changes(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want the rename refused", err, calls)
	}
	// Channels that are not reachable live.
	for _, id := range []string{chanB, chanD, chanE, hiddenCh} {
		calls = nil
		_, err := env.confirmed(categoriesUpdate.ID, "editor", prefix+catCustom+`","channel_ids":["`+id+`"]}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), id) || changes(calls) != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", id, err, calls)
		}
	}
	// A category of another user or team is not accepted from the read.
	for _, id := range []string{catOther, "catteamb"} {
		calls = nil
		_, err := env.confirmed(categoriesUpdate.ID, "editor", prefix+id+`","display_name":"X"}`)
		if classOf(err) != "invalid-provider-response" || changes(calls) != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", id, err, calls)
		}
	}
}

func TestCategoriesUpdateAcceptsChannelsOfASystemCategory(t *testing.T) {
	f := newCategoryFixture(t, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, f2(r)), nil
	})
	var calls []call
	args := `{"team_id":"` + teamA + `","category_id":"` + catFavorites + `","channel_ids":["` + chanA + `"]}`
	result, err := f.env(&calls).confirmed(categoriesUpdate.ID, "editor", args)
	if err != nil || !strings.Contains(result, `"type":"favorites"`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	// chanB is not reachable and stays in the category.
	if last := calls[len(calls)-1]; !strings.Contains(last.body, `"channel_ids":["`+chanA+`","`+chanB+`"]`) {
		t.Fatalf("body = %s", last.body)
	}
}

func TestCategoriesDeleteNeedsToolListConfirmationAndACustomCategory(t *testing.T) {
	var f *categoryFixture
	f = newCategoryFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodDelete || r.URL.Path != catBase+"/"+catCustom {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(200, f.categories[catCustom]), nil
	})
	var calls []call
	env := f.env(&calls)
	args := `{"team_id":"` + teamA + `","category_id":"` + catCustom + `"}`
	if _, err := env.confirmed(categoriesDelete.ID, "nodelete", args); err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want a refusal without the tool in the tools list", err, calls)
	}
	if _, err := env.invoke(categoriesDelete.ID, "catdel", args); !isConfirmationRequired(err) || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want confirmation-required before any request", err, calls)
	}
	// A system category, another user's category, and a malformed ID are refused before the DELETE.
	for _, id := range []string{catFavorites, catOther, "a/b", "../x"} {
		calls = nil
		_, err := env.confirmed(categoriesDelete.ID, "catdel", `{"team_id":"`+teamA+`","category_id":"`+id+`"}`)
		if err == nil || changes(calls) != 0 {
			t.Fatalf("%s: err = %v, calls = %+v", id, err, calls)
		}
	}
	calls = nil
	result, err := env.confirmed(categoriesDelete.ID, "catdel", args)
	if err != nil || !strings.Contains(result, `"deleted":true`) {
		t.Fatalf("result = %s, %v", result, err)
	}
	if changes(calls) != 1 || calls[len(calls)-1].method != http.MethodDelete || calls[len(calls)-1].body != "" {
		t.Fatalf("calls = %+v, want reads and then exactly one DELETE", calls)
	}
}

func TestCategoryChangesAreNeverRetriedAfterAnUnclearResult(t *testing.T) {
	for _, c := range []struct {
		operation, connection, arguments, method, hint string
	}{
		{categoriesCreate.ID, "creator", `{"team_id":"` + teamA + `","display_name":"X"}`, http.MethodPost,
			"may have been created"},
		{categoriesUpdate.ID, "editor", `{"team_id":"` + teamA + `","category_id":"` + catCustom +
			`","display_name":"X"}`, http.MethodPut, "category may have been changed"},
		{categoriesDelete.ID, "catdel", `{"team_id":"` + teamA + `","category_id":"` + catCustom + `"}`,
			http.MethodDelete, "may have been deleted"},
	} {
		for name, fail := range map[string]func(*http.Request) (*http.Response, error){
			"5xx": func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"`+messageCanary+`"}`), nil
			},
			"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{not json`), nil },
		} {
			var calls []call
			env := newCategoryFixture(t, fail).env(&calls)
			_, err := env.confirmed(c.operation, c.connection, c.arguments)
			if err == nil || !strings.Contains(err.Error(), c.hint) || strings.Contains(err.Error(), messageCanary) {
				t.Fatalf("%s %s: err = %v", c.operation, name, err)
			}
			if countMethod(calls, c.method) != 1 || changes(calls) != 1 || calls[len(calls)-1].method != c.method {
				t.Fatalf("%s %s: calls = %+v, want exactly one change request", c.operation, name, calls)
			}
		}
	}
}

func TestCategoriesUpdateRejectsAnAnswerForAnotherCategory(t *testing.T) {
	f := newCategoryFixture(t, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, categoryWith(catOther, ownUser, teamA, "X", "custom")), nil
	})
	var calls []call
	_, err := f.env(&calls).confirmed(categoriesUpdate.ID, "editor",
		`{"team_id":"`+teamA+`","category_id":"`+catCustom+`","display_name":"X"}`)
	if classOf(err) != "invalid-provider-response" || !strings.Contains(err.Error(), "may have been changed") {
		t.Fatalf("err = %v (%s)", err, classOf(err))
	}
}
