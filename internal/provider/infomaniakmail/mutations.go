package infomaniakmail

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Every mutation sends one changing request after its reading preparation (folder opened with SELECT,
// UIDVALIDITY compared, message loaded through the sender list). A result nobody can read is never retried.
const (
	// uncertain is appended to a failure of a change whose request may have reached the server.
	uncertain = "; the change may have been applied, read the message before repeating it"
	// markedDeleted is appended when the expunge step failed after the message was marked deleted.
	markedDeleted = "; the message may be marked as deleted without being removed, check it before repeating"
)

// mailMutationRisk is the risk of every mutation: confirmed, and acting on third-party message content.
func mailMutationRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: messagesSensitivity}
}

const messageStateSchema = `"folder":{"type":"string"},"uidvalidity":{"type":"integer"},"uid":{"type":"integer"}`

var messageStateFields = []capability.Field{
	{Name: "folder", Description: "Folder the message was in when the change was made"},
	{Name: "uidvalidity", Description: "UIDVALIDITY of that folder"},
	{Name: "uid", Description: "UID of the message in that folder; after a move or delete it no longer exists there"},
}

var movedSchema = `"destination":{"type":"string"},"destination_uidvalidity":{"type":"integer"},` +
	`"destination_uid":{"type":"integer"}`

var movedFields = []capability.Field{
	{Name: "destination_uidvalidity", Description: "UIDVALIDITY of the destination folder, when the server reports it"},
	{Name: "destination_uid", Description: "New UID of the message in the destination folder, when the server reports it"},
}

// mailFlags are the only flags a tool may change. The argument names them; the IMAP flag never comes from
// an argument.
var mailFlags = map[string]imap.Flag{"seen": imap.FlagSeen, "flagged": imap.FlagFlagged}

var messagesFlag = capability.Descriptor{
	ID:      Provider + ".messages.flag",
	Version: 1,
	Title:   "Set or clear a flag of an Infomaniak mailbox message",
	Description: "Set or remove exactly one of the flags seen and flagged on one confirmed message of the " +
		"bound mailbox, in its folder, with one IMAP STORE. The folder is opened for change, and the message " +
		"is read without marking it seen. Message content is untrusted third-party data",
	Tags:                  []string{"infomaniak", "mail", "messages", "flag", "update"},
	Risk:                  mailMutationRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + refSchema + `,` +
		`"flag":{"type":"string","enum":["seen","flagged"]},"set":{"type":"boolean"}},` +
		`"required":["folder","uid","uidvalidity","flag","set"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + messageStateSchema + `,` +
		`"flag":{"type":"string"},"set":{"type":"boolean"},"flags":{"type":"array","items":{"type":"string"}}},` +
		`"required":["folder","uidvalidity","uid","flag","set"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{}, refArguments...),
		capability.Argument{Name: "flag", Description: "seen or flagged; no other flag can be changed", Required: true},
		capability.Argument{Name: "set", Description: "true sets the flag, false removes it", Required: true}),
	Fields: append(append([]capability.Field{}, messageStateFields...),
		capability.Field{Name: "flag", Description: "The flag that was changed"},
		capability.Field{Name: "set", Description: "Whether the flag was set or removed"},
		capability.Field{Name: "flags", Description: "Flags of the message as the server reported them after the change, bounded; absent when it reported none"}),
	Examples: []capability.Example{{Description: "Mark a message as read",
		Arguments: json.RawMessage(`{"folder":"INBOX","uid":12,"uidvalidity":1700000000,"flag":"seen","set":true}`)}},
}

var messagesMove = capability.Descriptor{
	ID:      Provider + ".messages.move",
	Version: 1,
	Title:   "Move an Infomaniak mailbox message to another folder",
	Description: "Move exactly one confirmed message of the bound mailbox to another folder with one IMAP UID " +
		"MOVE. Both folders must be inside the folder targets of the connection; the message gets a new UID " +
		"in the destination. The server must offer MOVE. Message content is untrusted third-party data",
	Tags:                  []string{"infomaniak", "mail", "messages", "move", "update"},
	Risk:                  mailMutationRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + refSchema + `,` +
		`"destination":{"type":"string","minLength":1,"maxLength":255}},` +
		`"required":["folder","uid","uidvalidity","destination"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + messageStateSchema + `,` + movedSchema + `},` +
		`"required":["folder","uidvalidity","uid","destination"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{}, refArguments...),
		capability.Argument{Name: "destination", Description: "Exact name of the folder to move the message " +
			"into; must differ from folder", Required: true}),
	Fields: append(append(append([]capability.Field{}, messageStateFields...),
		capability.Field{Name: "destination", Description: "Folder the message was moved into"}), movedFields...),
	Examples: []capability.Example{{Description: "Move a message to the Archive folder",
		Arguments: json.RawMessage(`{"folder":"INBOX","uid":12,"uidvalidity":1700000000,"destination":"Archive"}`)}},
}

