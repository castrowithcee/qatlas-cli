package github

import (
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The content width: the members of GitHub's content objects (files, directory entries, symlinks, and
// submodules) and of its Git trees beyond the compact views of the contents, tree, and workflow file tools.
// download_url is never read: for a private repository GitHub answers it with a temporary token that grants
// access without sign-in. The _links member is left out as well, since its addresses repeat the *_url members
// that links holds.

var (
	contentLinkKeys      = []string{"url", "git_url", "submodule_git_url"}
	contentEntryLinkKeys = []string{"url", "git_url"}
	treeLinkKeys         = []string{"url"}
)

// contentAddress holds the web address and the REST addresses of one content object.
type contentAddress struct {
	URL   *string
	Links Links
}

// readContentAddress reads the addresses of one content object; an unreadable or unusable one stays out.
func readContentAddress(data []byte, keys []string) contentAddress {
	var web struct {
		HTMLURL *string `json:"html_url"`
	}
	_ = json.Unmarshal(data, &web)
	links, _ := readLinks(data, keys)
	return contentAddress{URL: address(web.HTMLURL), Links: links}
}

// withAddress decodes one content object into T and reads its addresses from the same object.
type withAddress[T any] struct {
	Value   T
	Address contentAddress
}

func (w *withAddress[T]) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &w.Value); err != nil {
		return err
	}
	w.Address = readContentAddress(data, contentEntryLinkKeys)
	return nil
}

// Schemas and fields of the content width.

var contentEntryProperties = `"name":{"type":"string"},"path":{"type":"string"},"type":{"type":"string"},` +
	`"size":{"type":"integer"},"sha":{"type":"string"},"url":{"type":"string"},"links":` +
	linksSchema(contentEntryLinkKeys)

var contentWidthProperties = `"name":{"type":"string"},"encoding":{"type":"string"},"url":{"type":"string"},` +
	`"links":` + linksSchema(contentLinkKeys)

var contentWidthFields = []capability.Field{
	{Name: "name", Description: "Name of the file, symlink, or submodule, untrusted data"},
	{Name: "encoding", Description: "Encoding GitHub reports for the content of a file, such as base64"},
	{Name: "url", Description: "Web address of the file, symlink, or submodule, untrusted data, never fetched"},
	{Name: "links", Description: "REST url, git_url, and for a submodule submodule_git_url by their GitHub " +
		"names, untrusted data, never fetched"},
}

var treeWidthProperties = `"links":` + linksSchema(treeLinkKeys)

var treeEntryProperties = `"path":{"type":"string"},"type":{"type":"string"},"sha":{"type":"string"},` +
	`"size":{"type":"integer"},"mode":{"type":"string"},"links":` + linksSchema(treeLinkKeys)

var treeWidthFields = []capability.Field{
	{Name: "links", Description: "REST url of the tree by its GitHub name, untrusted data, never fetched"},
}
