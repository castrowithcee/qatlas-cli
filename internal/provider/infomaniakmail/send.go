package infomaniakmail

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxReferences = 20
	// maxMessageID bounds a Message-ID: 200 characters before and after the at sign plus the brackets and the at sign.
	maxMessageID = 403
	// maxSendBytes is the message size Infomaniak documents as its limit.
	maxSendBytes = 25 << 20
	// copyNote is returned when the message was sent but the copy in the Sent folder is not confirmed.
	copyNote = "the message was sent; storing the copy in the Sent folder failed or has an unknown outcome, so the " +
		"copy may be missing; check the Sent folder and do not send the message again"
)

// messageIDPattern is one Message-ID: an id-left and an id-right of atext characters and dots, in angle brackets.
var messageIDPattern = regexp.MustCompile("^<[A-Za-z0-9!#$%&'*+/=?^_`{|}~.-]{1,200}@[A-Za-z0-9.-]{1,200}>$")

func validMessageID(id string) bool {
	return len(id) <= maxMessageID && messageIDPattern.MatchString(id)
}

var sendFieldArguments = func() []capability.Argument {
	out := append([]capability.Argument{}, draftFieldArguments...)
	for i := range out {
		if out[i].Name == "bcc" {
			out[i].Description = "Bcc addresses, plain addresses only; they travel only as envelope recipients " +
				"and appear in no header of the sent message or of its copy"
		}
	}
	return append(out,
		capability.Argument{Name: "in_reply_to", Description: "Message-ID of the message this one replies to, as " +
			"<id@host>; makes the message a reply. Take it from the message_id of messages.get"},
		capability.Argument{Name: "references", Description: "Up to 20 Message-IDs of the conversation as <id@host>, " +
			"oldest first; needs in_reply_to; defaults to in_reply_to alone"})
}()

var sendResultSchema = `"message_id":{"type":"string"},"recipients":{"type":"integer"},"size":{"type":"integer"},` +
	`"copy_stored":{"type":"boolean"},"sent_folder":{"type":"string"},"sent_uidvalidity":{"type":"integer"},` +
	`"sent_uid":{"type":"integer"},"note":{"type":"string"},"draft_kept":{"type":"boolean"},` +
	`"attachments":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
	`"type":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"}},` +
	`"required":["name","type","size","sha256"],"additionalProperties":false}}`

var sendResultFields = []capability.Field{
	{Name: "message_id", Description: "Message-ID of the sent message"},
	{Name: "recipients", Description: "Envelope recipients: To, Cc, and Bcc together, without duplicates"},
	{Name: "size", Description: "Size of the sent message in bytes"},
	{Name: "copy_stored", Description: "Whether the copy in the Sent folder was stored; the message is sent either way"},
	{Name: "sent_folder", Description: "The Sent folder the copy was stored in"},
	{Name: "sent_uidvalidity", Description: "UIDVALIDITY of the Sent folder, when the server reports it"},
	{Name: "sent_uid", Description: "UID of the copy in the Sent folder, when the server reports it"},
	{Name: "note", Description: "Set when the message was sent but its copy is not confirmed"},
	{Name: "draft_kept", Description: "True for drafts.send: the draft still exists and is not marked as sent"},
	{Name: "attachments", Description: "Name, type, decoded size, and SHA-256 of each attachment; never its content"},
}

const sendDescription = "The message is submitted over SMTP to the fixed host mail.infomaniak.com on port 587 with " +
	"mandatory STARTTLS and a verified certificate; the login is the mailbox address and password of the connection, " +
	"sent only after TLS. From is the mailbox of the connection, which must be inside the sender targets when " +
	"there are any, and is also the envelope sender. Recipients are the typed to, cc, and bcc only (at most 50 " +
	"together); bcc appears in no header. The message has exactly one DATA transfer and is never repeated: a " +
	"failure before its end means nothing was sent, a refusal after it means the server declined it, and any " +
	"unclear answer after it is reported as maybe sent. After a successful submission a copy is stored with " +
	"APPEND and the flag \\Seen in the one folder the server marks \\Sent (SPECIAL-USE), which must be inside " +
	"the folder targets and is checked before anything is sent; a failed copy is reported, never repeated, and " +
	"does not undo the sent message. The answer holds metadata only"

