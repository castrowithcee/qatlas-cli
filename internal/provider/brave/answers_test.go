package brave

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// questionCanary stands for the question, which no error may quote.
const questionCanary = "canary-question-7b1d"

// chunk is one event of an answer stream as Brave sends it, with content and an optional finish reason.
func chunk(content string, finish string) string {
	choice := map[string]any{"delta": map[string]any{"content": content}}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	encoded, _ := json.Marshal(map[string]any{"id": "chatcmpl-1", "object": "chat.completion.chunk",
		"created": 1760000000, "model": "brave-pro", "choices": []any{choice}})
	return string(encoded)
}

// streamReply answers with the events, each flushed on its own, and the closing marker when done is set.
func streamReply(headers map[string]string, events []string, done bool) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(http.StatusOK)
		for _, event := range events {
			fmt.Fprint(w, "data: "+event+"\n\n")
			w.(http.Flusher).Flush()
		}
		if done {
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}
}

// answerEvents is a stream of text fragments in which a citation and the usage tag each arrive in several
// fragments, between a role chunk and a closing chunk with the usage counts.
func answerEvents() []string {
	return []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1760000000,"model":"brave-pro",` +
			`"system_fingerprint":"fp-1","choices":[{"delta":{"role":"assistant","content":""}}]}`,
		chunk("Second highest", ""),
		chunk(" is K2.", ""),
		chunk(`<citation>{"start_index":0,`, ""),
		chunk(`"end_index":14,"number":1,"url":"https://en.wikipedia.org/wiki/K2","favicon":`+
			`"https://imgs.search.brave.com/fav.png","snippet":"K2 is the second`, ""),
		chunk(` highest mountain"}</cita`, ""),
		chunk(`tion>`, ""),
		chunk(`<citation>{"number":2,"url":"javascript:alert(1)","favicon":"ftp://x.example/f.ico"}</citation>`, ""),
		chunk(` See <entity>K2</entity> and <unknown>x</unknown> <citation>not json</citation>.`, ""),
		chunk(`<usage>{"X-Request-Requests":1,"X-Request-Queries":2,"X-Request-Tokens-In":1234,`, ""),
		chunk(`"X-Request-Tokens-Out":300,"X-Request-Requests-Cost":0,"X-Request-Queries-Cost":0.008,`+
			`"X-Request-Tokens-In-Cost":0.00617,"X-Request-Tokens-Out-Cost":0.0015,"X-Request-Total-Cost":0.01567}`+
			`</usage>`, ""),
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1760000000,"model":"brave-pro",` +
			`"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1234,"completion_tokens":300,` +
			`"total_tokens":1534,"completion_tokens_details":{"reasoning_tokens":0}}}`,
	}
}

var answerRate = map[string]string{"X-RateLimit-Limit": "2, 15000", "X-RateLimit-Policy": "2;w=1, 15000;w=2592000",
	"X-RateLimit-Remaining": "1, 14000", "X-RateLimit-Reset": "1, 1234567"}

func decoded(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("not JSON: %v: %s", err, text)
	}
	return value
}