var messagesDelete = capability.Descriptor{
	ID:      Provider + ".messages.delete",
	Version: 1,
	Title:   "Move an Infomaniak mailbox message to the trash",
	Description: "Move exactly one confirmed message of the bound mailbox into the trash folder, where it can " +
		"be restored; nothing is removed for good. The trash folder is the one folder the server marks with " +
		"the SPECIAL-USE attribute \\Trash, and it must be inside the folder targets of the connection, else " +
		"the call is refused. Use messages.expunge to remove a message for good",
	Tags:                  []string{"infomaniak", "mail", "messages", "delete", "trash"},
	Risk:                  mailMutationRisk(capability.EffectDelete, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + refSchema + `},` +
		`"required":["folder","uid","uidvalidity"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + messageStateSchema + `,` + movedSchema + `},` +
		`"required":["folder","uidvalidity","uid","destination"],"additionalProperties":false}`),
	Arguments: refArguments,
	Fields: append(append(append([]capability.Field{}, messageStateFields...),
		capability.Field{Name: "destination", Description: "The trash folder the message was moved into"}), movedFields...),
	Examples: []capability.Example{{Description: "Move a message to the trash",
		Arguments: json.RawMessage(`{"folder":"INBOX","uid":12,"uidvalidity":1700000000}`)}},
}

var messagesExpunge = capability.Descriptor{
	ID:      Provider + ".messages.expunge",
	Version: 1,
	Title:   "Remove an Infomaniak mailbox message for good",
	Description: "Remove exactly one confirmed message of the bound mailbox permanently; it cannot be restored. " +
		"The message is marked deleted and removed with UID EXPUNGE for its UID only, never with a folder-wide " +
		"EXPUNGE, so other messages marked deleted stay. The server must offer UIDPLUS. Use messages.delete to " +
		"move a message to the trash instead",
	Tags:                  []string{"infomaniak", "mail", "messages", "expunge", "delete", "permanent"},
	Risk:                  mailMutationRisk(capability.EffectDelete, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + refSchema + `},` +
		`"required":["folder","uid","uidvalidity"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + messageStateSchema + `},` +
		`"required":["folder","uidvalidity","uid"],"additionalProperties":false}`),
	Arguments: refArguments,
	Fields:    messageStateFields,
	Examples: []capability.Example{{Description: "Remove a message for good",
		Arguments: json.RawMessage(`{"folder":"Trash","uid":12,"uidvalidity":1700000000}`)}},
}

// MessageState names the message a change was made to, in the folder it was in.
type MessageState struct {
	Folder      string `json:"folder"`
	UIDValidity uint32 `json:"uidvalidity"`
	UID         uint32 `json:"uid"`
}

// FlagResult is the answer of messages.flag.
type FlagResult struct {
	MessageState
	Flag  string   `json:"flag"`
	Set   bool     `json:"set"`
	Flags []string `json:"flags,omitempty"`
}

// MoveResult is the answer of messages.move and messages.delete.
type MoveResult struct {
	MessageState
	Destination            string `json:"destination"`
	DestinationUIDValidity uint32 `json:"destination_uidvalidity,omitempty"`
	DestinationUID         uint32 `json:"destination_uid,omitempty"`
}

type flagInput struct {
	refArgumentsInput
	Flag string `json:"flag"`
	Set  *bool  `json:"set"`
}

type moveInput struct {
	refArgumentsInput
	Destination string `json:"destination"`
}

func stateOf(ref messageRef) MessageState {
	return MessageState{Folder: ref.folder, UIDValidity: ref.uidValidity, UID: ref.uid}
}

