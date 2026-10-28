package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// A poll is bound like the conversation it belongs to. An option ID reaches a vote only after it was found in
// the options of that poll as the poll itself reports them, so a vote never names an option Qatlas has not seen.

const (
	// The bounds are far below what Talk accepts, so a poll stays easy to review before it is created.
	maxPollQuestion = 500
	maxPollOption   = 200
	minPollOptions  = 2
	maxPollOptions  = 10
	maxPollIDLen    = 18

	// Bounds of the report of a poll that somebody else created.
	maxPollOptionsShown = 100
	maxPollVoters       = 500

	pollStatusOpen   = 0
	pollStatusClosed = 1

	pollModePublic = "public"
	pollModeHidden = "hidden"

	featurePolls = "talk-polls"

	messageTalkNoPoll   = "this Talk conversation or poll does not exist or is not accessible to this identity"
	messageTalkLobby    = "the lobby of this Talk conversation is active and this identity is not a moderator"
	messagePollClosed   = "this Talk poll is closed and takes no more votes"
	messagePollOption   = "an option_id is not an option of this Talk poll; read the options with talk.polls.get"
	messagePollTooMany  = "more options were chosen than this Talk poll allows per voter"
	uncertainPollCreate = "; the poll may have been created, check talk.messages.list before repeating"
	uncertainPollVote   = "; the vote may have been applied, check talk.polls.get before repeating"
	uncertainPollClose  = "; the poll may have been closed, check talk.polls.get before repeating"
)

var ocsTalkPoll = []string{"apps", "spreed", "api", "v1", "poll"}

var (
	talkPollCreateErrors = map[int]string{
		http.StatusBadRequest:         "Talk refused this poll (for example the conversation is a one-to-one conversation)",
		http.StatusForbidden:          "this identity may not create polls in this Talk conversation (for example it is read-only)",
		http.StatusPreconditionFailed: messageTalkLobby,
	}
	talkPollVoteErrors = map[int]string{
		http.StatusBadRequest:         "Talk refused this vote (for example the poll is closed or an option does not fit)",
		http.StatusPreconditionFailed: messageTalkLobby,
	}
	talkPollCloseErrors = map[int]string{
		http.StatusBadRequest:         "Talk cannot close this poll (for example it is already closed)",
		http.StatusForbidden:          "only the author of the poll or a moderator may close it",
		http.StatusPreconditionFailed: messageTalkLobby,
	}
	talkPollGetErrors = map[int]string{
		http.StatusBadRequest:         "Talk cannot report this poll",
		http.StatusPreconditionFailed: messageTalkLobby,
	}
)

// PollOption is one option of a poll. Votes is absent while Talk hides the counts.
type PollOption struct {
	ID    int    `json:"id"`
	Text  string `json:"text"`
	Votes *int   `json:"votes,omitempty"`
}

