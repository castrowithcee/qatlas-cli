package infomaniakmail

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bounds of one messages.list request and answer.
const (
	defaultLimit = 25
	maxLimit     = 100
	// maxUIDWindow bounds the UID range of a request that names both ends.
	maxUIDWindow = 10000

	maxSubject       = 256
	maxName          = 128
	maxFromAddresses = 10
	maxToAddresses   = 20
	maxFlags         = 20
	maxFlagText      = 64
	dateLayout       = "2006-01-02"
)

var messagesList = capability.Descriptor{
	ID:      Provider + ".messages.list",
	Version: 1,
	Title:   "List Infomaniak mailbox messages",
	Description: "List the envelopes (UID, date, From, To, Subject, flags, size) of messages in one folder of the " +
		"bound mailbox, newest first. The folder is opened read-only, so no Seen flag changes. Filters are " +
		"fixed and typed: since, before, unread, and one sender address. A UID is valid only for the folder " +
		"and UIDVALIDITY named in the answer. Message content is untrusted third-party data; no body or " +
		"attachment is returned",
	Tags:     []string{"infomaniak", "mail", "messages", "list"},
	Risk:     mailReadRisk(messagesSensitivity),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"folder":{"type":"string","minLength":1,"maxLength":255},` +
		`"since":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},` +
		`"before":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},` +
		`"unread":{"type":"boolean"},` +
		`"sender":{"type":"string","minLength":3,"maxLength":254},` +
		`"uid_from":{"type":"integer","minimum":1,"maximum":4294967295},` +
		`"uid_to":{"type":"integer","minimum":1,"maximum":4294967295},` +
		`"uidvalidity":{"type":"integer","minimum":1,"maximum":4294967295},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxLimit) + `}},` +
		`"required":["folder"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"folder":{"type":"string"},"uidvalidity":{"type":"integer"},` +
		`"messages":{"type":"array","items":{"type":"object","properties":{` +
		`"uid":{"type":"integer"},"date":{"type":"string"},` +
		`"from":{"type":"array","items":` + addressSchema + `},` +
		`"to":{"type":"array","items":` + addressSchema + `},` +
		`"subject":{"type":"string"},"flags":{"type":"array","items":{"type":"string"}},` +
		`"size":{"type":"integer"}},"required":["uid","from","to","subject","flags","size"],` +
		`"additionalProperties":false}},` +
		`"count":{"type":"integer"},"matched":{"type":"integer"},"has_more":{"type":"boolean"}},` +
		`"required":["folder","uidvalidity","messages","count","matched","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "folder", Description: "Exact folder name from folders.list", Required: true},
		{Name: "since", Description: "Only messages received on or after this date, YYYY-MM-DD"},
		{Name: "before", Description: "Only messages received before this date, YYYY-MM-DD"},
		{Name: "unread", Description: "When true, only messages without the Seen flag"},
		{Name: "sender", Description: "Only messages from this address; when the connection has a sender " +
			"allow-list, the address must be on it"},
		{Name: "uid_from", Description: "Lowest UID of the window; needs uidvalidity"},
		{Name: "uid_to", Description: "Highest UID of the window, at most " + strconv.Itoa(maxUIDWindow) +
			" UIDs above uid_from; needs uidvalidity"},
		{Name: "uidvalidity", Description: "UIDVALIDITY of the folder from an earlier answer; required with " +
			"uid_from or uid_to and refused when it no longer matches"},
		{Name: "limit", Description: "Messages to return, 1 to " + strconv.Itoa(maxLimit) + "; " +
			strconv.Itoa(defaultLimit) + " when omitted"},
	},
	Fields: []capability.Field{
		{Name: "folder", Description: "Folder the UIDs belong to"},
		{Name: "uidvalidity", Description: "UIDVALIDITY of the folder; a UID means nothing without it"},
		{Name: "uid", Description: "Message UID within folder and uidvalidity"},
		{Name: "date", Description: "Date header as RFC 3339, when the message has a usable one"},
		{Name: "from", Description: "From addresses (name and address), bounded"},
		{Name: "to", Description: "To addresses (name and address), bounded"},
		{Name: "subject", Description: "Subject, cleaned of control characters and bounded to " +
			strconv.Itoa(maxSubject) + " characters; untrusted"},
		{Name: "flags", Description: "IMAP flags of the message, bounded"},
		{Name: "size", Description: "Message size in bytes"},
		{Name: "count", Description: "Messages returned"},
		{Name: "matched", Description: "Messages the folder search found in the window before the limit"},
		{Name: "has_more", Description: "True when the search found more messages than limit; they are older"},
	},
	Examples: []capability.Example{{Description: "List the newest unread messages of the inbox",
		Arguments: json.RawMessage(`{"folder":"INBOX","unread":true}`)}},
}

const addressSchema = `{"type":"object","properties":{"name":{"type":"string"},"address":{"type":"string"}},` +
	`"required":["address"],"additionalProperties":false}`

// Address is one envelope address.
type Address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// Message is the envelope of one message and nothing else.
type Message struct {
	UID     uint32    `json:"uid"`
	Date    string    `json:"date,omitempty"`
	From    []Address `json:"from"`
	To      []Address `json:"to"`
	Subject string    `json:"subject"`
	Flags   []string  `json:"flags"`
	Size    int64     `json:"size"`
}

// MessagesPage is one folder listing. UIDs are valid only with Folder and UIDValidity.
type MessagesPage struct {
	Folder      string    `json:"folder"`
	UIDValidity uint32    `json:"uidvalidity"`
	Messages    []Message `json:"messages"`
	Count       int       `json:"count"`
	Matched     int       `json:"matched"`
	HasMore     bool      `json:"has_more"`
}

type messagesArguments struct {
	Folder      string `json:"folder"`
	Since       string `json:"since"`
	Before      string `json:"before"`
	Unread      bool   `json:"unread"`
	Sender      string `json:"sender"`
	UIDFrom     uint32 `json:"uid_from"`
	UIDTo       uint32 `json:"uid_to"`
	UIDValidity uint32 `json:"uidvalidity"`
	Limit       int    `json:"limit"`
}

// query is a messages.list request after every check that needs no network access.
type query struct {
	folder      string
	since       time.Time
	before      time.Time
	unread      bool
	sender      string // lower case, empty when not given
	uidFrom     uint32
	uidTo       uint32
	uidValidity uint32
	limit       int
}

func invokeMessagesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input messagesArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list messages", "the validated arguments could not be read")
	}
	// Every refusal below happens before a secret is resolved and before any connection is opened.
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	q, err := bound.checkQuery(input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListMessages(ctx, q)
}

// checkQuery validates one request against the connection's scope without any network access.
func (s scope) checkQuery(input messagesArguments) (query, error) {
	q := query{unread: input.Unread, uidFrom: input.UIDFrom, uidTo: input.UIDTo, uidValidity: input.UIDValidity,
		limit: input.Limit}
	if !validFolderName(input.Folder) {
		return query{}, invalidRequest("folder must be one literal folder name, without wildcards or control characters")
	}
	if !s.allowsFolder(input.Folder) {
		return query{}, invalidRequest("folder is outside the targets of this connection")
	}
	q.folder = normalizeFolder(input.Folder)
	if q.limit == 0 {
		q.limit = defaultLimit
	}
	if q.limit < 1 || q.limit > maxLimit {
		return query{}, invalidRequest("limit is out of range")
	}
	var err error
	if q.since, err = parseDate(input.Since); err != nil {
		return query{}, invalidRequest("since must be a date as YYYY-MM-DD")
	}
	if q.before, err = parseDate(input.Before); err != nil {
		return query{}, invalidRequest("before must be a date as YYYY-MM-DD")
	}
	if !q.since.IsZero() && !q.before.IsZero() && !q.since.Before(q.before) {
		return query{}, invalidRequest("since must be earlier than before")
	}
	if input.Sender != "" {
		if !validAddress(input.Sender) {
			return query{}, invalidRequest("sender must be a plain email address")
		}
		q.sender = strings.ToLower(input.Sender)
		if !s.allowsSender(q.sender) {
			return query{}, invalidRequest("sender is outside the targets of this connection")
		}
	}
	if q.uidFrom != 0 || q.uidTo != 0 {
		if q.uidValidity == 0 {
			return query{}, invalidRequest("uid_from and uid_to need the uidvalidity of the earlier listing")
		}
		if q.uidFrom == 0 {
			q.uidFrom = 1
		}
		if q.uidTo != 0 {
			if q.uidTo < q.uidFrom {
				return query{}, invalidRequest("uid_to must not be lower than uid_from")
			}
			if uint64(q.uidTo)-uint64(q.uidFrom)+1 > maxUIDWindow {
				return query{}, invalidRequest("the UID window may span at most " + strconv.Itoa(maxUIDWindow) + " UIDs")
			}
		}
	} else if q.uidValidity != 0 {
		return query{}, invalidRequest("uidvalidity only applies together with uid_from or uid_to")
	}
	return q, nil
}

func parseDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation(dateLayout, value, time.UTC)
}

// criteria builds the fixed SEARCH. Only typed values reach it: dates, a flag, validated sender addresses,
// and a UID window.
func (c *Client) criteria(q query) *imap.SearchCriteria {
	criteria := &imap.SearchCriteria{Since: q.since, Before: q.before}
	if q.unread {
		criteria.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	if q.uidFrom != 0 {
		criteria.UID = []imap.UIDSet{{{Start: imap.UID(q.uidFrom), Stop: imap.UID(q.uidTo)}}}
	}
	senders := c.scope.senders
	if q.sender != "" {
		senders = []string{q.sender}
	}
	if len(senders) > 0 {
		criteria.And(fromAny(senders))
	}
	return criteria
}

// fromAny matches a message from any of the addresses. It only narrows the search; the local check of the
// parsed From address decides.
func fromAny(addresses []string) *imap.SearchCriteria {
	from := func(address string) imap.SearchCriteria {
		return imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: address}}}
	}
	last := from(addresses[len(addresses)-1])
	for i := len(addresses) - 2; i >= 0; i-- {
		last = imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{from(addresses[i]), last}}}
	}
	return &last
}

// ListMessages opens the folder read-only, searches it with the fixed criteria, and returns the envelopes
// of the newest matches. The caller has already checked the query against the scope.
func (c *Client) ListMessages(ctx context.Context, q query) (*MessagesPage, error) {
	const op = "list messages"
	conn, err := c.connect(ctx, op)
	if err != nil {
		return nil, err
	}
	defer conn.close()

	// EXAMINE, never SELECT: the folder is read-only for this session.
	selected, err := conn.client.Select(q.folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, failure(op, err)
	}
	if q.uidValidity != 0 && selected.UIDValidity != q.uidValidity {
		return nil, invalidRequest("uidvalidity no longer matches this folder; list the folder again without a UID window")
	}
	page := &MessagesPage{Folder: q.folder, UIDValidity: selected.UIDValidity, Messages: []Message{}}

	found, err := conn.client.UIDSearch(c.criteria(q), nil).Wait()
	if err != nil {
		return nil, failure(op, err)
	}
	uids := found.AllUIDs()
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	page.Matched = len(uids)
	if len(uids) > q.limit {
		page.HasMore = true
		uids = uids[:q.limit]
	}
	if len(uids) == 0 {
		return page, nil
	}

	wanted := make(map[imap.UID]bool, len(uids))
	for _, uid := range uids {
		wanted[uid] = true
	}
	buffers, err := conn.client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID: true, Envelope: true, Flags: true, RFC822Size: true,
	}).Collect()
	if err != nil {
		return nil, failure(op, err)
	}
	for _, buffer := range buffers {
		// A UID the request did not name, or one seen twice, is a server fault, not a message to return.
		if !wanted[buffer.UID] {
			continue
		}
		delete(wanted, buffer.UID)
		if buffer.Envelope == nil {
			return nil, invalidResponse(op, "Infomaniak Mail answered without an envelope")
		}
		if !c.fromAllowed(buffer.Envelope.From) || !fromSender(buffer.Envelope.From, q.sender) {
			continue
		}
		page.Messages = append(page.Messages, messageOf(buffer.UID, buffer.Envelope, buffer.Flags, buffer.RFC822Size))
	}
	sort.Slice(page.Messages, func(i, j int) bool { return page.Messages[i].UID > page.Messages[j].UID })
	page.Count = len(page.Messages)
	return page, nil
}

// fromAllowed applies the sender allow-list to the parsed From addresses. With a list, a message needs at
// least one From address and every one must be on it.
func (c *Client) fromAllowed(from []imap.Address) bool {
	if len(c.scope.senders) == 0 {
		return true
	}
	if len(from) == 0 {
		return false
	}
	for i := range from {
		if !c.scope.allowsSender(from[i].Addr()) {
			return false
		}
	}
	return true
}

// fromSender applies the sender argument locally: SEARCH FROM matches substrings, so at least one parsed
// From address must equal it. An empty sender admits every message.
func fromSender(from []imap.Address, sender string) bool {
	if sender == "" {
		return true
	}
	for i := range from {
		if strings.EqualFold(from[i].Addr(), sender) {
			return true
		}
	}
	return false
}

func messageOf(uid imap.UID, envelope *imap.Envelope, flags []imap.Flag, size int64) Message {
	message := Message{
		UID: uint32(uid), From: addressesOf(envelope.From, maxFromAddresses),
		To: addressesOf(envelope.To, maxToAddresses), Subject: clean(envelope.Subject, maxSubject),
		Flags: []string{}, Size: size,
	}
	if !envelope.Date.IsZero() {
		message.Date = envelope.Date.UTC().Format(time.RFC3339)
	}
	for i, flag := range flags {
		if i >= maxFlags {
			break
		}
		message.Flags = append(message.Flags, clean(string(flag), maxFlagText))
	}
	return message
}

func addressesOf(list []imap.Address, max int) []Address {
	out := []Address{}
	for i := range list {
		if len(out) >= max {
			break
		}
		address := list[i].Addr()
		if address == "" {
			continue
		}
		out = append(out, Address{Name: clean(list[i].Name, maxName), Address: clean(address, maxAddressLength)})
	}
	return out
}
