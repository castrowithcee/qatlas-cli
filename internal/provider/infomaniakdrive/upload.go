package infomaniakdrive

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxInlineFileBytes bounds a file that travels inline as base64.
	maxInlineFileBytes = 4 << 20
	// maxInlineBase64 is the encoded length of maxInlineFileBytes.
	maxInlineBase64 = (maxInlineFileBytes + 2) / 3 * 4
	// transferTimeout replaces the 30 s of the client for each request that carries file content.
	transferTimeout = 30 * time.Minute
	// cancelTimeout bounds the one attempt to cancel an upload session after a failure.
	cancelTimeout = 30 * time.Second
	// maxChunks is the number of chunks Infomaniak accepts for one session.
	maxChunks = 10000
	// uploadHostSuffix is the only domain a chunk may be sent to, besides the API host itself.
	uploadHostSuffix = ".infomaniak.com"
)

// The size of a file up to which one request carries it, and the size of one chunk of a session. Infomaniak
// documents 1 GB for a direct upload and recommends a session above 100 MB; a chunk is 1 MB to 1 GB. Tests
// lower both.
var (
	directUploadLimit int64 = 100_000_000
	chunkSize         int64 = 64 << 20
)

// Upload methods reported in a result.
const (
	methodDirect  = "direct"
	methodSession = "session"
)

const uploadSchemaProperties = `"drive_id":` + idSchema + `,"directory_id":` + objectIDSchema + `,"file_id":` +
	objectIDSchema + `,"name":` + nameSchema + `,"etag":{"type":"string","pattern":"^([a-fA-F0-9]{16,32}|blank)$"},` +
	`"conflict":` + conflictSchema + `,"client_token":{"type":"string","minLength":16,"maxLength":36,"pattern":"^[A-Za-z0-9_!-]+$"},`

var etagPattern = regexp.MustCompile(`^([a-fA-F0-9]{16,32}|blank)$`)
var clientTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_!-]{16,36}$`)
var sessionTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

var filesUpload = capability.Descriptor{
	ID:      Provider + ".files.upload",
	Version: 1,
	Title:   "Upload a file to Infomaniak kDrive",
	Description: "Create one confirmed file in an existing folder of a drive this connection may reach, or replace the " +
		"content of one existing file when file_id and its current etag are given. The content comes from local_path " +
		"or inline from content_base64 up to 4 MiB. Files up to 100 MB go in one request; larger ones use an " +
		"upload session of several chunk requests automatically. A name that exists is refused unless conflict is " +
		"rename; a missing folder is never created. An unclear outcome is never repeated: the result names the " +
		"client_token (and the session) to check or to repeat the same upload",
	Tags:       []string{"infomaniak", "kdrive", "files", "upload", "replace"},
	Risk:       mutationRisk(capability.EffectUpdate),
	Provider:   Provider,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + uploadSchemaProperties +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,` +
		`"` + localfile.ContentArgument + `":{"type":"string","maxLength":` + strconv.Itoa(maxInlineBase64) + `}},` +
		`"required":["drive_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":{"type":"integer"},` +
		`"file_id":{"type":"integer"},"name":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"},` +
		`"method":{"type":"string","enum":["direct","session"]},"status":{"type":"string","enum":["done","pending"]},` +
		`"client_token":{"type":"string"}},"required":["drive_id","name","size","method","status"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		mutationDriveArgument,
		{Name: "directory_id", Description: "Existing folder of the same drive to create the file in; the drive's root is 1. " +
			"Required unless file_id is given"},
		{Name: "file_id", Description: "Existing file whose content is replaced as a new version; instead of directory_id, " +
			"name, and conflict, and only together with etag"},
		{Name: "name", Description: "Name of the new file, 1 to 255 bytes, without a slash; required with " +
			localfile.ContentArgument + " and not allowed with file_id, and with " + localfile.LocalPathArgument +
			" the name of the local file is used"},
		{Name: "etag", Description: "Current etag of the file to replace, from Infomaniak; required with file_id. " +
			"The replacement is refused when the file changed since"},
		{Name: "conflict", Description: "What to do when the name already exists in the folder: error (default) " +
			"refuses the upload; rename keeps both and gives the new file an available name; only for a new file"},
		{Name: "client_token", Description: "Optional 16 to 36 character token that makes a repeated direct upload " +
			"return the file of the first one; Qatlas generates it when omitted and reports it. Not available " +
			"for sessions of files above 100 MB"},
		localfile.UploadPathArgument(),
		{Name: localfile.ContentArgument, Description: "File content as base64, up to 4 MiB; instead of " + localfile.LocalPathArgument},
	},
	Fields: []capability.Field{
		{Name: "drive_id", Description: "Drive the file was uploaded to"},
		{Name: "file_id", Description: "File Infomaniak created or replaced, when it reports it"},
		{Name: "name", Description: "File name Infomaniak reports, untrusted data"},
		{Name: "size", Description: "Number of bytes sent"},
		{Name: "sha256", Description: "SHA-256 of the sent content as hex"},
		{Name: "method", Description: "direct for one request, session for chunks"},
		{Name: "status", Description: "done when Infomaniak saved the file; pending when it accepted the upload and has not finished it"},
		{Name: "client_token", Description: "Token of a direct upload, to repeat it idempotently"},
	},
	Examples: []capability.Example{
		{Description: "Upload a local file into the root of one drive",
			Arguments: json.RawMessage(`{"drive_id":1,"directory_id":1,"local_path":"~/uploads/report.pdf"}`)},
		{Description: "Replace the content of one file",
			Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"etag":"0123456789abcdef","local_path":"~/uploads/report.pdf"}`)},
	},
}

