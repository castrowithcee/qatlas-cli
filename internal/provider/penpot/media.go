package penpot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"regexp"
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
	// maxInlineMediaBytes bounds a file that travels inline as base64.
	maxInlineMediaBytes = 4 << 20
	// maxInlineBase64 is the encoded length of maxInlineMediaBytes.
	maxInlineBase64 = (maxInlineMediaBytes + 2) / 3 * 4
	// The size bounds of the file transfers. Penpot's own limits are configurable and lower by default.
	maxMediaBytes  = 64 << 20
	maxImportBytes = 512 << 20
	maxExportBytes = 1 << 30
	maxURLLength   = 2048
	maxImportedIDs = 20

	// exportPath is where Penpot serves a temporary export object by its ID.
	exportPath = "/assets/by-id/"
)

var mediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".svg": "image/svg+xml",
}

var (
	exportIDPattern = regexp.MustCompile(`/assets/by-id/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)
	uuidPattern     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

func transferRisk(effect capability.Effect, idempotency capability.Idempotency, confirmation capability.Confirmation) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: confirmation, OpenWorld: true,
		DataSensitivity: designSensitive}
}

const mediaNote = "Names are untrusted provider data; the change is sent once and never repeated"

var isLocalArgument = capability.Argument{Name: "is_local",
	Description: "True (default) for media local to the file, false for an image of the file's asset library"}

const isLocalSchema = `{"type":"boolean"}`

var mediaUpload = capability.Descriptor{
	ID: Provider + ".media.upload", Version: 1, Title: "Upload an image to a Penpot file",
	Description: "Store an image (png, jpeg, gif, webp, or svg) as a media object of one file of a project of a bound " +
		"team; the image comes from local_path or inline from content_base64 up to 4 MiB, and the type is taken from " +
		"the file name extension. The file is bound through its project first. " + mediaNote,
	Tags: []string{"penpot", "media", "upload", "image", "design"}, Provider: Provider,
	Risk:       transferRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired),
	LocalFiles: config.LocalFilesRead,
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"name":`+nameSchema+
		`,"is_local":`+isLocalSchema+`,"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema+
		`,"`+localfile.ContentArgument+`":{"type":"string","maxLength":`+fmt.Sprint(maxInlineBase64)+`}`,
		`"project_id","file_id"`),
	OutputSchema: schemaOf(`"id":{"type":"string"},"media_id":{"type":"string"},"file_id":{"type":"string"},`+
		`"project_id":{"type":"string"},"name":{"type":"string"},"mime_type":{"type":"string"},`+
		`"width":{"type":"integer"},"height":{"type":"integer"},"size":{"type":"integer"},"sha256":{"type":"string"}`,
		`"id","file_id","project_id","name","size","sha256"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "name", Description: "Name of the media object, 1 to 250 characters; required with " +
			localfile.ContentArgument + " (and then also names the image type by its extension), otherwise the name of the local file"},
		isLocalArgument,
		localfile.UploadPathArgument(),
		{Name: localfile.ContentArgument, Description: "Image as base64, up to 4 MiB; instead of " + localfile.LocalPathArgument}},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the media object of the file"},
		{Name: "media_id", Description: "Identifier of the stored image, when Penpot reports one"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
		{Name: "name", Description: "Name of the media object, untrusted data"},
		{Name: "mime_type", Description: "Image type as Penpot reports it"},
		{Name: "width", Description: "Width in pixels, when Penpot reports it"},
		{Name: "height", Description: "Height in pixels, when Penpot reports it"},
		{Name: "size", Description: "Size of the uploaded image in bytes"},
		{Name: "sha256", Description: "SHA-256 of the uploaded content as hex"},
	},
	Examples: []capability.Example{{Description: "Upload a local image",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","local_path":"~/uploads/logo.png"}`)}},
}

var mediaFromURL = capability.Descriptor{
	ID: Provider + ".media.fromurl", Version: 1, Title: "Add an image to a Penpot file from a URL",
	Description: "Let Penpot fetch an image from an https URL and store it as a media object of one file of a project " +
		"of a bound team. Qatlas accepts only an https URL with a public DNS host name on the default port, without " +
		"user info or fragment; IP addresses, single-label names, and local or internal name suffixes are refused. " +
		"Penpot itself fetches the URL and follows up to three redirects. " + mediaNote,
	Tags: []string{"penpot", "media", "url", "image", "design"}, Provider: Provider,
	Risk: transferRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"url":{"type":"string","minLength":1,"maxLength":`+
		fmt.Sprint(maxURLLength)+`},"name":`+nameSchema+`,"is_local":`+isLocalSchema, `"project_id","file_id","url"`),
	OutputSchema: schemaOf(`"id":{"type":"string"},"media_id":{"type":"string"},"file_id":{"type":"string"},`+
		`"project_id":{"type":"string"},"name":{"type":"string"},"mime_type":{"type":"string"},`+
		`"width":{"type":"integer"},"height":{"type":"integer"}`, `"id","file_id","project_id"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "url", Required: true, Description: "https URL of the image, up to 2048 characters; the URL is not repeated in the answer"},
		{Name: "name", Description: "Name of the media object, 1 to 250 characters; Penpot uses 'unknown' when omitted"},
		isLocalArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the media object of the file"},
		{Name: "media_id", Description: "Identifier of the stored image, when Penpot reports one"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
		{Name: "name", Description: "Name of the media object, untrusted data"},
		{Name: "mime_type", Description: "Image type as Penpot reports it"},
		{Name: "width", Description: "Width in pixels, when Penpot reports it"},
		{Name: "height", Description: "Height in pixels, when Penpot reports it"},
	},
	Examples: []capability.Example{{Description: "Add an image from a public URL",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","url":"https://images.example.com/logo.png","name":"Logo"}`)}},
}

var filesExport = capability.Descriptor{
	ID: Provider + ".files.export", Version: 1, Title: "Export a Penpot file",
	Description: "Export one file of a project of a bound team as a .penpot archive and write it to local_path. The " +
		"archive holds the file only: libraries are not included and their assets are not embedded. The file is bound " +
		"through its project first. Penpot keeps a temporary copy of the export for about an hour. The answer carries " +
		"metadata only; an existing local file is replaced only with confirmation",
	Tags: []string{"penpot", "files", "export", "download", "design"}, Provider: Provider,
	Risk:        transferRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	LocalFiles:  config.LocalFilesWrite,
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema, `"project_id","file_id","`+localfile.LocalPathArgument+`"`),
	OutputSchema: schemaOf(`"file_id":{"type":"string"},"project_id":{"type":"string"},"size":{"type":"integer"},`+
		`"sha256":{"type":"string"}`, `"file_id","project_id","size","sha256"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		func() capability.Argument {
			argument := localfile.DownloadPathArgument()
			argument.Required = true
			return argument
		}()},
	Fields: []capability.Field{
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
		{Name: "size", Description: "Size of the written archive in bytes"},
		{Name: "sha256", Description: "SHA-256 of the written archive as hex"},
	},
	Examples: []capability.Example{{Description: "Export a file to a local path",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","local_path":"~/exports/landing.penpot"}`)}},
}