func TestAnswersAssemblesTheStream(t *testing.T) {
	f := &fakeBrave{answer: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for name, value := range answerRate {
			w.Header().Set(name, value)
		}
		// Comments and other fields of the event-stream format carry nothing.
		fmt.Fprint(w, ": keep-alive\n\nevent: message\nid: 7\n")
		streamReply(nil, answerEvents(), true)(w, r)
		fmt.Fprint(w, "data: "+chunk("after the end", "")+"\n\n")
	}}
	env := newEnvironment(t, f)
	result, err := env.invoke("brave.answers.ask", "answers", `{"question":"What is the second highest mountain?",`+
		`"model":"brave","max_completion_tokens":1024,"search_context_size":"high",`+
		`"user_location":{"city":"Munich","country":"DE","region":"Bavaria","timezone":"Europe/Berlin"},`+
		`"country":"de","language":"de","safesearch":"strict","enable_citations":true,"enable_entities":true,`+
		`"enable_research":false}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	want := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1760000000,"model":"brave-pro",` +
		`"system_fingerprint":"fp-1","role":"assistant",` +
		`"answer":"Second highest is K2. See <entity>K2</entity> and <unknown>x</unknown> <citation>not json</citation>.",` +
		`"citations":[{"start_index":0,"end_index":14,"number":1,"url":"https://en.wikipedia.org/wiki/K2",` +
		`"favicon":"https://imgs.search.brave.com/fav.png","snippet":"K2 is the second highest mountain"},` +
		`{"number":2}],` +
		`"usage":{"prompt_tokens":1234,"completion_tokens":300,"total_tokens":1534,` +
		`"completion_tokens_details":{"reasoning_tokens":0}},` +
		`"cost":{"requests":1,"queries":2,"tokens_in":1234,"tokens_out":300,"requests_cost":0,"queries_cost":0.008,` +
		`"tokens_in_cost":0.00617,"tokens_out_cost":0.0015,"total_cost":0.01567},"finish_reason":"stop",` +
		`"quota":{"windows":[{"window_seconds":1,"limit":2,"remaining":1,"reset_seconds":1},` +
		`{"window_seconds":2592000,"limit":15000,"remaining":14000,"reset_seconds":1234567}]}}`
	if got := decoded(t, result); !reflect.DeepEqual(got, decoded(t, want)) {
		t.Errorf("result = %s\nwant     %s", result, want)
	}

	requests := f.recorded()
	if len(requests) != 1 {
		t.Fatalf("%d requests, want 1", len(requests))
	}
	r := requests[0]
	if r.method != http.MethodPost || r.path != "/res/v1/chat/completions" || r.rawQuery != "" ||
		r.header.Get("X-Subscription-Token") != tokenValue || r.header.Get("Accept") != "text/event-stream" ||
		r.header.Get("Content-Type") != "application/json" || r.header.Get("User-Agent") != "qatlas-cli" ||
		r.header.Get("Authorization") != "" {
		t.Errorf("request = %s %s?%s %v", r.method, r.path, r.rawQuery, r.header)
	}
	wantBody := `{"messages":[{"role":"user","content":"What is the second highest mountain?"}],"model":"brave",` +
		`"stream":true,"max_completion_tokens":1024,"web_search_options":{"search_context_size":"high",` +
		`"user_location":{"approximate":{"city":"Munich","country":"DE","region":"Bavaria",` +
		`"timezone":"Europe/Berlin"}}},"country":"de","language":"de","safesearch":"strict",` +
		`"enable_entities":true,"enable_citations":true,"enable_research":false}`
	if !reflect.DeepEqual(decoded(t, r.body), decoded(t, wantBody)) {
		t.Errorf("body = %s\nwant   %s", r.body, wantBody)
	}
	if strings.Contains(r.body, tokenValue) || strings.Contains(result, tokenValue) {
		t.Error("the key reached the body or the result")
	}
}

func TestAnswersSendsResearchWithinTheLimit(t *testing.T) {
	for _, tc := range []struct {
		name, arguments, body string
	}{
		{"defaults", `{"question":"  Compare fusion approaches\n"}`,
			`{"messages":[{"role":"user","content":"Compare fusion approaches"}],"model":"brave-pro","stream":true}`},
		{"research default seconds", `{"question":"q","enable_research":true}`,
			`{"messages":[{"role":"user","content":"q"}],"model":"brave-pro","stream":true,"enable_research":true,` +
				`"research_maximum_number_of_seconds":40}`},
		{"every research argument", `{"question":"q","enable_research":true,"enable_citations":false,` +
			`"research_maximum_number_of_tokens_per_query":1024,"research_maximum_number_of_queries":50,` +
			`"research_maximum_number_of_iterations":5,"research_maximum_number_of_seconds":50,` +
			`"research_maximum_number_of_results_per_query":60}`,
			`{"messages":[{"role":"user","content":"q"}],"model":"brave-pro","stream":true,"enable_citations":false,` +
				`"enable_research":true,"research_maximum_number_of_tokens_per_query":1024,` +
				`"research_maximum_number_of_queries":50,"research_maximum_number_of_iterations":5,` +
				`"research_maximum_number_of_seconds":50,"research_maximum_number_of_results_per_query":60}`},
		{"location only", `{"question":"q","user_location":{"timezone":"Europe/Berlin"}}`,
			`{"messages":[{"role":"user","content":"q"}],"model":"brave-pro","stream":true,` +
				`"web_search_options":{"user_location":{"approximate":{"timezone":"Europe/Berlin"}}}}`},
	} {
		f := &fakeBrave{answer: streamReply(nil, []string{chunk("ok", "stop")}, true)}
		env := newEnvironment(t, f)
		result, err := env.invoke("brave.answers.ask", "answers", tc.arguments)
		if err != nil {
			t.Errorf("%s: invoke = %v", tc.name, err)
			continue
		}
		if !strings.Contains(result, `"answer":"ok"`) {
			t.Errorf("%s: result = %s", tc.name, result)
		}
		if requests := f.recorded(); len(requests) != 1 ||
			!reflect.DeepEqual(decoded(t, requests[0].body), decoded(t, tc.body)) {
			t.Errorf("%s: requests = %+v, want body %s", tc.name, requests, tc.body)
		}
	}
}

