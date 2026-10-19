package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	pollFeatures = `{"version":{"string":"31"},"capabilities":{"spreed":{"features":["chat-v2","talk-polls"]}}}`
	pollPath     = "/ocs/v2.php/apps/spreed/api/v1/poll/" + boundRoom
)

func pollJSON(status, maxVotes int, extra string) string {
	return `{"id":7,"question":"Which day?","options":["Monday","Tuesday","Wednesday"],"votes":[],"actorType":"users",` +
		`"actorId":"alice","actorDisplayName":"Alice","status":` + strconv.Itoa(status) + `,"resultMode":0,"maxVotes":` +
		strconv.Itoa(maxVotes) + `,"votedSelf":[]` + extra + `}`
}

// pollCore is a core that binds one conversation and lists the given poll tools.
func pollCore(t *testing.T, tools ...string) *application.Core {
	t.Helper()
	cfg := coreConfig()
	connection := cfg.Connections["reports"]
	connection.Target = ""
	connection.Targets = []string{"talk/" + boundRoom}
	connection.Effects = []config.Effect{config.EffectRead, config.EffectCreate,
		config.EffectUpdate, config.EffectDelete}
	connection.Tools = tools
	cfg.Connections["reports"] = connection
	red := &redact.Redactor{}
	return application.New(registry(t), cfg, resolver(red), red)
}

func allPollTools() []string {
	return []string{talkPollsGet.ID, talkPollsCreate.ID, talkPollsVote.ID, talkPollsClose.ID}
}

func pollInvoke(t *testing.T, operation, args string, confirmed bool) (application.InvokeResponse, error) {
	t.Helper()
	return pollCore(t, allPollTools()...).Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: "reports", Arguments: json.RawMessage(args), Confirmed: confirmed,
	})
}

// pollServer answers the capability read with the given features, a read of poll 7 with the given poll, and
// any other request with the given answer.
func pollServer(t *testing.T, features, poll string, answer func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == "/ocs/v2.php/cloud/capabilities":
			return ocsResponse(http.StatusOK, ocsData(features)), nil
		case request.Method == http.MethodGet && request.URL.Path == pollPath+"/7":
			return ocsResponse(http.StatusOK, ocsData(poll)), nil
		}
		return answer(request)
	})
}

func pollOK(request *http.Request) (*http.Response, error) {
	return ocsResponse(http.StatusCreated, ocsData(pollJSON(0, 1, ""))), nil
}

type pollCase struct {
	name, operation, args, method, path, body string
	reads                                     int
}

func pollCases() []pollCase {
	return []pollCase{
		{"create", "nextcloud.talk.polls.create",
			talkArgs(boundRoom, `,"question":"Which day?","options":["Monday","Tuesday"],"result_mode":"hidden","max_votes":2`),
			http.MethodPost, pollPath,
			`maxVotes=2&options%5B%5D=Monday&options%5B%5D=Tuesday&question=Which+day%3F&resultMode=1`, 1},
		{"create defaults", "nextcloud.talk.polls.create",
			talkArgs(boundRoom, `,"question":"Which day?","options":["Monday","Tuesday"]`),
			http.MethodPost, pollPath,
			`maxVotes=1&options%5B%5D=Monday&options%5B%5D=Tuesday&question=Which+day%3F&resultMode=0`, 1},
		{"vote", "nextcloud.talk.polls.vote", talkArgs(boundRoom, `,"poll_id":"7","option_ids":[0,2]`),
			http.MethodPost, pollPath + "/7", `optionIds%5B%5D=0&optionIds%5B%5D=2`, 1},
		{"close", "nextcloud.talk.polls.close", talkArgs(boundRoom, `,"poll_id":"7"`),
			http.MethodDelete, pollPath + "/7", ``, 1},
	}
}

