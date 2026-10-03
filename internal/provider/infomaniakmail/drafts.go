package infomaniakmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// draftReplaced is appended when the new draft of drafts.update was stored and the old one may remain.
	draftReplaced = "; the new draft was created%s, but the old draft may still exist or may be marked as " +
		"deleted without being removed; check the drafts folder and remove the old draft with drafts.delete " +
		"before repeating"
	// draftUncertainUpdate is appended when the APPEND of drafts.update has an unknown outcome.
	draftUncertainUpdate = "; the new draft may have been created while the old draft still exists, read the " +
		"drafts folder before repeating"
)

var draftFieldsSchema = `"to":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"string","minLength":3,"maxLength":254}},` +
	`"cc":{"type":"array","maxItems":50,"items":{"type":"string","minLength":3,"maxLength":254}},` +
	`"bcc":{"type":"array","maxItems":50,"items":{"type":"string","minLength":3,"maxLength":254}},` +
	`"subject":{"type":"string","maxLength":256},` +
	`"body":{"type":"string","maxLength":262144},` +
	`"attachments":{"type":"array","maxItems":10,"items":{"type":"object","properties":{` +
	`"name":{"type":"string","minLength":1,"maxLength":255},` +
	`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,` +
	`"` + localfile.ContentArgument + `":{"type":"string","maxLength":` + strconv.Itoa(maxDraftInlineBase64) + `},` +
	`"content_type":{"type":"string","minLength":3,"maxLength":100}},"additionalProperties":false}}`

var draftFieldArguments = []capability.Argument{
	{Name: "to", Description: "Recipient addresses, 1 to 50 in To, Cc, and Bcc together; plain addresses only, " +
		"without display names", Required: true},
	{Name: "cc", Description: "Cc addresses, plain addresses only"},
	{Name: "bcc", Description: "Bcc addresses, plain addresses only; stored in the draft as a Bcc header"},
	{Name: "subject", Description: "Subject, at most 256 characters, without control characters"},
	{Name: "body", Description: "Plain text of the message as UTF-8, at most 256 KiB"},
	{Name: "attachments", Description: "Up to 10 attachments, together at most 15 MiB decoded; each is an object " +
		"with exactly one of local_path (a file in a directory the connection releases for reading) and " +
		"content_base64 (up to 4 MiB), an optional name (required with content_base64; a file name without a " +
		"path), and an optional content_type as type/subtype (else from the file extension, else " +
		"application/octet-stream)"},
}

var draftResultSchema = `"folder":{"type":"string"},"uidvalidity":{"type":"integer"},"uid":{"type":"integer"},` +
	`"size":{"type":"integer"},"replaced_uid":{"type":"integer"},` +
	`"attachments":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
	`"type":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"}},` +
	`"required":["name","type","size","sha256"],"additionalProperties":false}}`

var draftResultFields = []capability.Field{
	{Name: "folder", Description: "The drafts folder the draft was stored in"},
	{Name: "uidvalidity", Description: "UIDVALIDITY of the drafts folder, when the server reports it"},
	{Name: "uid", Description: "UID of the new draft in the drafts folder, when the server reports it"},
	{Name: "size", Description: "Size of the stored message in bytes"},
	{Name: "replaced_uid", Description: "UID of the old draft that drafts.update removed"},
	{Name: "attachments", Description: "Name, type, decoded size, and SHA-256 of each attachment; never its content"},
}