func TestAnswersRefusesArgumentsBeforeTheKey(t *testing.T) {
	for name, arguments := range map[string]string{
		"no question":               `{}`,
		"blank question":            `{"question":"   "}`,
		"long question":             `{"question":"` + strings.Repeat("a", 4001) + `"}`,
		"control character":         `{"question":"` + questionCanary + `\u0007"}`,
		"messages":                  `{"question":"` + questionCanary + `","messages":[{"role":"user","content":"x"},{"role":"user","content":"y"}]}`,
		"stream":                    `{"question":"` + questionCanary + `","stream":false}`,
		"metadata":                  `{"question":"` + questionCanary + `","metadata":{"a":"b"}}`,
		"model":                     `{"question":"` + questionCanary + `","model":"gpt-4"}`,
		"context size":              `{"question":"` + questionCanary + `","search_context_size":"huge"}`,
		"safesearch":                `{"question":"` + questionCanary + `","safesearch":"none"}`,
		"country":                   `{"question":"` + questionCanary + `","country":"DEU"}`,
		"language":                  `{"question":"` + questionCanary + `","language":"german language"}`,
		"location member":           `{"question":"` + questionCanary + `","user_location":{"street":"x"}}`,
		"location timezone":         `{"question":"` + questionCanary + `","user_location":{"timezone":"not a zone"}}`,
		"location padding":          `{"question":"` + questionCanary + `","user_location":{"city":" Munich"}}`,
		"completion tokens":         `{"question":"` + questionCanary + `","max_completion_tokens":0}`,
		"research seconds over 50":  `{"question":"` + questionCanary + `","enable_research":true,"research_maximum_number_of_seconds":51}`,
		"research seconds zero":     `{"question":"` + questionCanary + `","enable_research":true,"research_maximum_number_of_seconds":0}`,
		"research tokens":           `{"question":"` + questionCanary + `","enable_research":true,"research_maximum_number_of_tokens_per_query":1023}`,
		"research queries":          `{"question":"` + questionCanary + `","enable_research":true,"research_maximum_number_of_queries":51}`,
		"research iterations":       `{"question":"` + questionCanary + `","enable_research":true,"research_maximum_number_of_iterations":6}`,
		"research results":          `{"question":"` + questionCanary + `","enable_research":true,"research_maximum_number_of_results_per_query":61}`,
		"citations with research":   `{"question":"` + questionCanary + `","enable_research":true,"enable_citations":true}`,
		"entities with research":    `{"question":"` + questionCanary + `","enable_research":true,"enable_entities":true}`,
		"research without research": `{"question":"` + questionCanary + `","research_maximum_number_of_queries":3}`,
		"research switched off":     `{"question":"` + questionCanary + `","enable_research":false,"research_maximum_number_of_seconds":10}`,
	} {
		env := newEnvironment(t, &fakeBrave{})
		_, err := env.invoke("brave.answers.ask", "answers", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(env.fake.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, requests = %d, want a refusal before the key", name, err,
				*env.reads, len(env.fake.recorded()))
			continue
		}
		if strings.Contains(err.Error(), questionCanary) {
			t.Errorf("%s: the error quotes the question: %v", name, err)
		}
	}
	// The checks of the handler hold also where the schema is bypassed.
	for name, args := range map[string]answersArguments{
		"research seconds": {Question: "q", EnableResearch: pointer(true), answersResearch: answersResearch{Seconds: pointer(51)}},
		"citations":        {Question: "q", EnableResearch: pointer(true), EnableCitations: pointer(true)},
		"model":            {Question: "q", Model: "other"},
		"long":             {Question: strings.Repeat("a", 4001)},
		"country":          {Question: "q", Country: "D1"},
	} {
		if _, err := prepareAnswersAsk(args); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid request", name, err)
		}
	}
}

