package penpot

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxFilesListed = 1000
	// Bounds of one file read: shapes of the page, library entries per category.
	maxShapes  = 200
	maxSamples = 20
)

var designRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: designSensitive}

var projectIDArgument = capability.Argument{Name: "project_id", Required: true,
	Description: "Project identifier from penpot.projects.list; must be inside this connection's targets"}

var filesList = capability.Descriptor{
	ID: Provider + ".files.list", Version: 1, Title: "List Penpot files",
	Description: "List the files of one project of a bound team; names are untrusted provider data",
	Tags:        []string{"penpot", "files", "list", "design"}, Risk: metadataRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + uuidSchema +
		`},"required":["project_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"project_id":{"type":"string"},"modified_at":{"type":"string"},"revn":{"type":"integer"},` +
		`"is_shared":{"type":"boolean"},"created_at":` + timestampSchema + `,"vern":{"type":"integer"},` +
		`"thumbnail_id":{"type":"string"},"team_id":{"type":"string"}},` +
		`"required":["id","name","project_id"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["files","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument},
	Fields: []capability.Field{
		{Name: "files", Description: "Files with id (used as file_id by penpot.files.get), name, project_id, modified_at, revn, is_shared, created_at, vern, thumbnail_id, and team_id; names are untrusted provider data"},
		{Name: "count", Description: "Number of files in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at 1000 files"},
	},
	Examples: []capability.Example{{Description: "List the files of a project",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002"}`)}},
}

var filesGet = capability.Descriptor{
	ID: Provider + ".files.get", Version: 1, Title: "Get a Penpot file",
	Description: "Read the library summary of one file of a project and one page of it, bounded: the page carries " +
		"its metadata and at most 200 shapes with the attributes Penpot's schema defines (geometry, styling, " +
		"components, layout, interactions, content). Binary-encoded shape content is not returned, and a value beyond " +
		"the bounds is left out and named in omitted_fields. Identifiers of libraries outside the targets are not " +
		"resolved. Names, text content, and plugin data are untrusted provider data",
	Tags: []string{"penpot", "files", "get", "page", "design"}, Risk: designRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"project_id":` + uuidSchema + `,"file_id":` +
		uuidSchema + `,"page_id":` + uuidSchema + `,"object_ids":{"type":"array","items":` + uuidSchema +
		`,"minItems":1,"maxItems":50,"uniqueItems":true}},"required":["project_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"project_id":{"type":"string"},` +
		`"name":{"type":"string"},"summary":{"type":"object"},"page":` + pageSchema + `},` +
		`"required":["id","project_id","name","summary","page"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument,
		{Name: "file_id", Required: true, Description: "File identifier from penpot.files.list; must be a file of project_id"},
		{Name: "page_id", Description: "Page identifier; the first page of the file when omitted"},
		{Name: "object_ids", Description: "Shape identifiers (1 to 50, no duplicates) that narrow the page to these shapes and their children; requires page_id"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "File identifier"},
		{Name: "project_id", Description: "Project identifier"},
		{Name: "name", Description: "File name"},
		{Name: "summary", Description: "Library summary: components, variants, colors, and typographies, each with a count and up to 20 sample entries (id, name)"},
		{Name: "page", Description: pageDescription},
	},
	Examples: []capability.Example{{Description: "Read the first page of a file",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003"}`)}},
}

// File is one file of a project.
type File struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProjectID   string `json:"project_id"`
	ModifiedAt  string `json:"modified_at,omitempty"`
	Revn        int64  `json:"revn,omitempty"`
	IsShared    bool   `json:"is_shared,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	Vern        *int64 `json:"vern,omitempty"`
	ThumbnailID string `json:"thumbnail_id,omitempty"`
	TeamID      string `json:"team_id,omitempty"`
}

// FilesResult is the answer of files.list.
type FilesResult struct {
	Files     []File `json:"files"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated"`
}