func TestTalkPollWritesNeedConfirmationAndSendOneRequest(t *testing.T) {
	for _, c := range pollCases() {
		t.Run(c.name, func(t *testing.T) {
			refuse(t)
			if _, err := pollInvoke(t, c.operation, c.args, false); err == nil || !strings.Contains(err.Error(), "confirm") {
				t.Fatal("ran without confirm")
			}
			calls := pollServer(t, pollFeatures, pollJSON(0, 2, ""), pollOK)
			response, err := pollInvoke(t, c.operation, c.args, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(*calls) != 2 {
				t.Fatalf("calls = %+v", *calls)
			}
			// The one read before the change is the capability for create and close, the poll for vote.
			read := (*calls)[0].url.Path
			if wantRead := "/ocs/v2.php/cloud/capabilities"; c.name == "vote" && read != pollPath+"/7" || c.name != "vote" && read != wantRead {
				t.Errorf("read = %s", read)
			}
			got := (*calls)[1]
			if got.method != c.method || got.url.Path != c.path || got.body != c.body {
				t.Errorf("request = %s %s %q", got.method, got.url.Path, got.body)
			}
			var poll Poll
			if err := json.Unmarshal(response.Result, &poll); err != nil || poll.ID != "7" || poll.Question != "Which day?" ||
				len(poll.Options) != 3 || poll.Options[1].ID != 1 || poll.Options[1].Text != "Tuesday" {
				t.Errorf("result = %s, %v", response.Result, err)
			}
		})
	}
}

func TestTalkPollGetReportsOptionsVotesAndVoters(t *testing.T) {
	extra := `,"votes":{"option-0":2,"option-2":1},"numVoters":3,"votedSelf":[2],` +
		`"details":[{"actorType":"users","actorId":"bob","actorDisplayName":"Bob","optionId":0},` +
		`{"actorType":"users","actorId":"cy","actorDisplayName":"Cy","optionId":2}],"avatar":"` + bodyCanary + `"`
	poll := strings.Replace(pollJSON(1, 2, ""), `"votes":[],`, "", 1)
	poll = strings.Replace(poll, `"votedSelf":[]`, extra[1:], 1)
	calls := pollServer(t, pollFeatures, poll, nil)
	response, err := pollInvoke(t, "nextcloud.talk.polls.get", talkArgs(boundRoom, `,"poll_id":"7"`), false)
	if err != nil {
		t.Fatal(err)
	}
	var got Poll
	if err := json.Unmarshal(response.Result, &got); err != nil || got.Status != "closed" || got.MaxVotes != 2 ||
		got.Options[0].Votes == nil || *got.Options[0].Votes != 2 || got.Options[1].Votes != nil || *got.Options[2].Votes != 1 ||
		got.NumVoters == nil || *got.NumVoters != 3 || len(got.VotedSelf) != 1 || got.VotedSelf[0] != 2 ||
		len(got.Voters) != 2 || got.Voters[0].ActorName != "Bob" || got.Voters[1].OptionID != 2 || got.AuthorName != "Alice" {
		t.Errorf("result = %s, %v", response.Result, err)
	}
	if strings.Contains(string(response.Result), bodyCanary) || len(*calls) != 2 {
		t.Errorf("result = %s, calls = %d", response.Result, len(*calls))
	}
}

func TestTalkPollGetBoundsWhatItReports(t *testing.T) {
	options := make([]string, 120)
	for i := range options {
		options[i] = "o"
	}
	raw, _ := json.Marshal(map[string]any{"id": 7, "question": strings.Repeat("q", 5000), "options": options,
		"status": 0, "resultMode": 0, "maxVotes": 1})
	pollServer(t, pollFeatures, string(raw), nil)
	response, err := pollInvoke(t, "nextcloud.talk.polls.get", talkArgs(boundRoom, `,"poll_id":"7"`), false)
	if err != nil {
		t.Fatal(err)
	}
	var got Poll
	if err := json.Unmarshal(response.Result, &got); err != nil || !got.Truncated || len(got.Options) != 100 ||
		len(got.Question) != 4096 {
		t.Errorf("options = %d, question = %d, truncated = %v, err = %v", len(got.Options), len(got.Question), got.Truncated, err)
	}
}

func TestTalkPollsRefuseUnusableArgumentsBeforeAnyIO(t *testing.T) {
	refuse(t)
	many := `"` + strings.Join(strings.Split(strings.Repeat("o,", 11), ",")[:11], `","`) + `"`
	cases := map[string][2]string{
		"unbound room":       {"nextcloud.talk.polls.create", talkArgs(foreignRoom, `,"question":"q","options":["a","b"]`)},
		"get unbound":        {"nextcloud.talk.polls.get", talkArgs(foreignRoom, `,"poll_id":"7"`)},
		"vote unbound":       {"nextcloud.talk.polls.vote", talkArgs(foreignRoom, `,"poll_id":"7","option_ids":[0]`)},
		"close unbound":      {"nextcloud.talk.polls.close", talkArgs(foreignRoom, `,"poll_id":"7"`)},
		"blank question":     {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"  ","options":["a","b"]`)},
		"long question":      {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"`+strings.Repeat("q", 501)+`","options":["a","b"]`)},
		"one option":         {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"q","options":["a"]`)},
		"eleven options":     {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"q","options":[`+many+`]`)},
		"blank option":       {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"q","options":["a"," "]`)},
		"long option":        {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"q","options":["a","`+strings.Repeat("o", 201)+`"]`)},
		"unknown mode":       {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"q","options":["a","b"],"result_mode":"secret"`)},
		"more votes than o":  {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"q","options":["a","b"],"max_votes":3`)},
		"negative max votes": {"nextcloud.talk.polls.create", talkArgs(boundRoom, `,"question":"q","options":["a","b"],"max_votes":-1`)},
		"poll id":            {"nextcloud.talk.polls.get", talkArgs(boundRoom, `,"poll_id":"7/../8"`)},
		"vote poll id":       {"nextcloud.talk.polls.vote", talkArgs(boundRoom, `,"poll_id":"x","option_ids":[0]`)},
		"no options":         {"nextcloud.talk.polls.vote", talkArgs(boundRoom, `,"poll_id":"7","option_ids":[]`)},
		"negative option":    {"nextcloud.talk.polls.vote", talkArgs(boundRoom, `,"poll_id":"7","option_ids":[-1]`)},
		"duplicate option":   {"nextcloud.talk.polls.vote", talkArgs(boundRoom, `,"poll_id":"7","option_ids":[1,1]`)},
		"option as text":     {"nextcloud.talk.polls.vote", talkArgs(boundRoom, `,"poll_id":"7","option_ids":["Monday"]`)},
		"close poll id":      {"nextcloud.talk.polls.close", talkArgs(boundRoom, `,"poll_id":""`)},
	}
	for name, c := range cases {
		_, err := pollInvoke(t, c[0], c[1], true)
		if err == nil || strings.Contains(err.Error(), foreignRoom) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, targets := range [][]string{{"folder/Reports"}, {"account"}} {
		cfg := coreConfig()
		connection := cfg.Connections["reports"]
		connection.Target, connection.Targets = "", targets
		connection.Effects = []config.Effect{config.EffectRead, config.EffectCreate}
		connection.Tools = allPollTools()
		cfg.Connections["reports"] = connection
		red := &redact.Redactor{}
		_, err := application.New(registry(t), cfg, resolver(red), red).Invoke(context.Background(), application.InvokeRequest{
			Operation: "nextcloud.talk.polls.create", Connection: "reports", Confirmed: true,
			Arguments: json.RawMessage(talkArgs(boundRoom, `,"question":"q","options":["a","b"]`)),
		})
		if err == nil || strings.Contains(err.Error(), boundRoom) {
			t.Errorf("%v: err = %v", targets, err)
		}
	}
}