var messagesSend = capability.Descriptor{
	ID:      Provider + ".messages.send",
	Version: 1,
	Title:   "Send an Infomaniak mailbox message",
	Description: "Send one new message or reply from the bound mailbox, built from the typed arguments only (text " +
		"and attachments from local_path or content_base64, optional reply headers In-Reply-To and References as " +
		"validated Message-IDs; Date and Message-ID are generated). " + sendDescription,
	Tags:                  []string{"infomaniak", "mail", "messages", "send", "smtp", "attachments", "local"},
	Risk:                  mailMutationRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	LocalFiles:            config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + draftFieldsSchema + `,` +
		`"in_reply_to":{"type":"string","minLength":5,"maxLength":403},` +
		`"references":{"type":"array","maxItems":20,"items":{"type":"string","minLength":5,"maxLength":403}}},` +
		`"required":["to"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + sendResultSchema + `},` +
		`"required":["message_id","recipients","size","copy_stored","attachments"],"additionalProperties":false}`),
	Arguments: sendFieldArguments,
	Fields:    sendResultFields,
	Examples: []capability.Example{{Description: "Reply to a message",
		Arguments: json.RawMessage(`{"to":["client@example.net"],"subject":"Re: Invoice","body":"Thank you.",` +
			`"in_reply_to":"<abc123@example.net>"}`)}},
}

var draftsSend = capability.Descriptor{
	ID:      Provider + ".drafts.send",
	Version: 1,
	Title:   "Send an Infomaniak mailbox draft",
	Description: "Send one existing draft of the bound mailbox. The draft must be in the drafts folder (the one " +
		"folder the server marks \\Drafts) and carry the flag \\Draft, otherwise nothing happens. It is never sent " +
		"raw: it is read with BODY.PEEK and checked, and only a draft in the structure drafts.create and " +
		"messages.send write is accepted: only the headers From, To, Cc, Bcc, Subject, Date, Message-ID, " +
		"In-Reply-To, References, MIME-Version, Content-Type, and Content-Transfer-Encoding, one plain text part " +
		"and base64 attachments, 7-bit content, and From equal to the mailbox of the connection. Any other draft " +
		"is refused. The headers are rewritten from the checked values, the Bcc header is removed, and Date is " +
		"set to now. The draft is not deleted or changed afterwards and stays in the drafts folder, so sending " +
		"it again needs another confirmed call. " + sendDescription,
	Tags:                  []string{"infomaniak", "mail", "drafts", "send", "smtp"},
	Risk:                  mailMutationRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + refSchema + `},` +
		`"required":["folder","uid","uidvalidity"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + sendResultSchema + `},` +
		`"required":["message_id","recipients","size","copy_stored","attachments"],"additionalProperties":false}`),
	Arguments: refArguments,
	Fields:    sendResultFields,
	Examples: []capability.Example{{Description: "Send a draft",
		Arguments: json.RawMessage(`{"folder":"Drafts","uid":12,"uidvalidity":1700000000}`)}},
}

// SendResult is the answer of messages.send and drafts.send. It never carries message content.
type SendResult struct {
	MessageID       string            `json:"message_id"`
	Recipients      int               `json:"recipients"`
	Size            int64             `json:"size"`
	CopyStored      bool              `json:"copy_stored"`
	SentFolder      string            `json:"sent_folder,omitempty"`
	SentUIDValidity uint32            `json:"sent_uidvalidity,omitempty"`
	SentUID         uint32            `json:"sent_uid,omitempty"`
	Note            string            `json:"note,omitempty"`
	DraftKept       bool              `json:"draft_kept,omitempty"`
	Attachments     []DraftAttachment `json:"attachments"`
}

type sendInput struct {
	draftFieldsInput
	InReplyTo  string   `json:"in_reply_to"`
	References []string `json:"references"`
}

// recipientsOf lists every envelope recipient once, comparing addresses case-insensitively.
func recipientsOf(to, cc, bcc []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{to, cc, bcc} {
		for _, address := range list {
			if key := strings.ToLower(address); !seen[key] {
				seen[key] = true
				out = append(out, address)
			}
		}
	}
	return out
}