func pointer[T any](value T) *T { return &value }

func TestAnswersStreamFailuresAreNeverAnAnswer(t *testing.T) {
	var big []string
	for range answersMaxStreamBytes / (64 << 10) {
		big = append(big, chunk(strings.Repeat("a", 64<<10), ""))
	}
	gzipCut := func(w http.ResponseWriter, _ *http.Request) {
		var raw strings.Builder
		zip := gzip.NewWriter(&raw)
		for _, event := range answerEvents() {
			fmt.Fprint(zip, "data: "+event+"\n\n")
		}
		fmt.Fprint(zip, "data: [DONE]\n\n")
		zip.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		fmt.Fprint(w, raw.String()[:raw.Len()/2])
	}
	aborted := func(w http.ResponseWriter, r *http.Request) {
		streamReply(nil, answerEvents()[:3], false)(w, r)
		panic(http.ErrAbortHandler)
	}
	for _, tc := range []struct {
		name   string
		answer func(http.ResponseWriter, *http.Request)
		class  provider.Class
	}{
		{"no closing marker", streamReply(nil, answerEvents(), false), provider.ClassInvalidResponse},
		{"marker without its blank line", func(w http.ResponseWriter, r *http.Request) {
			streamReply(nil, answerEvents(), false)(w, r)
			fmt.Fprint(w, "data: [DONE]")
		}, provider.ClassInvalidResponse},
		{"aborted", aborted, provider.ClassInvalidResponse},
		{"cut in gzip", gzipCut, provider.ClassInvalidResponse},
		{"not a chunk", streamReply(nil, []string{chunk("x", ""), `{"choices":"` + detailCanary + `"}`}, true),
			provider.ClassInvalidResponse},
		{"not json", streamReply(nil, []string{"<html>" + detailCanary}, true), provider.ClassInvalidResponse},
		{"error event", streamReply(nil, []string{chunk("x", ""),
			`{"error":{"message":"` + detailCanary + `","type":"server_error"}}`}, true), provider.ClassProviderError},
		{"over the stream limit", streamReply(nil, big, true), provider.ClassInvalidResponse},
	} {
		env := newEnvironment(t, &fakeBrave{answer: tc.answer})
		result, err := env.invoke("brave.answers.ask", "answers", `{"question":"`+questionCanary+`"}`)
		if classOf(err) != tc.class || result != "" {
			t.Errorf("%s: class = %q (%v), result = %q, want %q and no answer", tc.name, classOf(err), err, result,
				tc.class)
			continue
		}
		text := err.Error()
		if !strings.Contains(text, "may have billed") || !strings.Contains(text, "does not repeat") {
			t.Errorf("%s: %q does not say that Brave may have billed the request", tc.name, text)
		}
		for _, leak := range []string{questionCanary, detailCanary, tokenValue, "api.search.brave.com"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s: error %q leaks %q", tc.name, text, leak)
			}
		}
		if n := len(env.fake.recorded()); n != 1 {
			t.Errorf("%s: %d requests, want exactly one", tc.name, n)
		}
	}
}