var filesImport = capability.Descriptor{
	ID: Provider + ".files.import", Version: 1, Title: "Import a Penpot file",
	Description: "Import a .penpot archive from local_path as a new file in one project of a bound team. Penpot " +
		"creates new files and never overwrites an existing one; the archive format and its content are checked by " +
		"Penpot alone. The archive is sent in one request, so it is limited to 512 MiB and by the upload limit of " +
		"the instance. " + mediaNote,
	Tags: []string{"penpot", "files", "import", "upload", "design"}, Provider: Provider,
	Risk:        transferRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired),
	LocalFiles:  config.LocalFilesRead,
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"name":`+nameSchema+`,"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema, `"project_id","name","`+localfile.LocalPathArgument+`"`),
	OutputSchema: schemaOf(`"imported":{"type":"boolean"},"project_id":{"type":"string"},"file_ids":{"type":"array","items":{"type":"string"}},`+
		`"size":{"type":"integer"},"sha256":{"type":"string"}`, `"imported","project_id","file_ids","size","sha256"`),
	Arguments: []capability.Argument{projectIDArgument,
		{Name: "name", Required: true, Description: "Name of the imported file, 1 to 250 characters"},
		func() capability.Argument {
			argument := localfile.UploadPathArgument()
			argument.Required = true
			argument.Description = "Local .penpot archive to import, absolute or starting with ~/, inside a directory the connection releases for reading"
			return argument
		}()},
	Fields: []capability.Field{
		{Name: "imported", Description: "True when Penpot reported the import as done"},
		{Name: "project_id", Description: "Identifier of the project"},
		{Name: "file_ids", Description: "Identifiers of the new files, at most 20, when Penpot reports them"},
		{Name: "size", Description: "Size of the imported archive in bytes"},
		{Name: "sha256", Description: "SHA-256 of the imported archive as hex"},
	},
	Examples: []capability.Example{{Description: "Import an archive into a project",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","name":"Landing page","local_path":"~/exports/landing.penpot"}`)}},
}

// MediaStored is the answer of media.upload and media.fromurl; Size and SHA256 are set for an upload only.
type MediaStored struct {
	ID        string `json:"id"`
	MediaID   string `json:"media_id,omitempty"`
	FileID    string `json:"file_id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	Width     int64  `json:"width,omitempty"`
	Height    int64  `json:"height,omitempty"`
	Size      *int64 `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// FileExported and FileImported are the answers of files.export and files.import.
type FileExported struct {
	FileID    string `json:"file_id"`
	ProjectID string `json:"project_id"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type FileImported struct {
	Imported  bool     `json:"imported"`
	ProjectID string   `json:"project_id"`
	FileIDs   []string `json:"file_ids"`
	Size      int64    `json:"size"`
	SHA256    string   `json:"sha256"`
}

type mediaArguments struct {
	ProjectID string  `json:"project_id"`
	FileID    string  `json:"file_id"`
	Name      *string `json:"name"`
	IsLocal   *bool   `json:"is_local"`
	URL       string  `json:"url"`
	LocalPath *string `json:"local_path"`
	Content   *string `json:"content_base64"`
}

func readMedia(op string, raw json.RawMessage) (mediaArguments, error) {
	var input mediaArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// selectFile is the local part of binding a file: project and file ID, before any secret is resolved.
func selectFile(resolved *config.Resolved, projectArgument, fileArgument string) (string, string, error) {
	projectID, err := selectProject(resolved, projectArgument)
	if err != nil {
		return "", "", err
	}
	fileID, ok := parseUUID(fileArgument)
	if !ok {
		return "", "", invalidRequest("file_id must be a UUID")
	}
	return projectID, fileID, nil
}

// validMediaName accepts the name of a media object: valid text, 1 to 250 characters, no control characters,
// and no path separator, since it also serves as the file name of the upload.
func validMediaName(name string) (string, error) {
	name, err := checkName(name)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(name, `/\`) {
		return "", invalidRequest("name must not contain path separators")
	}
	return name, nil
}

// mediaType returns the image type that a file name's extension names.
func mediaType(name string) (string, error) {
	mtype, ok := mediaTypes[strings.ToLower(path.Ext(name))]
	if !ok {
		return "", invalidRequest("the file name must end in .png, .jpg, .jpeg, .gif, .webp, or .svg")
	}
	return mtype, nil
}

// checkMediaURL is the Qatlas side of the boundary for a URL that Penpot fetches itself. It is syntactic: it
// accepts https URLs only, on the default port, without user info or fragment, whose host is a public-looking
// DNS name of at least two labels with an alphabetic last label. Every IP literal (also in decimal, octal, or
// hexadecimal form), single-label name, and name that ends in a local or internal suffix is refused. It cannot
// see what the name resolves to or where Penpot is redirected.
func checkMediaURL(raw string) error {
	const reason = "url must be an https URL of a public DNS host name, without user info, fragment, or a port other than 443"
	if raw == "" || len(raw) > maxURLLength || strings.ContainsAny(raw, `\`) {
		return invalidRequest(reason)
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f || r > 0x7e {
			return invalidRequest(reason)
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" ||
		strings.Contains(raw, "#") || parsed.Host == "" || (parsed.Port() != "" && parsed.Port() != "443") {
		return invalidRequest(reason)
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	labels := strings.Split(host, ".")
	if len(labels) < 2 || len(host) > 253 {
		return invalidRequest(reason)
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return invalidRequest(reason)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return invalidRequest(reason)
			}
		}
	}
	last := labels[len(labels)-1]
	if !strings.ContainsAny(last, "abcdefghijklmnopqrstuvwxyz") {
		return invalidRequest(reason)
	}
	for _, suffix := range []string{"localhost", "local", "internal", "intranet", "lan", "home", "corp", "localdomain",
		"home.arpa", "private"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return invalidRequest(reason)
		}
	}
	return nil
}

// mediaAnswer reads the file media object Penpot answers with. An answer without a media object ID leaves the
// result open, since the request was sent.
func mediaAnswer(op string, data []byte, projectID, fileID string) (*MediaStored, error) {
	answer, ok := asObj(data)
	if !ok || answer.id("id") == "" {
		return nil, invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	return &MediaStored{ID: answer.id("id"), MediaID: answer.id("mediaid"), FileID: fileID, ProjectID: projectID,
		Name: answer.str("name"), MimeType: answer.str("mtype"), Width: answer.integer("width"),
		Height: answer.integer("height")}, nil
}

// multipartBody frames a multipart body by hand, so that its length is known and a file streams without
// being held in memory. The file part is named partName.
func multipartBody(fields [][2]string, partName, fileName, contentType string, size int64, source io.Reader) (
	body io.Reader, length int64, formType string, err error) {
	var head bytes.Buffer
	form := multipart.NewWriter(&head)
	for _, field := range fields {
		if err := form.WriteField(field[0], field[1]); err != nil {
			return nil, 0, "", err
		}
	}
	disposition := mime.FormatMediaType("form-data", map[string]string{"name": partName, "filename": fileName})
	if disposition == "" {
		return nil, 0, "", io.ErrUnexpectedEOF
	}
	if _, err := form.CreatePart(textproto.MIMEHeader{"Content-Disposition": {disposition},
		"Content-Type": {contentType}}); err != nil {
		return nil, 0, "", err
	}
	tail := "\r\n--" + form.Boundary() + "--\r\n"
	body = io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(source, size), strings.NewReader(tail))
	return body, int64(head.Len()) + size + int64(len(tail)), form.FormDataContentType(), nil
}

// finishUpload makes sure the local file ended where its announced size did and returns its SHA-256.
func finishUpload(op string, upload *localfile.Upload) (string, error) {
	if _, err := io.Copy(io.Discard, upload); err != nil {
		return "", providerError(op, "the local file changed while it was uploaded")
	}
	sum, ok := upload.SHA256()
	if !ok {
		return "", providerError(op, "the local file could not be read completely")
	}
	return sum, nil
}

func invokeMediaUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload media"
	input, err := readMedia(op, raw)
	if err != nil {
		return nil, err
	}
	projectID, fileID, err := selectFile(resolved, input.ProjectID, input.FileID)
	if err != nil {
		return nil, err
	}
	var (
		source   io.Reader
		upload   *localfile.Upload
		fileName string
		size     int64
		content  []byte
	)
	// The local file is opened before the credential is resolved, so a path outside the release is refused
	// without secret access or provider I/O.
	kind, err := localfile.UploadSource(input.LocalPath, input.Content)
	if err != nil {
		return nil, err
	}
	if kind == localfile.SourceLocalPath {
		if upload, err = localfile.OpenForUpload(ctx, resolved, *input.LocalPath); err != nil {
			return nil, err
		}
		defer upload.Close()
		fileName, size, source = upload.Name, upload.Size, upload
	} else {
		if input.Name == nil {
			return nil, invalidRequest("name is required with " + localfile.ContentArgument)
		}
		if len(*input.Content) > maxInlineBase64 {
			return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		if content, err = base64.StdEncoding.DecodeString(*input.Content); err != nil {
			return nil, invalidRequest(localfile.ContentArgument + " is not valid base64")
		}
		if len(content) > maxInlineMediaBytes {
			return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		fileName, size, source = *input.Name, int64(len(content)), bytes.NewReader(content)
	}
	if fileName, err = validMediaName(fileName); err != nil {
		return nil, err
	}
	name := fileName
	if input.Name != nil {
		if name, err = validMediaName(*input.Name); err != nil {
			return nil, err
		}
	}
	mtype, err := mediaType(fileName)
	if err != nil {
		return nil, err
	}
	if size == 0 || size > maxMediaBytes {
		return nil, invalidRequest("the image must not be empty and is limited to 64 MiB")
	}
	isLocal := input.IsLocal == nil || *input.IsLocal
	body, length, formType, err := multipartBody([][2]string{{"file-id", fileID}, {"is-local", fmt.Sprint(isLocal)},
		{"name", name}}, "content", fileName, mtype, size, source)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.fileOf(ctx, op, projectID, fileID); err != nil {
		return nil, err
	}
	data, err := client.sendChange(ctx, op, cmdUploadMedia, body, length, formType, transferTimeout)
	if err != nil {
		return nil, err
	}
	result, err := mediaAnswer(op, data, projectID, fileID)
	if err != nil {
		return nil, err
	}
	if upload != nil {
		if result.SHA256, err = finishUpload(op, upload); err != nil {
			return nil, err
		}
	} else {
		digest := sha256.Sum256(content)
		result.SHA256 = hex.EncodeToString(digest[:])
	}
	result.Size = &size
	return result, nil
}

func invokeMediaFromURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add media from url"
	input, err := readMedia(op, raw)
	if err != nil {
		return nil, err
	}
	projectID, fileID, err := selectFile(resolved, input.ProjectID, input.FileID)
	if err != nil {
		return nil, err
	}
	if err := checkMediaURL(input.URL); err != nil {
		return nil, err
	}
	params := map[string]any{"file-id": fileID, "url": input.URL, "is-local": input.IsLocal == nil || *input.IsLocal}
	if input.Name != nil {
		if params["name"], err = validMediaName(*input.Name); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.fileOf(ctx, op, projectID, fileID); err != nil {
		return nil, err
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	data, err := client.sendChange(ctx, op, cmdMediaFromURL, bytes.NewReader(body), int64(len(body)), "application/json", fetchTimeout)
	if err != nil {
		return nil, err
	}
	return mediaAnswer(op, data, projectID, fileID)
}

// sendChange sends one change command with a prepared body and a timeout that fits it.
func (c *Client) sendChange(ctx context.Context, op, command string, body io.Reader, length int64, contentType string,
	timeout time.Duration) ([]byte, error) {
	if !isChange(command) {
		return nil, providerError(op, "the command is not offered")
	}
	return c.send(ctx, op, command, body, length, contentType, c.withTimeout(timeout), true)
}

// trackedReader remembers a failure of the source, so it can be told from a failure of the local file.
type trackedReader struct {
	io.Reader
	err error
}

func (t *trackedReader) Read(p []byte) (int, error) {
	n, err := t.Reader.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}

func invokeFilesExport(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "export file"
	var input struct {
		ProjectID string  `json:"project_id"`
		FileID    string  `json:"file_id"`
		LocalPath *string `json:"local_path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil || input.LocalPath == nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	projectID, fileID, err := selectFile(resolved, input.ProjectID, input.FileID)
	if err != nil {
		return nil, err
	}
	// The target is prepared before the credential is resolved, so a path outside the release, or a file that
	// would be replaced without confirmation, is refused without secret access or provider I/O.
	download, err := localfile.CreateForDownload(ctx, resolved, *input.LocalPath)
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = download.Abort()
		}
	}()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.fileOf(ctx, op, projectID, fileID); err != nil {
		return nil, err
	}
	// Libraries stay out of the archive: their files may lie outside this connection's projects.
	body, err := json.Marshal(map[string]any{"file-id": fileID, "include-libraries": false, "embed-assets": false})
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	data, err := client.send(ctx, op, cmdExportFile, bytes.NewReader(body), int64(len(body)), "application/json",
		client.withTimeout(transferTimeout), false)
	if err != nil {
		return nil, err
	}
	result, err := streamResult(op, data)
	if err != nil {
		return nil, err
	}
	// Only the ID of the temporary object is taken from the answer; the host is always the connection's own.
	match := exportIDPattern.FindSubmatch(result)
	if match == nil {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	if err := client.fetchExport(ctx, op, strings.ToLower(string(match[1])), download); err != nil {
		return nil, err
	}
	done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	return &FileExported{FileID: fileID, ProjectID: projectID, Size: download.Size(), SHA256: sum}, nil
}

// fetchExport reads the temporary export object from the connection's own origin, without the token: the
// object is addressed by its random ID alone.
func (c *Client) fetchExport(ctx context.Context, op, id string, download *localfile.Download) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+exportPath+id, nil)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("User-Agent", "qatlas-cli")
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Penpot", err)
	}
	response, err := c.withTimeout(transferTimeout).Do(req)
	if err != nil {
		return provider.Transport(op, "Penpot", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return c.statusError(op, response)
	}
	if response.ContentLength > maxExportBytes {
		return providerError(op, "the export is larger than the limit of 1 GiB")
	}
	if response.ContentLength >= 0 {
		if err := download.ExpectSize(response.ContentLength); err != nil {
			return err
		}
	}
	body := &trackedReader{Reader: io.LimitReader(response.Body, maxExportBytes+1)}
	if _, err := download.ReadFrom(body); err != nil {
		if body.err != nil {
			return provider.Transport(op, "Penpot", body.err)
		}
		return err
	}
	if download.Size() > maxExportBytes {
		return providerError(op, "the export is larger than the limit of 1 GiB")
	}
	return nil
}