func TestTalkPollVoteSendsOnlyOptionsOfThePoll(t *testing.T) {
	cases := map[string]struct {
		poll, args, want string
	}{
		"foreign option": {pollJSON(0, 2, ""), `[0,3]`, "not an option"},
		"far option":     {pollJSON(0, 2, ""), `[99]`, "not an option"},
		"too many votes": {pollJSON(0, 1, ""), `[0,1]`, "more options"},
		"closed poll":    {pollJSON(1, 1, ""), `[0]`, "closed"},
		"unknown status": {strings.Replace(pollJSON(0, 1, ""), `"status":0`, `"status":2`, 1), `[0]`, "closed"},
	}
	for name, c := range cases {
		calls := pollServer(t, pollFeatures, c.poll, func(request *http.Request) (*http.Response, error) {
			t.Errorf("%s: sent %s %s", name, request.Method, request.URL.Path)
			return pollOK(request)
		})
		_, err := pollInvoke(t, "nextcloud.talk.polls.vote", talkArgs(boundRoom, `,"poll_id":"7","option_ids":`+c.args), true)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(*calls) != 1 || (*calls)[0].method != http.MethodGet {
			t.Errorf("%s: calls = %+v, want the one read", name, *calls)
		}
	}
	// A poll that allows any number of votes takes all of its options.
	calls := pollServer(t, pollFeatures, pollJSON(0, 0, ""), pollOK)
	if _, err := pollInvoke(t, "nextcloud.talk.polls.vote", talkArgs(boundRoom, `,"poll_id":"7","option_ids":[0,1,2]`), true); err != nil || len(*calls) != 2 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestTalkPollsNeedTheirCapabilityFeature(t *testing.T) {
	for _, c := range pollCases() {
		if c.name == "vote" || c.name == "create defaults" {
			continue
		}
		args := c.args
		calls := serve(t, func(request *http.Request) (*http.Response, error) {
			return ocsResponse(http.StatusOK, ocsData(`{"capabilities":{"spreed":{"features":["chat-v2"]}}}`)), nil
		})
		if _, err := pollInvoke(t, c.operation, args, true); err == nil || len(*calls) != 1 {
			t.Errorf("%s: err = %v, calls = %d", c.name, err, len(*calls))
		}
	}
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		return ocsResponse(http.StatusOK, ocsData(`{"capabilities":{"spreed":{"features":["chat-v2"]}}}`)), nil
	})
	if _, err := pollInvoke(t, "nextcloud.talk.polls.get", talkArgs(boundRoom, `,"poll_id":"7"`), false); err == nil || len(*calls) != 1 {
		t.Errorf("get: err = %v, calls = %d", err, len(*calls))
	}
}