func TestAnswersEndAsATimeoutWithinTheInvokeLimit(t *testing.T) {
	if streamTimeout >= time.Minute || streamTimeout <= answersMaxResearchSeconds*time.Second {
		t.Fatalf("stream timeout %s must lie between the research limit and the 60 seconds of an invoke",
			streamTimeout)
	}
	previous := streamTimeout
	streamTimeout = 300 * time.Millisecond
	t.Cleanup(func() { streamTimeout = previous })
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"in the stream": func(w http.ResponseWriter, r *http.Request) {
			streamReply(nil, answerEvents()[:4], false)(w, r)
			<-r.Context().Done()
		},
		"before the answer": func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
	} {
		env := newEnvironment(t, &fakeBrave{answer: answer})
		start := time.Now()
		result, err := env.invoke("brave.answers.ask", "answers", `{"question":"`+questionCanary+`"}`)
		if classOf(err) != provider.ClassTimeout || result != "" || time.Since(start) > 10*time.Second {
			t.Errorf("%s: class = %q (%v), result = %q, want a timeout", name, classOf(err), err, result)
			continue
		}
		if text := err.Error(); !strings.Contains(text, "did not finish the answer") ||
			!strings.Contains(text, "may have billed") || strings.Contains(text, questionCanary) {
			t.Errorf("%s: %q", name, text)
		}
		if n := len(env.fake.recorded()); n != 1 {
			t.Errorf("%s: %d requests, want one and no retry", name, n)
		}
	}
}

func TestAnswersStatusesMapToClassesWithoutProviderText(t *testing.T) {
	for _, tc := range []struct {
		status int
		class  provider.Class
	}{
		{http.StatusBadRequest, provider.ClassInvalidResponse},
		{http.StatusPaymentRequired, provider.ClassPermission},
		{http.StatusForbidden, provider.ClassPermission},
		{http.StatusNotFound, provider.ClassNotFound},
		{http.StatusUnprocessableEntity, provider.ClassInvalidResponse},
		{http.StatusTooManyRequests, provider.ClassRateLimited},
		{http.StatusInternalServerError, provider.ClassProviderError},
		{http.StatusServiceUnavailable, provider.ClassUnreachable},
	} {
		env := newEnvironment(t, &fakeBrave{answer: reply(tc.status, nil, errorBody(tc.status))})
		_, err := env.invoke("brave.answers.ask", "answers", `{"question":"`+questionCanary+`"}`)
		if classOf(err) != tc.class {
			t.Errorf("HTTP %d: class = %q (%v), want %q", tc.status, classOf(err), err, tc.class)
			continue
		}
		var failed *provider.Error
		errors.As(err, &failed)
		for _, leak := range []string{detailCanary, tokenValue, questionCanary, "api.search.brave.com", "VALIDATION"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("HTTP %d: error %q leaks %q", tc.status, err, leak)
			}
		}
		if failed.Original == nil || failed.Original.Status != tc.status {
			t.Errorf("HTTP %d: original = %+v, want the status", tc.status, failed.Original)
		}
		if n := len(env.fake.recorded()); n != 1 {
			t.Errorf("HTTP %d: %d requests, want one and no retry", tc.status, n)
		}
	}
}

func TestAnswersAreOfferedOnlyByName(t *testing.T) {
	env := newEnvironment(t, &fakeBrave{answer: streamReply(nil, []string{chunk("ok", "stop")}, true)})
	for _, connection := range []string{"search", "listed"} {
		_, err := env.invoke("brave.answers.ask", connection, `{"question":"q"}`)
		var unsupported *capability.UnsupportedError
		if !errors.As(err, &unsupported) || *env.reads != 0 || len(env.fake.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, want unsupported before the key", connection, err, *env.reads)
		}
		tools, err := env.core.Tools(application.SearchRequest{Connection: connection})
		if err != nil {
			t.Fatalf("tools of %s = %v", connection, err)
		}
		for _, tool := range tools.Tools {
			if tool.ID == "brave.answers.ask" {
				t.Errorf("%s offers answers by its read effect", connection)
			}
		}
	}
	if _, err := env.invoke("brave.answers.ask", "answers", `{"question":"q"}`); err != nil {
		t.Errorf("a connection that lists answers: %v", err)
	}
	// The answers profile is not part of search, and no connection test spends an answer.
	if slices.Contains(searchTools, "brave.answers.ask") {
		t.Error("the search profile holds answers")
	}
	f := &fakeBrave{}
	serve(t, f)
	red := &redact.Redactor{}
	resolved := &config.Resolved{Name: "b", Provider: Provider, BaseURL: apiRoot, Credential: "k", Secrets: credential(),
		Tools: []string{"brave.answers.ask"}}
	if _, ok := probeFor(resolved); ok {
		t.Error("a connection test would probe answers")
	}
	if class, err := TestConnection(context.Background(), resolved, resolver(red, nil), red); err == nil || class != "" ||
		len(f.recorded()) != 0 {
		t.Errorf("connection test of answers: class = %q, err = %v, requests = %d", class, err, len(f.recorded()))
	}
}

