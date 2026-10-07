package bookstack

import (
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	// contentGroup is the group of the tools that read and change content.
	contentGroup = "content"
	// commentsGroup is the group of the page comment tools.
	commentsGroup = "comments"
	// filesGroup is the group of the tools that read, download, and change attachments and images.
	filesGroup = "files"
	// administrationGroup is the group of the instance information and content permission tools.
	administrationGroup = "administration"
)

// toolGroups are the subject areas BookStack's tools are sorted into for display.
var toolGroups = []config.ToolGroup{
	{ID: contentGroup, Title: "Content", Description: "Pages, search, books, chapters, and shelves of the knowledge base"},
	{ID: commentsGroup, Title: "Comments", Description: "Comments and replies on pages"},
	{ID: filesGroup, Title: "Files", Description: "Attachments and images of pages"},
	{ID: administrationGroup, Title: "Administration", Description: "Information about the BookStack instance and permissions of its content"},
}

func withGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = contentGroup
	return d
}

func withAdministrationGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = administrationGroup
	return d
}

func withCommentsGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = commentsGroup
	return d
}

func withFilesGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = filesGroup
	return d
}
