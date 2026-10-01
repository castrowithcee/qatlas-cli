package infomaniakmail

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxFolders bounds one folder listing; a mailbox with more is reported as truncated.
	maxFolders       = 500
	maxAttributes    = 16
	maxAttributeText = 32
)

var foldersList = capability.Descriptor{
	ID:      Provider + ".folders.list",
	Version: 1,
	Title:   "List Infomaniak mailbox folders",
	Description: "List the folders of the bound mailbox through IMAP LIST, limited to the folder allow-list of " +
		"the connection when it has one; folders only, no messages",
	Tags:        []string{"infomaniak", "mail", "folders", "list"},
	Risk:        mailReadRisk(foldersSensitivity),
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"folders":{"type":"array","items":{"type":"object","properties":{` +
		`"name":{"type":"string"},"delimiter":{"type":"string"},` +
		`"attributes":{"type":"array","items":{"type":"string"}}},` +
		`"required":["name"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["folders","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{},
	Fields: []capability.Field{
		{Name: "name", Description: "Exact folder name, the folder argument of messages.list"},
		{Name: "delimiter", Description: "Hierarchy delimiter of the folder, when the server reports one"},
		{Name: "attributes", Description: "IMAP folder attributes such as \\Sent or \\Noselect, bounded"},
		{Name: "count", Description: "Number of folders returned"},
		{Name: "truncated", Description: "True when the mailbox has more folders than the " +
			"500 this listing returns"},
	},
	Examples: []capability.Example{{Description: "List the folders", Arguments: json.RawMessage(`{}`)}},
}

// Folder is one folder of the bound mailbox.
type Folder struct {
	Name       string   `json:"name"`
	Delimiter  string   `json:"delimiter,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// FoldersPage is the folder listing of the bound mailbox.
type FoldersPage struct {
	Folders   []Folder `json:"folders"`
	Count     int      `json:"count"`
	Truncated bool     `json:"truncated"`
}

func invokeFoldersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListFolders(ctx)
}

// ListFolders runs IMAP LIST and keeps only folders the connection's scope allows. With a folder allow-list
// each listed name is asked for by itself, so no other folder name is requested from the server; every
// answer is filtered locally again.
func (c *Client) ListFolders(ctx context.Context) (*FoldersPage, error) {
	const op = "list folders"
	conn, err := c.connect(ctx, op)
	if err != nil {
		return nil, err
	}
	defer conn.close()

	patterns := []string{"*"}
	if len(c.scope.folders) > 0 {
		patterns = c.scope.folders
	}
	byName := map[string]Folder{}
	for _, pattern := range patterns {
		listed, err := conn.client.List("", pattern, nil).Collect()
		if err != nil {
			return nil, failure(op, err)
		}
		for _, data := range listed {
			name := normalizeFolder(data.Mailbox)
			// A folder this provider could not open again is never listed: its name must be a literal one,
			// and the connection's scope must admit it.
			if !validFolderName(name) || !c.scope.allowsFolder(name) {
				continue
			}
			folder := Folder{Name: name}
			if data.Delim != 0 {
				folder.Delimiter = clean(string(data.Delim), 1)
			}
			for i, attr := range data.Attrs {
				if i >= maxAttributes {
					break
				}
				folder.Attributes = append(folder.Attributes, clean(string(attr), maxAttributeText))
			}
			byName[name] = folder
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	page := &FoldersPage{Folders: make([]Folder, 0, len(names))}
	for _, name := range names {
		if len(page.Folders) >= maxFolders {
			page.Truncated = true
			break
		}
		page.Folders = append(page.Folders, byName[name])
	}
	page.Count = len(page.Folders)
	return page, nil
}
