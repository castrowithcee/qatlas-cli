package bookstack

import (
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	// contentGroup is the group of the tools that read and change content.
	contentGroup = "content"
	// administrationGroup is the group of the instance information tools.
	administrationGroup = "administration"
)

// toolGroups are the subject areas BookStack's tools are sorted into for display.
var toolGroups = []config.ToolGroup{
	{ID: contentGroup, Title: "Content", Description: "Pages, search, books, chapters, and shelves of the knowledge base"},
	{ID: administrationGroup, Title: "Administration", Description: "Information about the BookStack instance"},
}

func withGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = contentGroup
	return d
}

func withAdministrationGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = administrationGroup
	return d
}