var draftsCreate = capability.Descriptor{
	ID:      Provider + ".drafts.create",
	Version: 1,
	Title:   "Create an Infomaniak mailbox draft",
	Description: "Store one new draft message in the drafts folder of the bound mailbox with one IMAP APPEND and the " +
		"flag \\Draft. The message is built from the typed arguments only: From is the mailbox of the connection, " +
		"Date and Message-ID are generated, the text is quoted-printable UTF-8, and attachments come from " +
		"local_path or content_base64. No header, flag, folder, or raw message comes from an argument, and a " +
		"control character in any header value is refused. The drafts folder is the one folder the server marks " +
		"with the SPECIAL-USE attribute \\Drafts and must be inside the folder targets of the connection. " +
		"Nothing is sent. The answer holds metadata only",
	Tags:                  []string{"infomaniak", "mail", "drafts", "create", "attachments", "local"},
	Risk:                  mailMutationRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	LocalFiles:            config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + draftFieldsSchema + `},` +
		`"required":["to"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + draftResultSchema + `},` +
		`"required":["folder","size","attachments"],"additionalProperties":false}`),
	Arguments: draftFieldArguments,
	Fields:    draftResultFields,
	Examples: []capability.Example{{Description: "Draft a message with one attachment from a released directory",
		Arguments: json.RawMessage(`{"to":["client@example.net"],"subject":"Invoice","body":"Please find the invoice attached.",` +
			`"attachments":[{"local_path":"~/uploads/invoice.pdf"}]}`)}},
}

var draftsUpdate = capability.Descriptor{
	ID:      Provider + ".drafts.update",
	Version: 1,
	Title:   "Replace an Infomaniak mailbox draft",
	Description: "Replace one existing draft of the bound mailbox by a complete new one, built from the same typed " +
		"arguments as drafts.create. IMAP cannot change a message, so the new draft is stored with one APPEND " +
		"and the old draft is then removed with UID EXPUNGE for its UID only; the new draft gets a new UID. The " +
		"server must offer UIDPLUS, checked before anything is written. The message must be in the drafts " +
		"folder and carry the flag \\Draft, otherwise nothing happens. If the removal fails, the error says " +
		"that the new draft exists and the old one may remain",
	Tags:                  []string{"infomaniak", "mail", "drafts", "update", "attachments", "local"},
	Risk:                  mailMutationRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	LocalFiles:            config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + refSchema + `,` + draftFieldsSchema + `},` +
		`"required":["folder","uid","uidvalidity","to"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + draftResultSchema + `},` +
		`"required":["folder","size","attachments"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{}, refArguments...), draftFieldArguments...),
	Fields:    draftResultFields,
	Examples: []capability.Example{{Description: "Replace a draft",
		Arguments: json.RawMessage(`{"folder":"Drafts","uid":12,"uidvalidity":1700000000,"to":["client@example.net"],` +
			`"subject":"Invoice","body":"Corrected text."}`)}},
}

var draftsDelete = capability.Descriptor{
	ID:      Provider + ".drafts.delete",
	Version: 1,
	Title:   "Remove an Infomaniak mailbox draft for good",
	Description: "Remove exactly one draft of the bound mailbox permanently; it cannot be restored. The message must " +
		"be in the drafts folder (the one folder the server marks \\Drafts) and carry the flag \\Draft, otherwise " +
		"nothing happens, so no other message can be removed with this tool. It is marked deleted and removed " +
		"with UID EXPUNGE for its UID only. The server must offer UIDPLUS",
	Tags:                  []string{"infomaniak", "mail", "drafts", "delete", "permanent"},
	Risk:                  mailMutationRisk(capability.EffectDelete, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + refSchema + `},` +
		`"required":["folder","uid","uidvalidity"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + messageStateSchema + `},` +
		`"required":["folder","uidvalidity","uid"],"additionalProperties":false}`),
	Arguments: refArguments,
	Fields:    messageStateFields,
	Examples: []capability.Example{{Description: "Remove a draft",
		Arguments: json.RawMessage(`{"folder":"Drafts","uid":12,"uidvalidity":1700000000}`)}},
}

// DraftResult is the answer of drafts.create and drafts.update. It never carries message content.
type DraftResult struct {
	Folder      string            `json:"folder"`
	UIDValidity uint32            `json:"uidvalidity,omitempty"`
	UID         uint32            `json:"uid,omitempty"`
	Size        int64             `json:"size"`
	ReplacedUID uint32            `json:"replaced_uid,omitempty"`
	Attachments []DraftAttachment `json:"attachments"`
}

type draftAttachmentInput struct {
	Name        *string `json:"name"`
	LocalPath   *string `json:"local_path"`
	Content     *string `json:"content_base64"`
	ContentType string  `json:"content_type"`
}

type draftFieldsInput struct {
	To          []string               `json:"to"`
	Cc          []string               `json:"cc"`
	Bcc         []string               `json:"bcc"`
	Subject     string                 `json:"subject"`
	Body        string                 `json:"body"`
	Attachments []draftAttachmentInput `json:"attachments"`
}

type draftUpdateInput struct {
	refArgumentsInput
	draftFieldsInput
}

// draftPlan is a built draft, ready to be appended.
type draftPlan struct {
	raw         []byte
	attachments []DraftAttachment
}

// timeout is the time the operation may take: longer for a message that carries a large attachment.
func (p *draftPlan) timeout() time.Duration {
	if len(p.raw) > draftTransferBytes {
		return transferTimeout
	}
	return defaultTimeout
}

func checkRecipients(to, cc, bcc []string) error {
	if len(to) == 0 {
		return invalidRequest("to needs at least one recipient")
	}
	if len(to)+len(cc)+len(bcc) > maxDraftRecipients {
		return invalidRequest("a draft may have at most 50 recipients in to, cc, and bcc together")
	}
	for _, list := range [][]string{to, cc, bcc} {
		for _, address := range list {
			if !validRecipient(address) {
				return invalidRequest("every recipient must be a plain email address without display name or control characters")
			}
		}
	}
	return nil
}