// PollVoter is one vote of a closed public poll.
type PollVoter struct {
	ActorType string `json:"actor_type,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	ActorName string `json:"actor_name,omitempty"`
	OptionID  int    `json:"option_id"`
}

// Poll is the stable Qatlas view of one poll; question, options, and names are untrusted data.
type Poll struct {
	ID         string       `json:"id"`
	Question   string       `json:"question"`
	Options    []PollOption `json:"options"`
	Status     string       `json:"status"`
	ResultMode string       `json:"result_mode"`
	MaxVotes   int          `json:"max_votes"`
	AuthorType string       `json:"author_type,omitempty"`
	AuthorID   string       `json:"author_id,omitempty"`
	AuthorName string       `json:"author_name,omitempty"`
	VotedSelf  []int        `json:"voted_self,omitempty"`
	NumVoters  *int         `json:"num_voters,omitempty"`
	Voters     []PollVoter  `json:"voters,omitempty"`
	Truncated  bool         `json:"truncated,omitempty"`
}

type rawPoll struct {
	ID               json.Number     `json:"id"`
	Question         string          `json:"question"`
	Options          []string        `json:"options"`
	Votes            json.RawMessage `json:"votes"`
	ActorType        string          `json:"actorType"`
	ActorID          string          `json:"actorId"`
	ActorDisplayName string          `json:"actorDisplayName"`
	Status           json.Number     `json:"status"`
	ResultMode       json.Number     `json:"resultMode"`
	MaxVotes         json.Number     `json:"maxVotes"`
	VotedSelf        []json.Number   `json:"votedSelf"`
	NumVoters        *json.Number    `json:"numVoters"`
	Details          []struct {
		ActorType        string      `json:"actorType"`
		ActorID          string      `json:"actorId"`
		ActorDisplayName string      `json:"actorDisplayName"`
		OptionID         json.Number `json:"optionId"`
	} `json:"details"`
}

func validPollID(id string) bool { return digitsOnly(id) && len(id) <= maxPollIDLen }

func validPollText(text string, limit int) bool {
	return strings.TrimSpace(text) != "" && utf8.ValidString(text) && utf8.RuneCountInString(text) <= limit
}

// validPoll checks the bounds of a new poll and resolves the defaults: public results, one vote per voter.
func validPoll(question string, options []string, mode string, maxVotes *int) (resultMode, votes int, ok bool) {
	if !validPollText(question, maxPollQuestion) || len(options) < minPollOptions || len(options) > maxPollOptions {
		return 0, 0, false
	}
	for _, option := range options {
		if !validPollText(option, maxPollOption) {
			return 0, 0, false
		}
	}
	switch mode {
	case "", pollModePublic:
	case pollModeHidden:
		resultMode = 1
	default:
		return 0, 0, false
	}
	votes = 1
	if maxVotes != nil {
		votes = *maxVotes
	}
	return resultMode, votes, votes >= 0 && votes <= len(options)
}

// validOptionIDs accepts a non-empty list of distinct option IDs within what a poll can report.
func validOptionIDs(ids []int) bool {
	if len(ids) == 0 || len(ids) > maxPollOptionsShown {
		return false
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if id < 0 || id >= maxPollOptionsShown || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// pollOf builds the view of a poll. An option ID is its position in the options, which is how Talk numbers them.
func pollOf(raw rawPoll) (*Poll, bool) {
	if !validPollID(raw.ID.String()) {
		return nil, false
	}
	poll := &Poll{ID: raw.ID.String(), Options: []PollOption{}}
	var cutQuestion bool
	poll.Question, cutQuestion = cut(raw.Question, maxMessageText)
	poll.Truncated = cutQuestion
	switch status, _ := raw.Status.Int64(); status {
	case pollStatusOpen:
		poll.Status = "open"
	case pollStatusClosed:
		poll.Status = "closed"
	default:
		poll.Status = "other"
	}
	if mode, _ := raw.ResultMode.Int64(); mode == 1 {
		poll.ResultMode = pollModeHidden
	} else {
		poll.ResultMode = pollModePublic
	}
	votes, _ := raw.MaxVotes.Int64()
	poll.MaxVotes = int(votes)
	poll.AuthorType = bounded(raw.ActorType)
	poll.AuthorID, poll.AuthorName = bounded(raw.ActorID), bounded(raw.ActorDisplayName)
	// Talk reports the counts as a map "option-<id>" => number, or as an empty list while they are hidden.
	var counts map[string]json.Number
	_ = json.Unmarshal(raw.Votes, &counts)
	for id, text := range raw.Options {
		if id == maxPollOptionsShown {
			poll.Truncated = true
			break
		}
		shown, cutOption := cut(text, maxValueLength)
		poll.Truncated = poll.Truncated || cutOption
		option := PollOption{ID: id, Text: shown}
		if count, err := counts["option-"+strconv.Itoa(id)].Int64(); err == nil {
			n := int(count)
			option.Votes = &n
		}
		poll.Options = append(poll.Options, option)
	}
	for _, id := range raw.VotedSelf {
		if n, err := id.Int64(); err == nil {
			poll.VotedSelf = append(poll.VotedSelf, int(n))
		}
	}
	if raw.NumVoters != nil {
		if n, err := raw.NumVoters.Int64(); err == nil {
			count := int(n)
			poll.NumVoters = &count
		}
	}
	for _, detail := range raw.Details {
		if len(poll.Voters) == maxPollVoters {
			poll.Truncated = true
			break
		}
		option, _ := detail.OptionID.Int64()
		poll.Voters = append(poll.Voters, PollVoter{ActorType: bounded(detail.ActorType), ActorID: bounded(detail.ActorID),
			ActorName: bounded(detail.ActorDisplayName), OptionID: int(option)})
	}
	return poll, true
}

// readPoll reads the poll document of an answer. After a change, hint says that the change may have happened.
func readPoll(op string, data json.RawMessage, hint string) (*Poll, error) {
	var raw rawPoll
	if err := json.Unmarshal(data, &raw); err == nil {
		if poll, ok := pollOf(raw); ok {
			return poll, nil
		}
	}
	err := invalidResponse(op, "the Nextcloud poll could not be read")
	if hint != "" {
		err = withUncertainty(err, hint)
	}
	return nil, err
}

func talkPollError(op string, err error, messages map[int]string) error {
	return talkNotFound(op, tagAdminError(op, err, messages), messageTalkNoPoll)
}

// fetchPoll reads one poll of a conversation the caller has checked.
func (c *Client) fetchPoll(ctx context.Context, op, token, id string) (*Poll, error) {
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkPoll, suffix: []string{token, id}})
	if err != nil {
		return nil, talkPollError(op, err, talkPollGetErrors)
	}
	return readPoll(op, data, "")
}

// GetPoll reports one poll with the option IDs a vote needs.
func (c *Client) GetPoll(ctx context.Context, token, id string) (*Poll, error) {
	const op = "get talk poll"
	if !validTalkToken(token) || !validPollID(id) {
		return nil, providerError(op, "the poll arguments are unusable")
	}
	if err := c.talkGate(ctx, op, token, messageTalkNoPoll); err != nil {
		return nil, err
	}
	if err := c.talkFeature(ctx, op, featurePolls); err != nil {
		return nil, err
	}
	return c.fetchPoll(ctx, op, token, id)
}

// CreatePoll creates one poll after a single capability read.
func (c *Client) CreatePoll(ctx context.Context, token, question string, options []string, mode string, maxVotes *int) (*Poll, error) {
	const op = "create talk poll"
	resultMode, votes, ok := validPoll(question, options, mode, maxVotes)
	if !validTalkToken(token) || !ok {
		return nil, providerError(op, "the poll arguments are unusable")
	}
	if err := c.talkGate(ctx, op, token, messageTalkNoPoll); err != nil {
		return nil, err
	}
	if err := c.talkFeature(ctx, op, featurePolls); err != nil {
		return nil, err
	}
	form := url.Values{"question": {question}, "resultMode": {strconv.Itoa(resultMode)}, "maxVotes": {strconv.Itoa(votes)}}
	for _, option := range options {
		form.Add("options[]", option)
	}
	data, err := c.ocsSend(ctx, op, http.MethodPost, ocsRequest{app: ocsTalkPoll, suffix: []string{token}}, form, uncertainPollCreate)
	if err != nil {
		return nil, talkPollError(op, err, talkPollCreateErrors)
	}
	return readPoll(op, data, uncertainPollCreate)
}

// VotePoll votes after reading the poll once, which replaces the capability read: only option IDs the poll
// reports are sent, and not more of them than the poll allows.
func (c *Client) VotePoll(ctx context.Context, token, id string, optionIDs []int) (*Poll, error) {
	const op = "vote on talk poll"
	if !validTalkToken(token) || !validPollID(id) || !validOptionIDs(optionIDs) {
		return nil, providerError(op, "the poll arguments are unusable")
	}
	if err := c.talkGate(ctx, op, token, messageTalkNoPoll); err != nil {
		return nil, err
	}
	current, err := c.fetchPoll(ctx, op, token, id)
	if err != nil {
		return nil, err
	}
	if current.Status != "open" {
		return nil, providerError(op, messagePollClosed)
	}
	for _, option := range optionIDs {
		if option >= len(current.Options) {
			return nil, providerError(op, messagePollOption)
		}
	}
	if current.MaxVotes > 0 && len(optionIDs) > current.MaxVotes {
		return nil, providerError(op, messagePollTooMany)
	}
	form := url.Values{}
	for _, option := range optionIDs {
		form.Add("optionIds[]", strconv.Itoa(option))
	}
	data, err := c.ocsSend(ctx, op, http.MethodPost, ocsRequest{app: ocsTalkPoll, suffix: []string{token, id}}, form, uncertainPollVote)
	if err != nil {
		return nil, talkPollError(op, err, talkPollVoteErrors)
	}
	return readPoll(op, data, uncertainPollVote)
}

// ClosePoll closes a poll for good after a single capability read; Talk offers no way to open it again.
func (c *Client) ClosePoll(ctx context.Context, token, id string) (*Poll, error) {
	const op = "close talk poll"
	if !validTalkToken(token) || !validPollID(id) {
		return nil, providerError(op, "the poll arguments are unusable")
	}
	if err := c.talkGate(ctx, op, token, messageTalkNoPoll); err != nil {
		return nil, err
	}
	if err := c.talkFeature(ctx, op, featurePolls); err != nil {
		return nil, err
	}
	data, err := c.ocsSend(ctx, op, http.MethodDelete, ocsRequest{app: ocsTalkPoll, suffix: []string{token, id}}, nil, uncertainPollClose)
	if err != nil {
		return nil, talkPollError(op, err, talkPollCloseErrors)
	}
	return readPoll(op, data, uncertainPollClose)
}

const (
	pollIDSchema   = `{"type":"string","pattern":"^[0-9]{1,18}$"}`
	pollSchemaHead = `"token":` + talkTokenSchema + `,"poll_id":` + pollIDSchema
	pollSchema     = `{"type":"object","properties":{"id":{"type":"string"},"question":{"type":"string"},` +
		`"options":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"text":{"type":"string"},` +
		`"votes":{"type":"integer"}},"required":["id","text"],"additionalProperties":false}},` +
		`"status":{"type":"string","enum":["open","closed","other"]},"result_mode":{"type":"string","enum":["public","hidden"]},` +
		`"max_votes":{"type":"integer"},"author_type":{"type":"string"},"author_id":{"type":"string"},"author_name":{"type":"string"},` +
		`"voted_self":{"type":"array","items":{"type":"integer"}},"num_voters":{"type":"integer"},` +
		`"voters":{"type":"array","items":{"type":"object","properties":{"actor_type":{"type":"string"},` +
		`"actor_id":{"type":"string"},"actor_name":{"type":"string"},"option_id":{"type":"integer"}},` +
		`"required":["option_id"],"additionalProperties":false}},"truncated":{"type":"boolean"}},` +
		`"required":["id","question","options","status","result_mode","max_votes"],"additionalProperties":false}`
)

var pollIDArgument = capability.Argument{Name: "poll_id", Required: true,
	Description: "Numeric ID of a poll, as reported by the message that announces it or by talk.polls.create"}

var pollFields = []capability.Field{
	{Name: "id", Description: "ID of the poll"},
	{Name: "question", Description: "Question, untrusted data"},
	{Name: "options", Description: "Options with the id a vote names, their text (untrusted data), and the votes once Talk shows them; at most 100"},
	{Name: "status", Description: "open or closed; other for a state Qatlas does not know"},
	{Name: "result_mode", Description: "public shows the result at once, hidden only after the poll is closed"},
	{Name: "max_votes", Description: "Options one voter may choose; 0 means any number"},
	{Name: "author_type", Description: "Actor type of the author, untrusted data"},
	{Name: "author_id", Description: "ID of the author"},
	{Name: "author_name", Description: "Display name of the author, untrusted data"},
	{Name: "voted_self", Description: "Option IDs this identity voted for"},
	{Name: "num_voters", Description: "Number of voters, where Talk reports it"},
	{Name: "voters", Description: "Who voted for which option, only for a closed public poll, untrusted data, at most 500"},
	{Name: "truncated", Description: "True when a text was cut or options or voters were left out"},
}

var talkPollsGet = talkDescriptor("talk.polls.get", "Get a Nextcloud Talk poll",
	"Read one poll of a Talk conversation the connection binds, with the option IDs that talk.polls.vote needs. "+
		"Question, options, and names are untrusted data",
	`{"type":"object","properties":{`+pollSchemaHead+`},"required":["token","poll_id"],"additionalProperties":false}`,
	pollSchema, []capability.Argument{talkTokenArgument, pollIDArgument}, pollFields,
	capability.Example{Description: "Read a poll", Arguments: json.RawMessage(`{"token":"abcd1234","poll_id":"7"}`)})

var talkPollsCreate = capability.Descriptor{
	ID: Provider + ".talk.polls.create", Version: 1, Title: "Create a Nextcloud Talk poll",
	Description: "Create one poll in a Talk conversation the connection binds, with a question of at most 500 characters " +
		"and 2 to 10 options of at most 200 characters. After an unclear outcome the poll may exist, so check " +
		"talk.messages.list before creating it again. A rate limit is reported as such and not repeated",
	Tags:     []string{"nextcloud", "talk", "poll", "create", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema + `,"question":{"type":"string","minLength":1,"maxLength":500},` +
		`"options":{"type":"array","minItems":2,"maxItems":10,"items":{"type":"string","minLength":1,"maxLength":200}},` +
		`"result_mode":{"type":"string","enum":["public","hidden"]},"max_votes":{"type":"integer","minimum":0,"maximum":10}},` +
		`"required":["token","question","options"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(pollSchema),
	Arguments: []capability.Argument{talkTokenArgument,
		{Name: "question", Required: true, Description: "Question, 1 to 500 characters"},
		{Name: "options", Required: true, Description: "2 to 10 options, each 1 to 200 characters"},
		{Name: "result_mode", Description: "public (default) shows the result at once, hidden only after the poll is closed"},
		{Name: "max_votes", Description: "Options one voter may choose, at most the number of options; 0 means any number, 1 when omitted"}},
	Fields:   pollFields,
	Examples: []capability.Example{{Description: "Ask for a day", Arguments: json.RawMessage(`{"token":"abcd1234","question":"Which day?","options":["Monday","Tuesday"]}`)}},
}