// Sample is one library entry.
type Sample struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Category is the count and a few samples of one kind of library asset.
type Category struct {
	Count   int64    `json:"count"`
	Samples []Sample `json:"samples"`
}

// Summary is the bounded library summary of a file.
type Summary struct {
	Components   Category `json:"components"`
	Variants     Category `json:"variants"`
	Colors       Category `json:"colors"`
	Typographies Category `json:"typographies"`
}

var (
	pageMemberSchema, pageMemberNames   = schemaMembers(attrSchemas(pageAttrs))
	shapeMemberSchema, shapeMemberNames = schemaMembers(attrSchemas(allShapeSpecs()...))
)

var pageSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"shape_count":{"type":"integer"},"truncated":{"type":"boolean"},` + pageMemberSchema +
	`"omitted_fields":{"type":"array","items":{"type":"string"}},"shapes":{"type":"array","items":` +
	`{"type":"object","properties":{"id":{"type":"string"},"type":{"type":"string"},"name":{"type":"string"},` +
	`"parent_id":{"type":"string"},"frame_id":{"type":"string"},"x":{"type":"number"},"y":{"type":"number"},` +
	`"width":{"type":"number"},"height":{"type":"number"},` + shapeMemberSchema +
	`"omitted_fields":{"type":"array","items":{"type":"string"}}},"required":["id","x","y","width","height"],` +
	`"additionalProperties":false}}},"required":["shape_count","truncated","shapes"],"additionalProperties":false}`

var pageDescription = "One page: id, name, shape_count, truncated, the page metadata (" + strings.Join(pageMemberNames, ", ") +
	"), omitted_fields, and up to 200 shapes (id, type, name, parent_id, frame_id, x, y, width, height, omitted_fields, " +
	"and the attributes the shape has: " + strings.Join(shapeMemberNames, ", ") + "). Keys are snake case; " +
	"omitted_fields names the attributes left out because they are binary-encoded or beyond the bounds " +
	"(depth 8, 200 entries, 4 KiB per text); truncated is also set when the attributes exceed 1 MiB"

// Shape is one shape of a page: identity, geometry, and the attributes Penpot's schema defines.
type Shape struct {
	ID       string  `json:"id"`
	Type     string  `json:"type,omitempty"`
	Name     string  `json:"name,omitempty"`
	ParentID string  `json:"parent_id,omitempty"`
	FrameID  string  `json:"frame_id,omitempty"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
	// OmittedFields names the attributes that are binary-encoded or beyond the bounds.
	OmittedFields []string `json:"omitted_fields,omitempty"`
	// Attrs holds the further attributes, keyed by their snake-case names.
	Attrs map[string]json.RawMessage `json:"-"`
}

// MarshalJSON writes the shape with its attributes as members.
func (s Shape) MarshalJSON() ([]byte, error) {
	type plain Shape
	base, err := json.Marshal(plain(s))
	if err != nil {
		return nil, err
	}
	return marshalWith(base, s.Attrs), nil
}

// Page is one bounded page.
type Page struct {
	ID         string  `json:"id,omitempty"`
	Name       string  `json:"name,omitempty"`
	ShapeCount int     `json:"shape_count"`
	Truncated  bool    `json:"truncated"`
	Shapes     []Shape `json:"shapes"`
	// OmittedFields names the page attributes that are beyond the bounds.
	OmittedFields []string `json:"omitted_fields,omitempty"`
	// Attrs holds the page metadata, keyed by snake-case names.
	Attrs map[string]json.RawMessage `json:"-"`
}

// MarshalJSON writes the page with its metadata as members.
func (p Page) MarshalJSON() ([]byte, error) {
	type plain Page
	base, err := json.Marshal(plain(p))
	if err != nil {
		return nil, err
	}
	return marshalWith(base, p.Attrs), nil
}