// readAttachment validates one attachment argument and reads its content: decoded from content_base64, or
// from a local file the connection releases for reading. remaining is what is left of the total.
func readAttachment(ctx context.Context, resolved *config.Resolved, index int, input draftAttachmentInput,
	remaining int64) (draftAttachment, error) {
	label := "attachment " + strconv.Itoa(index+1)
	source, err := localfile.UploadSource(input.LocalPath, input.Content)
	if err != nil {
		return draftAttachment{}, err
	}
	name := ""
	if input.Name != nil {
		name = *input.Name
		if !validFileName(name) {
			return draftAttachment{}, invalidRequest(label + ": name must be a file name without a path or control characters")
		}
	}
	var content []byte
	if source == localfile.SourceContent {
		if name == "" {
			return draftAttachment{}, invalidRequest(label + ": name is required with " + localfile.ContentArgument)
		}
		if len(*input.Content) > maxDraftInlineBase64 {
			return draftAttachment{}, invalidRequest(label + ": inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		content, err = base64.StdEncoding.DecodeString(*input.Content)
		if err != nil {
			return draftAttachment{}, invalidRequest(label + ": " + localfile.ContentArgument + " is not valid base64")
		}
		if len(content) > maxDraftInline {
			return draftAttachment{}, invalidRequest(label + ": inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
	} else {
		upload, err := localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
		if err != nil {
			return draftAttachment{}, err
		}
		defer upload.Close()
		if upload.Size > remaining {
			return draftAttachment{}, invalidRequest(label + ": the attachments of a draft are limited to 15 MiB together")
		}
		content, err = io.ReadAll(upload)
		if err != nil {
			return draftAttachment{}, err
		}
		if name == "" {
			name = upload.Name
			if !validFileName(name) {
				return draftAttachment{}, invalidRequest(label + ": the local file name is not usable; pass name")
			}
		}
	}
	if int64(len(content)) > remaining {
		return draftAttachment{}, invalidRequest(label + ": the attachments of a draft are limited to 15 MiB together")
	}
	contentType, ok := attachmentType(input.ContentType, name)
	if !ok {
		return draftAttachment{}, invalidRequest(label + ": content_type must be a plain type/subtype, not multipart or message")
	}
	return draftAttachment{name: name, contentType: contentType, content: content}, nil
}

// checkDraft validates the typed fields without any network access, reads the attachments, and builds the
// message. It runs before the credential is resolved, so a refusal costs no secret access and no I/O at the
// provider.
func (s scope) checkDraft(ctx context.Context, resolved *config.Resolved, input draftFieldsInput) (*draftPlan, error) {
	if !s.allowsSender(s.mailbox) {
		return nil, invalidRequest("the mailbox of this connection is outside its sender targets, so its drafts " +
			"could not be read back")
	}
	if err := checkRecipients(input.To, input.Cc, input.Bcc); err != nil {
		return nil, err
	}
	if !validSubject(input.Subject) {
		return nil, invalidRequest("subject must be at most 256 characters without control characters")
	}
	if len(input.Body) > maxDraftBody || !utf8.ValidString(input.Body) || strings.ContainsRune(input.Body, 0) {
		return nil, invalidRequest("body must be valid UTF-8 text of at most 256 KiB")
	}
	if len(input.Attachments) > maxDraftAttachments {
		return nil, invalidRequest("a draft may have at most 10 attachments")
	}
	spec := draftSpec{from: s.mailbox, to: input.To, cc: input.Cc, bcc: input.Bcc, subject: input.Subject, body: input.Body}
	remaining := int64(maxDraftTotalBytes)
	for i, attachment := range input.Attachments {
		read, err := readAttachment(ctx, resolved, i, attachment, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= int64(len(read.content))
		spec.attachments = append(spec.attachments, read)
	}
	raw, summary, err := spec.build()
	if err != nil {
		return nil, providerError("build draft", err.Error())
	}
	return &draftPlan{raw: raw, attachments: summary}, nil
}

func invokeDraftsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input draftFieldsInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("create draft", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	plan, err := bound.checkDraft(ctx, resolved, input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateDraft(ctx, plan)
}

func invokeDraftsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input draftUpdateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("update draft", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	ref, err := bound.checkRef(input.refArgumentsInput)
	if err != nil {
		return nil, err
	}
	plan, err := bound.checkDraft(ctx, resolved, input.draftFieldsInput)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateDraft(ctx, ref, plan)
}

func invokeDraftsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input refArgumentsInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("delete draft", "the validated arguments could not be read")
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
	return client.DeleteDraft(ctx, ref)
}

// appendDraft stores the message in folder with one APPEND and the flag \Draft. Its failure is a change
// failure: after a timeout or a dropped connection the draft may exist.
func appendDraft(conn *session, folder string, raw []byte, suffix string, op string) (*imap.AppendData, error) {
	command := conn.client.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDraft}})
	if _, err := command.Write(raw); err != nil {
		_ = command.Close()
		return nil, changeFailure(op, err, suffix)
	}
	if err := command.Close(); err != nil {
		return nil, changeFailure(op, err, suffix)
	}
	data, err := command.Wait()
	if err != nil {
		return nil, changeFailure(op, err, suffix)
	}
	return data, nil
}

func hasDraftFlag(flags []imap.Flag) bool {
	for _, flag := range flags {
		if strings.EqualFold(string(flag), string(imap.FlagDraft)) {
			return true
		}
	}
	return false
}

// openDraft finds the drafts folder, requires the reference to name it, opens it with SELECT, compares the
// UIDVALIDITY, and reads the message through the sender list. A message without the flag \Draft answers
// not-found exactly like a missing UID, so no other message can be reached through a draft tool.
func (c *Client) openDraft(conn *session, op string, ref messageRef) error {
	folder, err := c.draftsFolder(conn, op)
	if err != nil {
		return err
	}
	if folder != ref.folder {
		return invalidRequest("folder is not the drafts folder of this mailbox")
	}
	selected, err := conn.client.Select(ref.folder, nil).Wait()
	if err != nil {
		return failure(op, err)
	}
	if selected.UIDValidity != ref.uidValidity {
		return invalidRequest("uidvalidity no longer matches this folder; list the folder again")
	}
	loaded, err := c.load(conn, op, ref)
	if err != nil {
		return err
	}
	if !hasDraftFlag(loaded.flags) {
		return errNoSuchMessage(op)
	}
	return nil
}

func draftResult(folder string, data *imap.AppendData, plan *draftPlan) *DraftResult {
	result := &DraftResult{Folder: folder, Size: int64(len(plan.raw)), Attachments: plan.attachments}
	if data != nil {
		result.UID, result.UIDValidity = uint32(data.UID), data.UIDValidity
	}
	return result
}

// CreateDraft stores a new draft in the drafts folder with one APPEND.
func (c *Client) CreateDraft(ctx context.Context, plan *draftPlan) (*DraftResult, error) {
	const op = "create draft"
	conn, err := c.connectWithin(ctx, op, plan.timeout())
	if err != nil {
		return nil, err
	}
	defer conn.close()
	folder, err := c.draftsFolder(conn, op)
	if err != nil {
		return nil, err
	}
	data, err := appendDraft(conn, folder, plan.raw, uncertain, op)
	if err != nil {
		return nil, err
	}
	return draftResult(folder, data, plan), nil
}

// UpdateDraft replaces one draft: it stores the new draft with one APPEND and then removes the old UID with
// UID STORE and UID EXPUNGE for that UID alone. UIDPLUS is required and checked before anything is written.
// When the removal fails, the error says the new draft exists and the old one may remain; nothing is repeated.
func (c *Client) UpdateDraft(ctx context.Context, ref messageRef, plan *draftPlan) (*DraftResult, error) {
	const op = "update draft"
	conn, err := c.connectWithin(ctx, op, plan.timeout())
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if !conn.client.Caps().Has(imap.CapUIDPlus) {
		return nil, unsupported(op, "UID EXPUNGE (UIDPLUS)")
	}
	if err := c.openDraft(conn, op, ref); err != nil {
		return nil, err
	}
	data, err := appendDraft(conn, ref.folder, plan.raw, draftUncertainUpdate, op)
	if err != nil {
		return nil, err
	}
	known := ""
	if data != nil && data.UID != 0 {
		known = " as uid " + strconv.FormatUint(uint64(data.UID), 10)
	}
	if err := expungeSelected(conn, op, ref, fmt.Sprintf(draftReplaced, known), true); err != nil {
		return nil, err
	}
	result := draftResult(ref.folder, data, plan)
	result.ReplacedUID = ref.uid
	return result, nil
}

// DeleteDraft removes one draft for good with UID EXPUNGE for its UID alone.
func (c *Client) DeleteDraft(ctx context.Context, ref messageRef) (*MessageState, error) {
	const op = "delete draft"
	conn, err := c.connect(ctx, op)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if !conn.client.Caps().Has(imap.CapUIDPlus) {
		return nil, unsupported(op, "UID EXPUNGE (UIDPLUS)")
	}
	if err := c.openDraft(conn, op, ref); err != nil {
		return nil, err
	}
	if err := expungeSelected(conn, op, ref, "", false); err != nil {
		return nil, err
	}
	state := stateOf(ref)
	return &state, nil
}