func invokeFilesImport(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "import file"
	var input struct {
		ProjectID string  `json:"project_id"`
		Name      string  `json:"name"`
		LocalPath *string `json:"local_path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil || input.LocalPath == nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	projectID, err := selectProject(resolved, input.ProjectID)
	if err != nil {
		return nil, err
	}
	name, err := checkName(input.Name)
	if err != nil {
		return nil, err
	}
	upload, err := localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	if upload.Size == 0 || upload.Size > maxImportBytes {
		return nil, invalidRequest("the archive must not be empty and is limited to 512 MiB")
	}
	body, length, formType, err := multipartBody([][2]string{{"project-id", projectID}, {"name", name}},
		"file", "import.penpot", "application/octet-stream", upload.Size, upload)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.locateProject(ctx, op, projectID); err != nil {
		return nil, err
	}
	data, err := client.sendChange(ctx, op, cmdImportFile, body, length, formType, transferTimeout)
	if err != nil {
		return nil, err
	}
	result, err := streamResult(op, data)
	if err != nil {
		return nil, err
	}
	sum, err := finishUpload(op, upload)
	if err != nil {
		return nil, err
	}
	imported := &FileImported{Imported: true, ProjectID: projectID, FileIDs: []string{}, Size: upload.Size, SHA256: sum}
	for _, id := range uuidPattern.FindAll(result, -1) {
		if len(imported.FileIDs) >= maxImportedIDs {
			break
		}
		if fileID := strings.ToLower(string(id)); fileID != projectID {
			imported.FileIDs = append(imported.FileIDs, fileID)
		}
	}
	return imported, nil
}