func TestTalkPollWritesClassifyFailures(t *testing.T) {
	for _, c := range pollCases() {
		t.Run(c.name, func(t *testing.T) {
			answers := map[string]struct {
				answer    func(*http.Request) (*http.Response, error)
				class     provider.Class
				uncertain bool
			}{
				"429":  {func(*http.Request) (*http.Response, error) { return status(429), nil }, provider.ClassRateLimited, false},
				"500":  {func(*http.Request) (*http.Response, error) { return status(500), nil }, provider.ClassProviderError, true},
				"503":  {func(*http.Request) (*http.Response, error) { return status(503), nil }, provider.ClassUnreachable, true},
				"drop": {func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") }, "", true},
				"body": {func(*http.Request) (*http.Response, error) {
					return ocsResponse(http.StatusOK, `{"nope":true}`), nil
				}, provider.ClassInvalidResponse, true},
				"403": {func(*http.Request) (*http.Response, error) { return status(403), nil }, provider.ClassPermission, false},
				"400": {func(*http.Request) (*http.Response, error) { return status(400), nil }, provider.ClassProviderError, false},
				"412": {func(*http.Request) (*http.Response, error) { return status(412), nil }, provider.ClassProviderError, false},
				"404": {func(*http.Request) (*http.Response, error) { return status(404), nil }, provider.ClassNotFound, false},
				"302": {func(*http.Request) (*http.Response, error) { return status(302), nil }, provider.ClassProviderError, false},
			}
			for name, a := range answers {
				calls := pollServer(t, pollFeatures, pollJSON(0, 2, ""), a.answer)
				_, err := pollInvoke(t, c.operation, c.args, true)
				if err == nil {
					t.Fatalf("%s: no error", name)
				}
				text := err.Error()
				var providerErr *provider.Error
				if a.class != "" && (!errors.As(err, &providerErr) || providerErr.Class != a.class) {
					t.Errorf("%s: err = %v", name, err)
				}
				if has := strings.Contains(text, "may have been"); has != a.uncertain {
					t.Errorf("%s: hint present = %v in %q", name, has, text)
				}
				if strings.Contains(text, "Location") || strings.Contains(text, "redirect") && name != "302" {
					t.Errorf("%s: %q", name, text)
				}
				for _, leaked := range []string{bodyCanary, boundRoom} {
					if strings.Contains(text, leaked) {
						t.Errorf("%s: leaked %q in %q", name, leaked, text)
					}
				}
				if len(*calls) != 2 {
					t.Errorf("%s: %d requests, want one read and one change", name, len(*calls))
				}
			}
		})
	}
}

func TestTalkPollGetReadFailuresNameNoTarget(t *testing.T) {
	for _, code := range []int{302, 404, 412, 429, 500} {
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/ocs/v2.php/cloud/capabilities" {
				return ocsResponse(http.StatusOK, ocsData(pollFeatures)), nil
			}
			return status(code), nil
		})
		_, err := pollInvoke(t, "nextcloud.talk.polls.get", talkArgs(boundRoom, `,"poll_id":"7"`), false)
		if err == nil || strings.Contains(err.Error(), boundRoom) || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%d: err = %v", code, err)
		}
	}
}

func TestTalkPollCloseNeedsAToolsList(t *testing.T) {
	refuse(t)
	core := pollCore(t, talkPollsGet.ID, talkPollsCreate.ID, talkPollsVote.ID)
	_, err := core.Invoke(context.Background(), application.InvokeRequest{
		Operation: "nextcloud.talk.polls.close", Connection: "reports", Confirmed: true,
		Arguments: json.RawMessage(talkArgs(boundRoom, `,"poll_id":"7"`)),
	})
	if err == nil {
		t.Fatal("close ran without being listed")
	}
	if !talkPollsClose.RequiresToolAllowList || talkPollsClose.Risk.Effect != "delete" ||
		talkPollsCreate.RequiresToolAllowList || talkPollsVote.RequiresToolAllowList ||
		talkPollsCreate.Risk.Effect != "create" || talkPollsVote.Risk.Effect != "update" ||
		!talkPollsCreate.Risk.OpenWorld || talkPollsGet.Risk.Effect != "read" {
		t.Errorf("risks = %+v %+v %+v", talkPollsCreate.Risk, talkPollsVote.Risk, talkPollsClose.Risk)
	}
}