// UploadResult is the stable answer of one upload. It never carries the content.
type UploadResult struct {
	DriveID     int64  `json:"drive_id"`
	FileID      int64  `json:"file_id,omitempty"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256,omitempty"`
	Method      string `json:"method"`
	Status      string `json:"status"`
	ClientToken string `json:"client_token,omitempty"`
}

type uploadArguments struct {
	DriveID     int64   `json:"drive_id"`
	DirectoryID int64   `json:"directory_id"`
	FileID      int64   `json:"file_id"`
	Name        *string `json:"name"`
	Etag        string  `json:"etag"`
	Conflict    string  `json:"conflict"`
	ClientToken string  `json:"client_token"`
	LocalPath   *string `json:"local_path"`
	Content     *string `json:"content_base64"`
}

// uploadPlan is what the upload sends: where it goes, what it is called, and the content.
type uploadPlan struct {
	driveID, directoryID, fileID int64
	name, etag, conflict, token  string
	size                         int64
	body                         io.Reader
	local                        *localfile.Upload
	sum                          string
}

func (p *uploadPlan) replace() bool { return p.fileID != 0 }

func invokeFilesUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload file"
	var input uploadArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	plan := &uploadPlan{driveID: input.DriveID, directoryID: input.DirectoryID, fileID: input.FileID,
		etag: input.Etag, conflict: input.Conflict, token: input.ClientToken}
	if err := checkUploadTarget(plan, input); err != nil {
		return nil, err
	}
	if err := selectDrive(resolved, input.DriveID); err != nil {
		return nil, err
	}
	// The content is settled before the credential is resolved, so a path outside the release or an
	// invalid inline content is refused without secret access or provider I/O.
	kind, err := localfile.UploadSource(input.LocalPath, input.Content)
	if err != nil {
		return nil, err
	}
	if kind == localfile.SourceLocalPath {
		if input.Name != nil {
			return nil, invalidRequest("name is only used with " + localfile.ContentArgument)
		}
		upload, err := localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
		if err != nil {
			return nil, err
		}
		defer upload.Close()
		plan.local, plan.body, plan.size = upload, upload, upload.Size
		if !plan.replace() {
			plan.name = upload.Name
		}
	} else {
		if len(*input.Content) > maxInlineBase64 {
			return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		content, err := base64.StdEncoding.DecodeString(*input.Content)
		if err != nil {
			return nil, invalidRequest(localfile.ContentArgument + " is not valid base64")
		}
		if len(content) > maxInlineFileBytes {
			return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		digest := sha256.Sum256(content)
		plan.sum = hex.EncodeToString(digest[:])
		plan.body, plan.size = bytes.NewReader(content), int64(len(content))
		if input.Name != nil {
			plan.name = *input.Name
		}
	}
	if !plan.replace() && !validName(plan.name) {
		return nil, invalidRequest(nameReason + "; name is required with " + localfile.ContentArgument)
	}
	session := plan.size > directUploadLimit
	if session && input.ClientToken != "" {
		return nil, invalidRequest("client_token is only available for files up to the direct upload limit of 100 MB")
	}
	if !session && plan.token == "" {
		plan.token, err = newClientToken()
		if err != nil {
			return nil, providerError(op, "no client token could be generated")
		}
	}

	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyDriveAccount(ctx, op, input.DriveID); err != nil {
		return nil, err
	}
	if session {
		return client.uploadSession(ctx, op, plan)
	}
	return client.uploadDirect(ctx, op, plan)
}

// checkUploadTarget validates the identifiers and the combination of destination arguments: a new file needs
// a folder, a replacement needs the file and its etag and nothing that names or places it.
func checkUploadTarget(plan *uploadPlan, input uploadArguments) error {
	if input.Conflict != "" && input.Conflict != conflictError && input.Conflict != conflictRename {
		return invalidRequest("conflict must be error or rename")
	}
	if input.ClientToken != "" && !clientTokenPattern.MatchString(input.ClientToken) {
		return invalidRequest("client_token must be 16 to 36 letters, digits, underscores, or hyphens")
	}
	if input.FileID != 0 {
		switch {
		case !validObjectID(input.FileID) || input.FileID == rootFileID:
			return invalidRequest("file_id must be a positive integer other than the drive's root")
		case input.DirectoryID != 0 || input.Name != nil || input.Conflict != "":
			return invalidRequest("file_id excludes directory_id, name, and conflict")
		case !etagPattern.MatchString(input.Etag):
			return invalidRequest("etag is required with file_id and must be the file's current etag")
		}
		return nil
	}
	if input.Etag != "" {
		return invalidRequest("etag is only used with file_id")
	}
	if !validObjectID(input.DirectoryID) {
		return invalidRequest("directory_id must be a positive integer, or give file_id to replace a file")
	}
	if plan.conflict == "" {
		plan.conflict = conflictError
	}
	return nil
}

func newClientToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// uncertainUpload names what an unclear outcome leaves open and how to repeat it safely.
func uncertainUpload(plan *uploadPlan, sessionToken string) string {
	switch {
	case sessionToken != "":
		return "; the file may have been saved, read the drive before repeating (session " + sessionToken + ")"
	case plan.token != "":
		return "; the file may have been uploaded, read the drive before repeating, or repeat with client_token " +
			plan.token + " to get the file of this upload"
	}
	return "; the file may have been uploaded, read the drive before repeating"
}

// fileOf reads the file resource of an upload answer and checks it against what was asked.
func fileOf(plan *uploadPlan, raw json.RawMessage) (*fileJSON, bool) {
	var item fileJSON
	if json.Unmarshal(raw, &item) != nil || item.ID <= 0 || item.Type != "file" {
		return nil, false
	}
	wrongFile := plan.replace() && item.ID != plan.fileID
	wrongFolder := !plan.replace() && item.ParentID != 0 && item.ParentID != plan.directoryID
	if wrongFile || wrongFolder || item.Size != nil && *item.Size != plan.size {
		return nil, false
	}
	return &item, true
}

// uploadDirect sends the whole file in the one request of the direct upload route.
func (c *Client) uploadDirect(ctx context.Context, op string, plan *uploadPlan) (*UploadResult, error) {
	query := url.Values{"total_size": {strconv.FormatInt(plan.size, 10)}, "client_token": {plan.token}}
	if plan.replace() {
		query.Set("file_id", strconv.FormatInt(plan.fileID, 10))
	} else {
		query.Set("directory_id", strconv.FormatInt(plan.directoryID, 10))
		query.Set("file_name", plan.name)
		query.Set("conflict", plan.conflict)
	}
	header := http.Header{"Content-Type": {"application/octet-stream"}}
	if plan.replace() {
		header.Set("If-Match", plan.etag)
	}
	hint := uncertainUpload(plan, "")
	env, err := c.exchange(ctx, op, exchangeSpec{method: http.MethodPost,
		endpoint: fmt.Sprintf("%s/3/drive/%d/upload?%s", apiRoot, plan.driveID, query.Encode()), header: header,
		body: plan.body, length: plan.size, hint: hint, etag: plan.replace()})
	if err != nil {
		return nil, err
	}
	if err := plan.verifyRead(op, hint); err != nil {
		return nil, err
	}
	return plan.result(op, env, env.Data, methodDirect, hint)
}

// verifyRead makes sure a local file ended where it was announced to, so the hash covers what was sent.
func (p *uploadPlan) verifyRead(op, hint string) error {
	if p.local == nil {
		return nil
	}
	if _, err := io.Copy(io.Discard, p.local); err != nil {
		return providerError(op, "the local file changed while it was uploaded"+hint)
	}
	sum, ok := p.local.SHA256()
	if !ok {
		return providerError(op, "the local file could not be read completely"+hint)
	}
	p.sum = sum
	return nil
}

// result normalises the answer that carries the file. "asynchronous" is a pending upload.
func (p *uploadPlan) result(op string, env *envelope, fileData json.RawMessage, method, hint string) (*UploadResult, error) {
	out := &UploadResult{DriveID: p.driveID, Name: p.name, Size: p.size, SHA256: p.sum, Method: method}
	if method == methodDirect {
		out.ClientToken = p.token
	}
	switch env.Result {
	case "success":
		out.Status = statusDone
	case "asynchronous":
		out.Status = statusPending
	default:
		return nil, invalidResponse(op, "Infomaniak reported an error for a response with an HTTP success status"+hint)
	}
	item, ok := fileOf(p, fileData)
	switch {
	case ok:
		out.FileID, out.Name = item.ID, bounded(item.Name)
	case out.Status == statusDone:
		return nil, invalidResponse(op, "Infomaniak answered the upload without a usable file"+hint)
	case p.replace():
		out.FileID = p.fileID
	}
	return out, nil
}

type sessionStart struct {
	Token     string `json:"token"`
	UploadURL string `json:"upload_url"`
}

type sessionChunk struct {
	Number int64 `json:"number"`
	Size   int64 `json:"size"`
}

type sessionFinish struct {
	File json.RawMessage `json:"file"`
}

// uploadSession opens one session, sends the file chunk by chunk to the upload URL Infomaniak named, and
// closes it. Every request is sent once; a failure cancels the session once.
func (c *Client) uploadSession(ctx context.Context, op string, plan *uploadPlan) (*UploadResult, error) {
	chunks := (plan.size + chunkSize - 1) / chunkSize
	if chunks > maxChunks {
		return nil, invalidRequest("the file needs more chunks than one upload session allows")
	}
	body := map[string]any{"total_size": plan.size, "total_chunks": chunks}
	header := http.Header{"Content-Type": {"application/json"}}
	if plan.replace() {
		body["file_id"] = plan.fileID
		header.Set("If-Match", plan.etag)
	} else {
		body["directory_id"], body["file_name"], body["conflict"] = plan.directoryID, plan.name, plan.conflict
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	startHint := "; a session may have been opened without a result, it expires after 4 hours, read the drive before repeating"
	env, err := c.exchange(ctx, op, exchangeSpec{method: http.MethodPost,
		endpoint: fmt.Sprintf("%s/3/drive/%d/upload/session/start", apiRoot, plan.driveID), header: header,
		body: bytes.NewReader(encoded), length: int64(len(encoded)), hint: startHint, etag: plan.replace()})
	if err != nil {
		return nil, err
	}
	var start sessionStart
	if env.Result != "success" || json.Unmarshal(env.Data, &start) != nil || !sessionTokenPattern.MatchString(start.Token) {
		return nil, invalidResponse(op, "Infomaniak answered the session start without a usable session"+startHint)
	}
	token := start.Token
	target, ok := chunkTarget(start.UploadURL, plan.driveID, token)
	if !ok {
		return nil, c.abortSession(ctx, op, plan, token, invalidResponse(op,
			"Infomaniak named an upload location that is not an Infomaniak https address; nothing was sent to it"))
	}

	for number := int64(1); number <= chunks; number++ {
		length := min(chunkSize, plan.size-(number-1)*chunkSize)
		query := url.Values{"chunk_number": {strconv.FormatInt(number, 10)}, "chunk_size": {strconv.FormatInt(length, 10)}}
		endpoint := *target
		endpoint.RawQuery = query.Encode()
		env, err := c.exchange(ctx, op, exchangeSpec{method: http.MethodPost, endpoint: endpoint.String(),
			header: http.Header{"Content-Type": {"application/octet-stream"}},
			body:   io.LimitReader(plan.body, length), length: length, hint: "; chunk " + strconv.FormatInt(number, 10) +
				" may have been received"})
		if err != nil {
			return nil, c.abortSession(ctx, op, plan, token, err)
		}
		var got sessionChunk
		if env.Result != "success" || json.Unmarshal(env.Data, &got) != nil || got.Number != number || got.Size != length {
			return nil, c.abortSession(ctx, op, plan, token, invalidResponse(op,
				"Infomaniak answered a chunk without a matching result"))
		}
	}
	if err := plan.verifyRead(op, ""); err != nil {
		return nil, c.abortSession(ctx, op, plan, token, err)
	}

	hint := uncertainUpload(plan, token)
	unclear := false
	env, err = c.exchange(ctx, op, exchangeSpec{method: http.MethodPost,
		endpoint: fmt.Sprintf("%s/3/drive/%d/upload/session/%s/finish", apiRoot, plan.driveID, token),
		header:   http.Header{"Content-Type": {"application/json"}}, body: strings.NewReader("{}"), length: 2, hint: hint, unclear: &unclear})
	if err != nil {
		// An unclear finish may have saved the file; cancelling could erase it, so the session is left to
		// expire and the uncertainty is reported with its token.
		if !unclear {
			return nil, c.abortSession(ctx, op, plan, token, err)
		}
		return nil, err
	}
	var done sessionFinish
	if json.Unmarshal(env.Data, &done) != nil {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response"+hint)
	}
	return plan.result(op, env, done.File, methodSession, hint)
}

// abortSession cancels an open session once, with a context that outlives the request, and returns the
// failure that caused it, extended by what the cancellation achieved.
func (c *Client) abortSession(ctx context.Context, op string, plan *uploadPlan, token string, cause error) error {
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelTimeout)
	defer cancel()
	env, err := c.exchange(cancelCtx, op, exchangeSpec{method: http.MethodDelete,
		endpoint: fmt.Sprintf("%s/3/drive/%d/upload/session/%s", apiRoot, plan.driveID, token),
		hint:     "; the session could not be cancelled"})
	note := "; the upload session was cancelled and the file was not saved"
	if err != nil || env.Result != "success" {
		note = "; the upload session could not be cancelled and expires after 4 hours (session " + token + ")"
	}
	var providerErr *provider.Error
	if errors.As(cause, &providerErr) {
		copied := *providerErr
		copied.Message += note
		return &copied
	}
	return fmt.Errorf("%w%s", cause, note)
}

// chunkTarget accepts the upload location of a session only when it is an https address of Infomaniak with
// no credentials, query, or fragment, naming this drive's chunk route of this session. The token is sent to
// nothing else.
func chunkTarget(raw string, driveID int64, token string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		!infomaniakHost(u.Hostname()) || (u.Port() != "" && u.Port() != "443") ||
		u.Path != path.Clean(u.Path) || u.RawPath != "" && u.RawPath != u.Path {
		return nil, false
	}
	if u.Path != fmt.Sprintf("/3/drive/%d/upload/session/%s/chunk", driveID, token) {
		return nil, false
	}
	return u, true
}

// infomaniakHost reports whether a host is the API host or a name below infomaniak.com.
func infomaniakHost(host string) bool {
	host = strings.ToLower(host)
	return host == apiHost || strings.HasSuffix(host, uploadHostSuffix) && len(host) > len(uploadHostSuffix) &&
		!strings.HasPrefix(host, ".") && !strings.Contains(host, "..")
}

// exchangeSpec is one request of an upload. The endpoint is built by this package from fixed paths and, for a
// chunk, from the validated upload location.
type exchangeSpec struct {
	method, endpoint string
	header           http.Header
	body             io.Reader
	length           int64
	// hint is appended to a failure whose request may have arrived; unclear, when not nil, is set when it was.
	hint    string
	unclear *bool
	// etag marks a request with If-Match, whose 409 and 412 mean the etag no longer matches.
	etag bool
}

func (s exchangeSpec) markUnclear() {
	if s.unclear != nil {
		*s.unclear = true
	}
}

// exchange sends exactly one request with the long transfer timeout and decodes the envelope. The token goes
// only to https addresses of Infomaniak, and no redirect is followed.
func (c *Client) exchange(ctx context.Context, op string, spec exchangeSpec) (*envelope, error) {
	target, err := url.Parse(spec.endpoint)
	if err != nil || target.Scheme != "https" || target.User != nil || !infomaniakHost(target.Hostname()) {
		return nil, providerError(op, "the request could not be built")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Infomaniak", err)
	}
	var body io.Reader = http.NoBody
	if spec.body != nil && spec.length > 0 {
		body = spec.body
	}
	req, err := http.NewRequestWithContext(ctx, spec.method, spec.endpoint, body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.ContentLength = spec.length
	for name, values := range spec.header {
		req.Header[name] = values
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "qatlas-cli")

	client := newHTTPClient()
	client.Timeout = transferTimeout
	response, err := client.Do(req)
	if err != nil {
		failure := transportError(op, err)
		var providerErr *provider.Error
		if errors.As(failure, &providerErr) && (providerErr.Class == provider.ClassTimeout ||
			providerErr.Cause == provider.CauseConnectionReset || providerErr.Cause == provider.CauseUnknown) {
			providerErr.Message += spec.hint
			spec.markUnclear()
		}
		return nil, failure
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		if spec.etag && (response.StatusCode == http.StatusPreconditionFailed || response.StatusCode == http.StatusConflict) {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
			return nil, providerError(op, "etag conflict: the file changed, or is locked, since the given etag was "+
				"read; nothing was replaced, read the file again for its current etag")
		}
		failure := c.statusError(op, response, true)
		if response.StatusCode == http.StatusPreconditionFailed {
			failure.Message = "precondition failed: Infomaniak refused the request; nothing was changed"
		}
		if response.StatusCode >= 500 {
			failure.Message += spec.hint
			spec.markUnclear()
		}
		return nil, failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		spec.markUnclear()
		return nil, invalidResponse(op, "the Infomaniak response could not be read within the size limit"+spec.hint)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		spec.markUnclear()
		return nil, invalidResponse(op, "Infomaniak returned an invalid response"+spec.hint)
	}
	return &env, nil
}