// checkSend validates a new message or reply without any network access, reads the attachments, and builds
// the message without a Bcc header.
func (s scope) checkSend(ctx context.Context, resolved *config.Resolved, input sendInput) (*outgoing, error) {
	if !s.allowsSender(s.mailbox) {
		return nil, invalidRequest("the mailbox of this connection is outside its sender targets, so it may not send")
	}
	spec, err := s.messageSpec(ctx, resolved, input.draftFieldsInput)
	if err != nil {
		return nil, err
	}
	switch {
	case input.InReplyTo == "" && len(input.References) > 0:
		return nil, invalidRequest("references needs in_reply_to")
	case input.InReplyTo != "":
		if !validMessageID(input.InReplyTo) {
			return nil, invalidRequest("in_reply_to must be one Message-ID as <id@host>")
		}
		if len(input.References) > maxReferences {
			return nil, invalidRequest("references may hold at most 20 Message-IDs")
		}
		for _, id := range input.References {
			if !validMessageID(id) {
				return nil, invalidRequest("every reference must be a Message-ID as <id@host>")
			}
		}
		spec.inReplyTo, spec.references = input.InReplyTo, input.References
		if len(spec.references) == 0 {
			spec.references = []string{input.InReplyTo}
		}
	}
	id, err := messageID(s.mailbox)
	if err != nil {
		return nil, providerError("send message", "no message ID could be generated")
	}
	spec.id, spec.hideBcc = id, true
	raw, summary, err := spec.build()
	if err != nil {
		return nil, providerError("send message", err.Error())
	}
	return &outgoing{raw: raw, from: s.mailbox, rcpts: recipientsOf(input.To, input.Cc, input.Bcc), messageID: id,
		attachments: summary}, nil
}

func invokeMessagesSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input sendInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("send message", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	msg, err := bound.checkSend(ctx, resolved, input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SendMessage(ctx, msg)
}

func invokeDraftsSend(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input refArgumentsInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("send draft", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	ref, err := bound.checkRef(input)
	if err != nil {
		return nil, err
	}
	if !bound.allowsSender(bound.mailbox) {
		return nil, invalidRequest("the mailbox of this connection is outside its sender targets, so it may not send")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SendDraft(ctx, ref)
}

// sentFolder finds the one folder the server marks \Sent, under the same rules as trashFolder. It is
// resolved before anything is sent, so a mailbox without a usable Sent folder never sends.
func (c *Client) sentFolder(conn *session, op string) (string, error) {
	return c.specialFolder(conn, op, imap.MailboxAttrSent, "sent")
}

// SendMessage sends a new message or reply and stores its copy.
func (c *Client) SendMessage(ctx context.Context, msg *outgoing) (*SendResult, error) {
	const op = "send message"
	// The IMAP connection waits through the submission, so its time covers both.
	conn, err := c.connectWithin(ctx, op, msg.timeout()+defaultTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	sent, err := c.sentFolder(conn, op)
	if err != nil {
		return nil, err
	}
	return c.deliver(ctx, conn, op, sent, msg)
}

// SendDraft sends one draft after checking it and stores the copy. The draft stays in the drafts folder.
func (c *Client) SendDraft(ctx context.Context, ref messageRef) (*SendResult, error) {
	const op = "send draft"
	conn, err := c.connectWithin(ctx, op, transferTimeout+defaultTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	sent, err := c.sentFolder(conn, op)
	if err != nil {
		return nil, err
	}
	loaded, err := c.openDraft(conn, op, ref)
	if err != nil {
		return nil, err
	}
	if loaded.size > maxSendBytes {
		return nil, invalidRequest("the draft is larger than 25 MiB and cannot be sent")
	}
	var raw []byte
	err = streamPart(conn, op, loaded.uid, nil, nil, func(r io.Reader) error {
		var readErr error
		raw, readErr = io.ReadAll(io.LimitReader(r, maxSendBytes+1))
		if readErr == nil && len(raw) > maxSendBytes {
			readErr = invalidRequest("the draft is larger than 25 MiB and cannot be sent")
		}
		return readErr
	})
	if err != nil {
		return nil, err
	}
	msg, err := c.scope.outgoingFromDraft(raw)
	if err != nil {
		return nil, err
	}
	return c.deliver(ctx, conn, op, sent, msg)
}

// deliver submits the message once and then stores the copy in the Sent folder. The submission is never
// repeated. A copy that cannot be confirmed does not turn the sent message into an error.
func (c *Client) deliver(ctx context.Context, conn *session, op, sent string, msg *outgoing) (*SendResult, error) {
	if err := c.smtpSend(ctx, op, msg, msg.timeout()); err != nil {
		return nil, err
	}
	result := &SendResult{MessageID: msg.messageID, Recipients: len(msg.rcpts), Size: int64(len(msg.raw)),
		Attachments: msg.attachments, DraftKept: msg.draftKept}
	data, err := appendWithFlag(conn, sent, msg.raw, imap.FlagSeen, "", op)
	if err != nil {
		result.Note = copyNote
		return result, nil
	}
	result.CopyStored, result.SentFolder = true, sent
	if data != nil {
		result.SentUID, result.SentUIDValidity = uint32(data.UID), data.UIDValidity
	}
	return result, nil
}