// FileDetail is the answer of files.get.
type FileDetail struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"project_id"`
	Name      string  `json:"name"`
	Summary   Summary `json:"summary"`
	Page      Page    `json:"page"`
}

func invokeFilesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list files"
	var input struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	projectID, err := selectProject(resolved, input.ProjectID)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.locateProject(ctx, op, projectID); err != nil {
		return nil, err
	}
	files, err := client.filesOf(ctx, op, projectID)
	if err != nil {
		return nil, err
	}
	result := &FilesResult{Files: []File{}}
	for _, file := range files {
		if len(result.Files) >= maxFilesListed {
			result.Truncated = true
			break
		}
		result.Files = append(result.Files, file)
	}
	result.Count = len(result.Files)
	return result, nil
}

// filesOf reads the files of one project. A file that reports another project is dropped.
func (c *Client) filesOf(ctx context.Context, op, projectID string) ([]File, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdProjectFile, map[string]any{"project-id": projectID}, &raw); err != nil {
		return nil, err
	}
	entries, ok := objects(raw)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		id := entry.id("id")
		if id == "" {
			continue
		}
		if reported := entry.id("projectid"); reported != "" && reported != projectID {
			continue
		}
		team := entry.id("teamid")
		if team != "" && !c.scope.allowsTeam(team) {
			continue
		}
		files = append(files, File{ID: id, Name: entry.str("name"), ProjectID: projectID,
			ModifiedAt: entry.str("modifiedat"), Revn: entry.integer("revn"), IsShared: entry.boolean("isshared"),
			CreatedAt: entry.str("createdat"), Vern: entry.optInteger("vern"), ThumbnailID: entry.id("thumbnailid"), TeamID: team})
	}
	return files, nil
}

// fileOf binds a file through its project: the project is located in the bound teams and the file in the
// project's file list. Both are requests made with the credential; neither refusal names the target.
func (c *Client) fileOf(ctx context.Context, op, projectID, fileID string) (*File, error) {
	return c.fileOfAs(ctx, op, "project_id", "file_id", projectID, fileID)
}

// fileOfAs is fileOf for arguments with other names, such as a library file.
func (c *Client) fileOfAs(ctx context.Context, op, projectField, fileField, projectID, fileID string) (*File, error) {
	if !c.scope.allowsFile(projectID, fileID) {
		return nil, invalidRequest(projectField + " is outside the targets of this connection")
	}
	if err := c.locateProjectAs(ctx, op, projectField, projectID); err != nil {
		return nil, err
	}
	files, err := c.filesOf(ctx, op, projectID)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if files[i].ID == fileID {
			return &files[i], nil
		}
	}
	return nil, invalidRequest(fileField + " is not a file of this project")
}

func invokeFilesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get file"
	var input struct {
		ProjectID string   `json:"project_id"`
		FileID    string   `json:"file_id"`
		PageID    string   `json:"page_id"`
		ObjectIDs []string `json:"object_ids"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	projectID, err := selectProjectForFile(resolved, "project_id", input.ProjectID, input.FileID)
	if err != nil {
		return nil, err
	}
	fileID, ok := parseUUID(input.FileID)
	if !ok {
		return nil, invalidRequest("file_id must be a UUID")
	}
	pageID := ""
	if input.PageID != "" {
		if pageID, ok = parseUUID(input.PageID); !ok {
			return nil, invalidRequest("page_id must be a UUID")
		}
	}
	objectIDs, err := parseObjectIDs(input.ObjectIDs, pageID != "")
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	file, err := client.fileOf(ctx, op, projectID, fileID)
	if err != nil {
		return nil, err
	}
	detail := &FileDetail{ID: file.ID, ProjectID: projectID, Name: file.Name}
	var summary json.RawMessage
	if err := client.do(ctx, op, cmdSummary, map[string]any{"id": fileID}, &summary); err != nil {
		return nil, err
	}
	if err := detail.readSummary(summary); err != nil {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	params := map[string]any{"file-id": fileID}
	if pageID != "" {
		params["page-id"] = pageID
	}
	switch len(objectIDs) {
	case 0:
	case 1:
		params["object-id"] = objectIDs[0]
	default:
		params["object-id"] = objectIDs
	}
	var page json.RawMessage
	if err := client.do(ctx, op, cmdPage, params, &page); err != nil {
		return nil, err
	}
	if err := detail.readPage(page); err != nil {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	return detail, nil
}

const maxObjectIDs = 50

// parseObjectIDs validates the shape filter; its errors name no identifier.
func parseObjectIDs(raw []string, hasPage bool) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	if !hasPage {
		return nil, invalidRequest("object_ids requires page_id")
	}
	if len(raw) == 0 || len(raw) > maxObjectIDs {
		return nil, invalidRequest("object_ids must hold 1 to 50 identifiers")
	}
	seen := make(map[string]bool, len(raw))
	ids := make([]string, 0, len(raw))
	for _, r := range raw {
		id, ok := parseUUID(r)
		if !ok {
			return nil, invalidRequest("object_ids must be UUIDs")
		}
		if seen[id] {
			return nil, invalidRequest("object_ids must not repeat an identifier")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

type readError struct{}

func (readError) Error() string { return "unreadable" }

func (d *FileDetail) readSummary(raw json.RawMessage) error {
	summary, ok := asObj(raw)
	if !ok {
		return readError{}
	}
	if name := summary.str("name"); name != "" {
		d.Name = name
	}
	d.Summary = Summary{Components: category(summary["components"]), Variants: category(summary["variants"]),
		Colors: category(summary["colors"]), Typographies: category(summary["typographies"])}
	return nil
}

// category reads {count, sample}; a missing or different shape reads as an empty category.
func category(raw json.RawMessage) Category {
	result := Category{Samples: []Sample{}}
	entry, ok := asObj(raw)
	if !ok {
		return result
	}
	result.Count = entry.integer("count")
	samples, _ := objects(entry["sample"])
	for _, sample := range samples {
		if len(result.Samples) >= maxSamples {
			break
		}
		result.Samples = append(result.Samples, Sample{ID: sample.id("id"), Name: sample.str("name")})
	}
	return result
}

func (d *FileDetail) readPage(raw json.RawMessage) error {
	page, ok := asObj(raw)
	if !ok {
		return readError{}
	}
	d.Page = Page{ID: page.id("id"), Name: page.cleanName("name"), Shapes: []Shape{}}
	meta := readAttrs(page, sortedSpecs(pageAttrs))
	d.Page.Attrs, d.Page.OmittedFields = meta.values, meta.omitted
	var shapes map[string]json.RawMessage
	if err := json.Unmarshal(page["objects"], &shapes); err != nil {
		return nil
	}
	type entry struct {
		id  string
		raw json.RawMessage
	}
	entries := make([]entry, 0, len(shapes))
	for key, value := range shapes {
		if id, ok := parseUUID(key); ok {
			entries = append(entries, entry{id, value})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	d.Page.ShapeCount = len(entries)
	used := 0
	for _, item := range entries {
		if len(d.Page.Shapes) >= maxShapes {
			d.Page.Truncated = true
			break
		}
		shape, _ := asObj(item.raw)
		kind := shape.str("type")
		extra := readAttrs(shape, shapeAttrSpecs(kind))
		if used+extra.size > maxAttrBytes {
			d.Page.Truncated = true
			break
		}
		used += extra.size
		d.Page.Shapes = append(d.Page.Shapes, Shape{ID: item.id, Type: kind, Name: shape.cleanName("name"),
			ParentID: shape.id("parentid"), FrameID: shape.id("frameid"), X: shape.number("x"), Y: shape.number("y"),
			Width: shape.number("width"), Height: shape.number("height"), OmittedFields: extra.omitted, Attrs: extra.values})
	}
	return nil
}