// Answers keeps at least half a second between two requests of the key, also when the answer carried no
// rate-limit header.
func TestAnswersSpaceTheirRequests(t *testing.T) {
	env := newEnvironment(t, &fakeBrave{answer: streamReply(nil, []string{chunk("ok", "stop")}, true)})
	clock := time.Unix(1_000_000, 0)
	var waits []time.Duration
	t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
		func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil })))
	for range 2 {
		if _, err := env.invoke("brave.answers.ask", "answers", `{"question":"q"}`); err != nil {
			t.Fatalf("invoke = %v", err)
		}
	}
	if !reflect.DeepEqual(waits, []time.Duration{500 * time.Millisecond}) {
		t.Errorf("waits = %v, want one wait of 500ms before the second answer", waits)
	}
}

func TestAnswersDescriptionNamesCostAndLimits(t *testing.T) {
	for _, want := range []string{"4 USD per 1000 searches", "5 USD per million input and output tokens",
		"up to 50", "Answers plan", "lists this tool in its tools", "50 seconds", "55 seconds", "terms of use"} {
		if !strings.Contains(answersAsk.Description, want) {
			t.Errorf("description lacks %q", want)
		}
	}
	if !answersAsk.RequiresToolAllowList || answersAsk.Risk != readRisk {
		t.Errorf("descriptor = %+v", answersAsk)
	}
}

func TestSplitTagsKeepsWhatItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		content, text string
		citations     int
		cost          bool
	}{
		{"plain", "plain", 0, false},
		{"a <citation>{\"number\":1}</citation>b", "a b", 1, false},
		{"open <citation>{\"number\":1}", "open <citation>{\"number\":1}", 0, false},
		{"<usage>[1]</usage>x", "<usage>[1]</usage>x", 0, false},
		{"<usage>null</usage>", "<usage>null</usage>", 0, false},
		{"<usage>{}</usage><usage>{\"X-Request-Total-Cost\":0.1}</usage>", "", 0, true},
		{"<citation>bad</citation><citation>{\"number\":2}</citation>", "<citation>bad</citation>", 1, false},
		{"<thinking>why</thinking><answer>A</answer>", "<thinking>why</thinking><answer>A</answer>", 0, false},
	} {
		text, citations, cost := splitTags(tc.content)
		if text != tc.text || len(citations) != tc.citations || (cost != nil) != tc.cost {
			t.Errorf("%q: text = %q, citations = %d, cost = %v", tc.content, text, len(citations), cost)
		}
	}
	_, _, cost := splitTags("<usage>{}</usage><usage>{\"X-Request-Total-Cost\":0.1}</usage>")
	if cost == nil || cost.TotalCost == nil || *cost.TotalCost != 0.1 {
		t.Errorf("the last usage tag does not win: %+v", cost)
	}
}

func TestAnswersBoundTheText(t *testing.T) {
	var stream answerStream
	if err := stream.add([]byte(chunk(strings.Repeat("é", answersMaxRunes+10), "length"))); err != nil {
		t.Fatal(err)
	}
	for range answersMaxCitations + 5 {
		_ = stream.add([]byte(chunk(`<citation>{"number":1,"snippet":"`+strings.Repeat("s", 2000)+`"}</citation>`, "")))
	}
	result := stream.result(rateInfo{})
	if n := len([]rune(result.Answer)); n != answersMaxRunes+1 || !strings.HasSuffix(result.Answer, "…") {
		t.Errorf("answer has %d characters, want the bound and the cut mark", n)
	}
	if len(result.Citations) != answersMaxCitations || len([]rune(result.Citations[0].Snippet)) != maxSnippetRunes+1 {
		t.Errorf("citations = %d, snippet = %d characters", len(result.Citations), len([]rune(result.Citations[0].Snippet)))
	}
	if result.FinishReason != "length" {
		t.Errorf("finish_reason = %q", result.FinishReason)
	}
}