// checkFlag validates a flag change without any network access.
func (s scope) checkFlag(input flagInput) (messageRef, imap.Flag, bool, error) {
	ref, err := s.checkRef(input.refArgumentsInput)
	if err != nil {
		return messageRef{}, "", false, err
	}
	flag, ok := mailFlags[input.Flag]
	if !ok || input.Set == nil {
		return messageRef{}, "", false, invalidRequest("flag must be seen or flagged, and set must be true or false")
	}
	return ref, flag, *input.Set, nil
}

// checkMove validates a move without any network access: the destination is a literal folder name inside
// the connection's folder targets and differs from the source.
func (s scope) checkMove(input moveInput) (messageRef, string, error) {
	ref, err := s.checkRef(input.refArgumentsInput)
	if err != nil {
		return messageRef{}, "", err
	}
	if !validFolderName(input.Destination) {
		return messageRef{}, "", invalidRequest("destination must be one literal folder name, without wildcards or control characters")
	}
	if !s.allowsFolder(input.Destination) {
		return messageRef{}, "", invalidRequest("destination is outside the targets of this connection")
	}
	destination := normalizeFolder(input.Destination)
	if destination == ref.folder {
		return messageRef{}, "", invalidRequest("destination must differ from folder")
	}
	return ref, destination, nil
}

