package brave

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	answersPath = "/res/v1/chat/completions"
	answersOp   = "ask answers"
	// Bounds of the request. Brave documents no limit for the question or the completion tokens.
	answersMaxQuestionRunes = 4000
	answersMaxTokens        = 32768
	// Research may run 300 seconds at Brave, but a streamed request ends after streamTimeout. The default
	// leaves time for the answer Brave writes after its research budget.
	answersMaxResearchSeconds     = 50
	answersDefaultResearchSeconds = 40
	// Bounds of the stream and of what the result shows.
	answersMaxStreamBytes = 8 << 20
	answersMaxRunes       = 200000
	answersMaxCitations   = 200
)

// answersInterval is the spacing after an answer request: the Answers plan allows two requests per second,
// whatever spacing the other plans of the key allow.
const answersInterval = 500 * time.Millisecond

var (
	answersModels       = []string{"brave-pro", "brave"}
	answersContextSizes = []string{"low", "medium", "high"}
	// Brave documents country and language by default value only, us and en.
	answersCountryPattern  = regexp.MustCompile(`^([A-Za-z]{2}|ALL)$`)
	answersLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,2}$`)

	rangeMaxTokens       = intRange{"max_completion_tokens", 1, answersMaxTokens}
	rangeResearchTokens  = intRange{"research_maximum_number_of_tokens_per_query", 1024, 16384}
	rangeResearchQueries = intRange{"research_maximum_number_of_queries", 1, 50}
	rangeResearchRounds  = intRange{"research_maximum_number_of_iterations", 1, 5}
	rangeResearchSeconds = intRange{"research_maximum_number_of_seconds", 1, answersMaxResearchSeconds}
	rangeResearchResults = intRange{"research_maximum_number_of_results_per_query", 1, 60}
	answersIntegerRanges = []intRange{rangeMaxTokens, rangeResearchTokens, rangeResearchQueries,
		rangeResearchRounds, rangeResearchSeconds, rangeResearchResults}
	answersRangeProps     = intProps(answersIntegerRanges)
	answersLocationSchema = `{"type":"object","properties":{` +
		`"city":{"type":"string","minLength":1,"maxLength":100},` +
		`"country":{"type":"string","minLength":1,"maxLength":100},` +
		`"region":{"type":"string","minLength":1,"maxLength":100},` +
		`"timezone":` + timezoneSchema + `},"additionalProperties":false}`
)

const (
	answersCitationSchema = `{"type":"object","properties":{"start_index":` + integerSchema + `,"end_index":` +
		integerSchema + `,"number":` + integerSchema + `,"url":` + stringSchema + `,"favicon":` + stringSchema +
		`,"snippet":` + stringSchema + `},"additionalProperties":false}`
	answersUsageSchema = `{"type":"object","properties":{"prompt_tokens":` + integerSchema + `,` +
		`"completion_tokens":` + integerSchema + `,"total_tokens":` + integerSchema + `,` +
		`"completion_tokens_details":{"type":"object","properties":{"reasoning_tokens":` + integerSchema + `},` +
		`"additionalProperties":false}},"additionalProperties":false}`
	answersCostSchema = `{"type":"object","properties":{"requests":` + integerSchema + `,"queries":` +
		integerSchema + `,"tokens_in":` + integerSchema + `,"tokens_out":` + integerSchema + `,` +
		`"requests_cost":{"type":"number"},"queries_cost":{"type":"number"},"tokens_in_cost":{"type":"number"},` +
		`"tokens_out_cost":{"type":"number"},"total_cost":{"type":"number"}},"additionalProperties":false}`
	answersOutputSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"object":` + stringSchema +
		`,"created":` + integerSchema + `,"model":` + stringSchema + `,"system_fingerprint":` + stringSchema +
		`,"role":` + stringSchema + `,"answer":` + stringSchema + `,"citations":{"type":"array","items":` +
		answersCitationSchema + `},"usage":` + answersUsageSchema + `,"cost":` + answersCostSchema +
		`,"finish_reason":` + stringSchema + `,"quota":` + quotaSchema + `},"required":["answer"],` +
		`"additionalProperties":false}`
)