var talkPollsVote = capability.Descriptor{
	ID: Provider + ".talk.polls.vote", Version: 1, Title: "Vote on a Nextcloud Talk poll",
	Description: "Vote on an open poll of a Talk conversation the connection binds. The option IDs must be those of " +
		"talk.polls.get; Qatlas reads the poll once and refuses an unknown option or more options than the poll allows. " +
		"A vote replaces an earlier one of this identity",
	Tags:     []string{"nextcloud", "talk", "poll", "vote", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + pollSchemaHead + `,"option_ids":{"type":"array","minItems":1,` +
		`"maxItems":100,"uniqueItems":true,"items":{"type":"integer","minimum":0,"maximum":99}}},` +
		`"required":["token","poll_id","option_ids"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(pollSchema),
	Arguments: []capability.Argument{talkTokenArgument, pollIDArgument,
		{Name: "option_ids", Required: true, Description: "IDs of the chosen options, as reported by talk.polls.get"}},
	Fields:   pollFields,
	Examples: []capability.Example{{Description: "Choose the first option", Arguments: json.RawMessage(`{"token":"abcd1234","poll_id":"7","option_ids":[0]}`)}},
}

var talkPollsClose = capability.Descriptor{
	ID: Provider + ".talk.polls.close", Version: 1, Title: "Close a Nextcloud Talk poll",
	Description: "Close a poll of a Talk conversation the connection binds for good: nobody can vote afterwards and " +
		"Talk publishes the result. Talk allows only the author and moderators",
	Tags:     []string{"nextcloud", "talk", "poll", "close", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + pollSchemaHead +
		`},"required":["token","poll_id"],"additionalProperties":false}`),
	OutputSchema:          json.RawMessage(pollSchema),
	Arguments:             []capability.Argument{talkTokenArgument, pollIDArgument},
	Fields:                pollFields,
	Examples:              []capability.Example{{Description: "Close a poll", Arguments: json.RawMessage(`{"token":"abcd1234","poll_id":"7"}`)}},
	RequiresToolAllowList: true,
}

func invokeTalkPollsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get talk poll"
	input, err := readTalkWrite(op, messageTalkNoPoll, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validPollID(input.PollID) {
		return nil, providerError(op, "the poll arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.GetPoll(ctx, input.Token, input.PollID)
}

func invokeTalkPollsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create talk poll"
	input, err := readTalkWrite(op, messageTalkNoPoll, resolved, raw)
	if err != nil {
		return nil, err
	}
	if _, _, ok := validPoll(input.Question, input.Options, input.ResultMode, input.MaxVotes); !ok {
		return nil, providerError(op, "the poll arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.CreatePoll(ctx, input.Token, input.Question, input.Options, input.ResultMode, input.MaxVotes)
}

func invokeTalkPollsVote(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "vote on talk poll"
	input, err := readTalkWrite(op, messageTalkNoPoll, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validPollID(input.PollID) || !validOptionIDs(input.OptionIDs) {
		return nil, providerError(op, "the poll arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.VotePoll(ctx, input.Token, input.PollID, input.OptionIDs)
}

func invokeTalkPollsClose(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "close talk poll"
	input, err := readTalkWrite(op, messageTalkNoPoll, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validPollID(input.PollID) {
		return nil, providerError(op, "the poll arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.ClosePoll(ctx, input.Token, input.PollID)
}