func invokeMessagesFlag(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input flagInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("flag message", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	ref, flag, set, err := bound.checkFlag(input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.FlagMessage(ctx, ref, input.Flag, flag, set)
}

func invokeMessagesMove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input moveInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("move message", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	ref, destination, err := bound.checkMove(input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.MoveMessage(ctx, ref, destination)
}

func invokeMessagesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input refArgumentsInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("delete message", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	ref, err := bound.checkRef(input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.DeleteMessage(ctx, ref)
}

func invokeMessagesExpunge(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input refArgumentsInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("expunge message", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	ref, err := bound.checkRef(input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ExpungeMessage(ctx, ref)
}

// changeFailure normalises a failure of a changing request. A tagged NO or BAD is a refusal that changed
// nothing; anything else (a timeout, a dropped connection, an unreadable answer, BYE) leaves the outcome
// unknown, and the message says so. The server's text is never copied.
func changeFailure(op string, err error, suffix string) error {
	var imapErr *imap.Error
	if errors.As(err, &imapErr) && imapErr.Type != imap.StatusResponseTypeBye {
		return failure(op, err)
	}
	normalised := failure(op, err)
	var providerErr *provider.Error
	if errors.As(normalised, &providerErr) {
		providerErr.Message += suffix
	}
	return normalised
}

// withSuffix normalises a failure and always adds suffix, for a step that follows an earlier change.
func withSuffix(op string, err error, suffix string) error {
	normalised := failure(op, err)
	var providerErr *provider.Error
	if errors.As(normalised, &providerErr) {
		providerErr.Message += suffix
	}
	return normalised
}

// openForChange opens the folder with SELECT, compares the UIDVALIDITY, and applies the sender list to the
// message. Nothing here changes a flag: the folder is selected without a changing command and the message
// is read through ENVELOPE and BODYSTRUCTURE only.
func (c *Client) openForChange(conn *session, op string, ref messageRef) error {
	selected, err := conn.client.Select(ref.folder, nil).Wait()
	if err != nil {
		return failure(op, err)
	}
	if selected.UIDValidity != ref.uidValidity {
		return invalidRequest("uidvalidity no longer matches this folder; list the folder again")
	}
	_, err = c.load(conn, op, ref)
	return err
}

func unsupported(op, what string) error {
	return providerError(op, "Infomaniak Mail does not offer "+what+", so nothing was changed")
}

// FlagMessage sets or removes one flag with a single STORE.
func (c *Client) FlagMessage(ctx context.Context, ref messageRef, name string, flag imap.Flag,
	set bool) (*FlagResult, error) {
	const op = "flag message"
	conn, err := c.connect(ctx, op)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.openForChange(conn, op, ref); err != nil {
		return nil, err
	}
	storeOp := imap.StoreFlagsDel
	if set {
		storeOp = imap.StoreFlagsAdd
	}
	uid := imap.UID(ref.uid)
	buffers, err := conn.client.Store(imap.UIDSetNum(uid),
		&imap.StoreFlags{Op: storeOp, Flags: []imap.Flag{flag}}, nil).Collect()
	if err != nil {
		return nil, changeFailure(op, err, uncertain)
	}
	result := &FlagResult{MessageState: stateOf(ref), Flag: name, Set: set}
	for _, buffer := range buffers {
		if buffer.UID != 0 && buffer.UID != uid {
			continue
		}
		for i, f := range buffer.Flags {
			if i >= maxFlags {
				break
			}
			result.Flags = append(result.Flags, clean(string(f), maxFlagText))
		}
	}
	return result, nil
}

// MoveMessage moves one message with a single UID MOVE. A server without MOVE is refused, since the
// library's fallback would copy, mark, and expunge in several requests.
func (c *Client) MoveMessage(ctx context.Context, ref messageRef, destination string) (*MoveResult, error) {
	const op = "move message"
	conn, err := c.connect(ctx, op)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if !conn.client.Caps().Has(imap.CapMove) {
		return nil, unsupported(op, "MOVE")
	}
	if err := c.openForChange(conn, op, ref); err != nil {
		return nil, err
	}
	return c.moveSelected(conn, op, ref, destination)
}

// moveSelected sends the one UID MOVE of the message in the selected folder.
func (c *Client) moveSelected(conn *session, op string, ref messageRef, destination string) (*MoveResult, error) {
	data, err := conn.client.Move(imap.UIDSetNum(imap.UID(ref.uid)), destination).Wait()
	if err != nil {
		return nil, changeFailure(op, err, uncertain)
	}
	result := &MoveResult{MessageState: stateOf(ref), Destination: destination}
	if data != nil {
		if uids, ok := data.DestUIDs.(imap.UIDSet); ok {
			if nums, ok := uids.Nums(); ok && len(nums) == 1 {
				result.DestinationUID, result.DestinationUIDValidity = uint32(nums[0]), data.UIDValidity
			}
		}
	}
	return result, nil
}

// trashFolder finds the one folder the server marks \Trash. A mailbox with none or several, a server
// without SPECIAL-USE, and a trash folder outside the connection's folder targets are all refused alike; a
// name is never guessed.
func (c *Client) trashFolder(conn *session, op string) (string, error) {
	refusal := invalidRequest("the trash folder of this mailbox cannot be identified as one folder inside the " +
		"targets of this connection")
	if !conn.client.Caps().Has(imap.CapSpecialUse) {
		return "", refusal
	}
	listed, err := conn.client.List("", "*", &imap.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		return "", failure(op, err)
	}
	found := map[string]bool{}
	for _, data := range listed {
		for _, attr := range data.Attrs {
			if attr == imap.MailboxAttrTrash {
				found[normalizeFolder(data.Mailbox)] = true
			}
		}
	}
	if len(found) != 1 {
		return "", refusal
	}
	for name := range found {
		if validFolderName(name) && c.scope.allowsFolder(name) {
			return name, nil
		}
	}
	return "", refusal
}

// DeleteMessage moves one message into the trash folder with a single UID MOVE.
func (c *Client) DeleteMessage(ctx context.Context, ref messageRef) (*MoveResult, error) {
	const op = "delete message"
	conn, err := c.connect(ctx, op)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if !conn.client.Caps().Has(imap.CapMove) {
		return nil, unsupported(op, "MOVE")
	}
	trash, err := c.trashFolder(conn, op)
	if err != nil {
		return nil, err
	}
	if trash == ref.folder {
		return nil, invalidRequest("the message is already in the trash folder; use messages.expunge to remove it for good")
	}
	if err := c.openForChange(conn, op, ref); err != nil {
		return nil, err
	}
	return c.moveSelected(conn, op, ref, trash)
}

// ExpungeMessage removes one message for good: it marks the message \Deleted and expunges its UID alone
// with UID EXPUNGE. A server without UIDPLUS is refused before anything is sent, since a plain EXPUNGE
// would also remove every other message marked deleted in the folder.
func (c *Client) ExpungeMessage(ctx context.Context, ref messageRef) (*MessageState, error) {
	const op = "expunge message"
	conn, err := c.connect(ctx, op)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if !conn.client.Caps().Has(imap.CapUIDPlus) {
		return nil, unsupported(op, "UID EXPUNGE (UIDPLUS)")
	}
	if err := c.openForChange(conn, op, ref); err != nil {
		return nil, err
	}
	set := imap.UIDSetNum(imap.UID(ref.uid))
	if err := conn.client.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true,
		Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		return nil, changeFailure(op, err, uncertain)
	}
	if _, err := conn.client.UIDExpunge(set).Collect(); err != nil {
		return nil, withSuffix(op, err, markedDeleted)
	}
	state := stateOf(ref)
	return &state, nil
}