// answersAsk asks Brave for a cited answer. It costs more than a search and varies with the answer, so a
// connection offers it only by name.
var answersAsk = func() capability.Descriptor {
	d := descriptor("answers", "ask", "Ask Brave Answers",
		"Ask a question and get an AI-written answer grounded in a live Brave web search, with inline citations, "+
			"token usage, and the cost Brave reports. Needs the Answers plan of Brave, which bills 4 USD per 1000 "+
			"searches plus 5 USD per million input and output tokens; an answer runs one search, research mode "+
			"up to 50. Because the cost varies, only a connection that lists this tool in its tools offers it. "+
			"The answer streams within one call: one that does not finish within 55 seconds ends as a timeout, "+
			"may still be billed, and is never repeated. Research is limited to 50 seconds here, so longer "+
			"research does not run through Qatlas, and citations and entities are not available with research.",
		[]string{"answers", "ai", "grounding"},
		`{"type":"object","properties":{"question":{"type":"string","minLength":1,"maxLength":4000},`+
			`"model":`+enumSchema(answersModels)+`,"search_context_size":`+enumSchema(answersContextSizes)+`,`+
			`"user_location":`+answersLocationSchema+`,`+
			`"country":{"type":"string","pattern":"^([A-Za-z]{2}|ALL)$","x-form":"two-letter code or ALL"},`+
			`"language":{"type":"string","maxLength":20,"pattern":"^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,2}$",`+
			`"x-form":"language code such as en or pt-br"},"safesearch":`+safeSearchSchema+`,`+
			answersRangeProps+
			`"enable_entities":`+booleanSchema+`,"enable_citations":`+booleanSchema+`,`+
			`"enable_research":`+booleanSchema+`},"required":["question"],"additionalProperties":false}`,
		json.RawMessage(answersOutputSchema),
		[]capability.Argument{
			{Name: "question", Description: "The question, at most 4000 characters; sent as the one user " +
				"message Brave accepts", Required: true},
			{Name: "model", Description: "brave-pro or brave; brave-pro when omitted"},
			{Name: "max_completion_tokens", Description: "Upper bound of the tokens of the answer, from 1 " +
				"through 32768; no bound when omitted"},
			{Name: "search_context_size", Description: "How much search context the answer draws on: low, " +
				"medium, or high"},
			{Name: "user_location", Description: "Approximate location of the user: city, country, region, " +
				"and timezone (IANA name such as Europe/Berlin), each optional"},
			{Name: "country", Description: "Country the search results come from as a two-letter code, or ALL; " +
				"us when omitted"},
			{Name: "language", Description: "Language of the answer as a code such as en or de; en when omitted"},
			{Name: "safesearch", Description: "off, moderate, or strict; moderate when omitted"},
			{Name: "enable_citations", Description: "Whether the answer carries citations; false when omitted. " +
				"Not with enable_research"},
			{Name: "enable_entities", Description: "Whether the answer carries Brave's inline entity tags, which " +
				"stay in the answer text unchanged; false when omitted. Not with enable_research"},
			{Name: "enable_research", Description: "Whether Brave researches with several searches instead of " +
				"one; slower and billed per search. false when omitted"},
			{Name: "research_maximum_number_of_tokens_per_query", Description: "Research: tokens gathered per " +
				"query, from 1024 through 16384; 8192 when omitted"},
			{Name: "research_maximum_number_of_queries", Description: "Research: search queries in total, from " +
				"1 through 50; 20 when omitted"},
			{Name: "research_maximum_number_of_iterations", Description: "Research: iterations, from 1 through " +
				"5; 4 when omitted"},
			{Name: "research_maximum_number_of_seconds", Description: "Research: time budget in seconds, from 1 " +
				"through 50; 40 when omitted, which leaves Brave time to write the answer"},
			{Name: "research_maximum_number_of_results_per_query", Description: "Research: search results " +
				"considered per query, from 1 through 60; 30 when omitted"},
		},
		[]capability.Field{
			{Name: "answer", Description: "The answer text without citation and usage tags, untrusted data; " +
				"entity tags and other tags Brave sends stay in it"},
			{Name: "citations", Description: "Sources of the answer: start_index and end_index of the cited " +
				"span, number, url, favicon, and snippet; addresses are never fetched"},
			{Name: "usage", Description: "Tokens Brave counted: prompt_tokens, completion_tokens, total_tokens, " +
				"and completion_tokens_details.reasoning_tokens"},
			{Name: "cost", Description: "What Brave reports it bills for this answer: requests, queries, " +
				"tokens_in, tokens_out, and their costs in USD with total_cost"},
			{Name: "finish_reason", Description: "Why the answer ended, as Brave states it"},
			{Name: "id", Description: "Identifier of the answer at Brave"},
			{Name: "created", Description: "Creation time of the answer as Unix seconds"},
			quotaField,
		},
		[]capability.Example{{
			Description: "Ask with citations",
			Arguments:   json.RawMessage(`{"question":"What is the second highest mountain?","enable_citations":true}`),
		}, {
			Description: "Research a question within the time limit",
			Arguments: json.RawMessage(`{"question":"Compare approaches to nuclear fusion","enable_research":true,` +
				`"research_maximum_number_of_iterations":2,"research_maximum_number_of_seconds":30}`),
		}})
	d.RequiresToolAllowList = true
	return d
}()

// answersLocation is the approximate location of the user. Qatlas never derives one: the caller states it.
type answersLocation struct {
	City     string `json:"city,omitempty"`
	Country  string `json:"country,omitempty"`
	Region   string `json:"region,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// answersResearch holds the research limits, as argument and as request member alike.
type answersResearch struct {
	TokensPerQuery  *int `json:"research_maximum_number_of_tokens_per_query,omitempty"`
	Queries         *int `json:"research_maximum_number_of_queries,omitempty"`
	Iterations      *int `json:"research_maximum_number_of_iterations,omitempty"`
	Seconds         *int `json:"research_maximum_number_of_seconds,omitempty"`
	ResultsPerQuery *int `json:"research_maximum_number_of_results_per_query,omitempty"`
}

// answersArguments are the arguments of brave.answers.ask.
type answersArguments struct {
	Question            string           `json:"question"`
	Model               string           `json:"model"`
	MaxCompletionTokens *int             `json:"max_completion_tokens"`
	SearchContextSize   string           `json:"search_context_size"`
	UserLocation        *answersLocation `json:"user_location"`
	Country             string           `json:"country"`
	Language            string           `json:"language"`
	Safesearch          string           `json:"safesearch"`
	EnableEntities      *bool            `json:"enable_entities"`
	EnableCitations     *bool            `json:"enable_citations"`
	EnableResearch      *bool            `json:"enable_research"`
	answersResearch
}

// answersMessage is one message of the conversation; Brave takes exactly one, from the user.
type answersMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// answersRequest is the body sent. Stream is always true: citations, entities, and research exist only in
// the stream.
type answersRequest struct {
	Messages            []answersMessage  `json:"messages"`
	Model               string            `json:"model"`
	Stream              bool              `json:"stream"`
	MaxCompletionTokens *int              `json:"max_completion_tokens,omitempty"`
	WebSearchOptions    *answersWebSearch `json:"web_search_options,omitempty"`
	Country             string            `json:"country,omitempty"`
	Language            string            `json:"language,omitempty"`
	Safesearch          string            `json:"safesearch,omitempty"`
	EnableEntities      *bool             `json:"enable_entities,omitempty"`
	EnableCitations     *bool             `json:"enable_citations,omitempty"`
	EnableResearch      *bool             `json:"enable_research,omitempty"`
	answersResearch
}

type answersWebSearch struct {
	SearchContextSize string               `json:"search_context_size,omitempty"`
	UserLocation      *answersUserLocation `json:"user_location,omitempty"`
}

type answersUserLocation struct {
	Approximate answersLocation `json:"approximate"`
}

// prepareAnswersAsk checks every argument; the request is built only from what passed. No message names the
// question.
func prepareAnswersAsk(args answersArguments) (run, error) {
	question := strings.TrimSpace(args.Question)
	switch {
	case question == "":
		return nil, badRequest("question must not be empty")
	case utf8.RuneCountInString(question) > answersMaxQuestionRunes:
		return nil, badRequest("question is longer than " + strconv.Itoa(answersMaxQuestionRunes) + " characters")
	case hasControl(question, true):
		return nil, badRequest("question must not contain control characters other than line breaks and tabs")
	}
	for _, check := range []error{
		checkEnum("model", args.Model, answersModels),
		checkEnum("search_context_size", args.SearchContextSize, answersContextSizes),
		checkEnum("safesearch", args.Safesearch, safeSearchModes),
		checkPattern("country", args.Country, answersCountryPattern, "a two-letter country code or ALL"),
		checkPattern("language", args.Language, answersLanguagePattern, "a language code such as en or pt-br"),
		args.UserLocation.check(),
	} {
		if check != nil {
			return nil, check
		}
	}
	for _, number := range []struct {
		limit intRange
		value *int
	}{
		{rangeMaxTokens, args.MaxCompletionTokens},
		{rangeResearchTokens, args.TokensPerQuery},
		{rangeResearchQueries, args.Queries},
		{rangeResearchRounds, args.Iterations},
		{rangeResearchSeconds, args.Seconds},
		{rangeResearchResults, args.ResultsPerQuery},
	} {
		if number.value != nil && (*number.value < number.limit.min || *number.value > number.limit.max) {
			return nil, badRequest(number.limit.name + " must be between " + strconv.Itoa(number.limit.min) +
				" and " + strconv.Itoa(number.limit.max))
		}
	}
	research := isTrue(args.EnableResearch)
	if research && (isTrue(args.EnableCitations) || isTrue(args.EnableEntities)) {
		return nil, badRequest("enable_citations and enable_entities are not available with enable_research")
	}
	if !research && args.answersResearch != (answersResearch{}) {
		return nil, badRequest("the research_maximum_* arguments need enable_research set to true")
	}

	model := args.Model
	if model == "" {
		model = answersModels[0]
	}
	body := answersRequest{
		Messages: []answersMessage{{Role: "user", Content: question}}, Model: model, Stream: true,
		MaxCompletionTokens: args.MaxCompletionTokens, Country: args.Country, Language: args.Language,
		Safesearch: args.Safesearch, EnableEntities: args.EnableEntities, EnableCitations: args.EnableCitations,
		EnableResearch: args.EnableResearch, answersResearch: args.answersResearch,
	}
	if research && body.Seconds == nil {
		// Brave's own default of 180 seconds would always outlast the request.
		seconds := answersDefaultResearchSeconds
		body.Seconds = &seconds
	}
	if args.SearchContextSize != "" || args.UserLocation.set() {
		body.WebSearchOptions = &answersWebSearch{SearchContextSize: args.SearchContextSize}
		if l := args.UserLocation; l.set() {
			body.WebSearchOptions.UserLocation = &answersUserLocation{Approximate: *l}
		}
	}

	return func(ctx context.Context, client *Client) (any, error) {
		var answer answerStream
		info, err := client.stream(ctx, answersOp, answersPath, body, answersInterval, answersMaxStreamBytes,
			answer.add)
		if err != nil {
			return nil, err
		}
		return answer.result(info), nil
	}, nil
}

func isTrue(value *bool) bool { return value != nil && *value }

// checkPattern refuses an optional argument that does not have the form the tool accepts.
func checkPattern(name, value string, pattern *regexp.Regexp, form string) error {
	if value != "" && (len(value) > 20 || !pattern.MatchString(value)) {
		return badRequest(name + " must be " + form)
	}
	return nil
}

func (l *answersLocation) set() bool {
	return l != nil && (l.City != "" || l.Country != "" || l.Region != "" || l.Timezone != "")
}

func (l *answersLocation) check() error {
	if l == nil {
		return nil
	}
	for _, field := range []struct{ name, value string }{
		{"user_location.city", l.City}, {"user_location.country", l.Country}, {"user_location.region", l.Region},
	} {
		if err := checkLocationText(field.name, field.value, maxLocationText); err != nil {
			return err
		}
	}
	return checkTimezone(l.Timezone)
}

// answerChunk is one event of the stream, read leniently: a member Brave adds is ignored.
type answerChunk struct {
	ID                string          `json:"id"`
	Object            string          `json:"object"`
	Created           *float64        `json:"created"`
	Model             string          `json:"model"`
	SystemFingerprint string          `json:"system_fingerprint"`
	Choices           []answerChoice  `json:"choices"`
	Usage             *answerUsage    `json:"usage"`
	Error             json.RawMessage `json:"error"`
}

type answerChoice struct {
	Delta struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// answerUsage is the token usage in Brave's member names; the counts are read as numbers and returned whole.
type answerUsage struct {
	PromptTokens            *float64 `json:"prompt_tokens,omitempty"`
	CompletionTokens        *float64 `json:"completion_tokens,omitempty"`
	TotalTokens             *float64 `json:"total_tokens,omitempty"`
	CompletionTokensDetails *struct {
		ReasoningTokens *float64 `json:"reasoning_tokens,omitempty"`
	} `json:"completion_tokens_details,omitempty"`
}

// answerCitation is one <citation> tag of the stream.
type answerCitation struct {
	StartIndex *float64 `json:"start_index,omitempty"`
	EndIndex   *float64 `json:"end_index,omitempty"`
	Number     *float64 `json:"number,omitempty"`
	URL        string   `json:"url,omitempty"`
	Favicon    string   `json:"favicon,omitempty"`
	Snippet    string   `json:"snippet,omitempty"`
}

// answerUsageTag is the <usage> tag of the stream, the X-Request-* values Brave bills by.
type answerUsageTag struct {
	Requests      *float64 `json:"X-Request-Requests"`
	Queries       *float64 `json:"X-Request-Queries"`
	TokensIn      *float64 `json:"X-Request-Tokens-In"`
	TokensOut     *float64 `json:"X-Request-Tokens-Out"`
	RequestsCost  *float64 `json:"X-Request-Requests-Cost"`
	QueriesCost   *float64 `json:"X-Request-Queries-Cost"`
	TokensInCost  *float64 `json:"X-Request-Tokens-In-Cost"`
	TokensOutCost *float64 `json:"X-Request-Tokens-Out-Cost"`
	TotalCost     *float64 `json:"X-Request-Total-Cost"`
}

// answerCost is the cost as the result shows it.
type answerCost struct {
	Requests      *float64 `json:"requests,omitempty"`
	Queries       *float64 `json:"queries,omitempty"`
	TokensIn      *float64 `json:"tokens_in,omitempty"`
	TokensOut     *float64 `json:"tokens_out,omitempty"`
	RequestsCost  *float64 `json:"requests_cost,omitempty"`
	QueriesCost   *float64 `json:"queries_cost,omitempty"`
	TokensInCost  *float64 `json:"tokens_in_cost,omitempty"`
	TokensOutCost *float64 `json:"tokens_out_cost,omitempty"`
	TotalCost     *float64 `json:"total_cost,omitempty"`
}

// answerStream collects the events of one answer.
type answerStream struct {
	first        answerChunk
	role         string
	finishReason string
	usage        *answerUsage
	content      strings.Builder
}

// add takes one event. An event that is not a chunk, or that reports an error, ends the stream.
func (s *answerStream) add(data []byte) error {
	var chunk answerChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return provider.InvalidResponse(answersOp, "Brave returned an invalid answer")
	}
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return provider.Fail(answersOp, "Brave reported an error in the middle of the answer")
	}
	keep := func(target *string, value string) {
		if *target == "" {
			*target = value
		}
	}
	keep(&s.first.ID, chunk.ID)
	keep(&s.first.Object, chunk.Object)
	keep(&s.first.Model, chunk.Model)
	keep(&s.first.SystemFingerprint, chunk.SystemFingerprint)
	if s.first.Created == nil {
		s.first.Created = chunk.Created
	}
	for _, choice := range chunk.Choices {
		keep(&s.role, choice.Delta.Role)
		s.content.WriteString(choice.Delta.Content)
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			s.finishReason = *choice.FinishReason
		}
	}
	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}
	return nil
}

// answersResult is what the tool returns.
type answersResult struct {
	ID                string           `json:"id,omitempty"`
	Object            string           `json:"object,omitempty"`
	Created           *float64         `json:"created,omitempty"`
	Model             string           `json:"model,omitempty"`
	SystemFingerprint string           `json:"system_fingerprint,omitempty"`
	Role              string           `json:"role,omitempty"`
	Answer            string           `json:"answer"`
	Citations         []answerCitation `json:"citations,omitempty"`
	Usage             *answerUsage     `json:"usage,omitempty"`
	Cost              *answerCost      `json:"cost,omitempty"`
	FinishReason      string           `json:"finish_reason,omitempty"`
	Quota             *quota           `json:"quota,omitempty"`
}

// result takes the tags out of the assembled text and bounds every value. The whole text is parsed only
// now, so a tag that arrived in several fragments is whole.
func (s *answerStream) result(info rateInfo) answersResult {
	text, citations, cost := splitTags(s.content.String())
	out := answersResult{
		ID: clip(s.first.ID, maxShortRunes), Object: clip(s.first.Object, maxShortRunes),
		Created: wholeNumber(s.first.Created), Model: clip(s.first.Model, maxShortRunes),
		SystemFingerprint: clip(s.first.SystemFingerprint, maxShortRunes), Role: clip(s.role, maxShortRunes),
		Answer: clip(text, answersMaxRunes), FinishReason: clip(s.finishReason, maxShortRunes),
		Cost: cost, Quota: quotaOf(info),
	}
	for i, citation := range citations {
		if i == answersMaxCitations {
			break
		}
		out.Citations = append(out.Citations, citation.bounded())
	}
	if u := s.usage; u != nil {
		usage := answerUsage{PromptTokens: wholeNumber(u.PromptTokens),
			CompletionTokens: wholeNumber(u.CompletionTokens), TotalTokens: wholeNumber(u.TotalTokens)}
		if d := u.CompletionTokensDetails; d != nil && wholeNumber(d.ReasoningTokens) != nil {
			usage.CompletionTokensDetails = &struct {
				ReasoningTokens *float64 `json:"reasoning_tokens,omitempty"`
			}{ReasoningTokens: wholeNumber(d.ReasoningTokens)}
		}
		out.Usage = &usage
	}
	return out
}

func (c answerCitation) bounded() answerCitation {
	c.StartIndex, c.EndIndex, c.Number = wholeNumber(c.StartIndex), wholeNumber(c.EndIndex), wholeNumber(c.Number)
	c.URL, c.Favicon, c.Snippet = address(c.URL), address(c.Favicon), clip(c.Snippet, maxSnippetRunes)
	return c
}

// splitTags removes every <citation> and <usage> tag whose content is the JSON object Brave documents and
// returns the remaining text, the citations in order, and the last usage. A tag without its closing tag or
// with other content stays in the text, as do the entity tags and any tag Brave does not document.
func splitTags(content string) (string, []answerCitation, *answerCost) {
	var (
		text      strings.Builder
		citations []answerCitation
		cost      *answerCost
	)
	rest := content
	for {
		at, name := nextTag(rest)
		if at < 0 {
			text.WriteString(rest)
			break
		}
		open, closing := "<"+name+">", "</"+name+">"
		inner := rest[at+len(open):]
		end := strings.Index(inner, closing)
		if end < 0 {
			text.WriteString(rest)
			break
		}
		payload := []byte(inner[:end])
		parsed := false
		if name == "citation" {
			var citation answerCitation
			if parsed = isObject(payload) && json.Unmarshal(payload, &citation) == nil; parsed {
				citations = append(citations, citation)
			}
		} else {
			var tag answerUsageTag
			if parsed = isObject(payload) && json.Unmarshal(payload, &tag) == nil; parsed {
				cost = tag.cost()
			}
		}
		if !parsed {
			text.WriteString(rest[:at+len(open)])
			rest = inner
			continue
		}
		text.WriteString(rest[:at])
		rest = inner[end+len(closing):]
	}
	return text.String(), citations, cost
}

// nextTag finds the earliest opening <citation> or <usage> tag.
func nextTag(text string) (int, string) {
	citation, usage := strings.Index(text, "<citation>"), strings.Index(text, "<usage>")
	switch {
	case citation < 0 && usage < 0:
		return -1, ""
	case usage < 0 || (citation >= 0 && citation < usage):
		return citation, "citation"
	}
	return usage, "usage"
}

func isObject(payload []byte) bool {
	trimmed := strings.TrimSpace(string(payload))
	return strings.HasPrefix(trimmed, "{")
}

func (t answerUsageTag) cost() *answerCost {
	return &answerCost{
		Requests: wholeNumber(t.Requests), Queries: wholeNumber(t.Queries), TokensIn: wholeNumber(t.TokensIn),
		TokensOut: wholeNumber(t.TokensOut), RequestsCost: t.RequestsCost, QueriesCost: t.QueriesCost,
		TokensInCost: t.TokensInCost, TokensOutCost: t.TokensOutCost, TotalCost: t.TotalCost,
	}
}
