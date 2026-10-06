package bookstack

import (
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// contentGroup is the one group every BookStack tool belongs to.
const contentGroup = "content"

// toolGroups are the subject areas BookStack's tools are sorted into for display.
var toolGroups = []config.ToolGroup{
	{ID: contentGroup, Title: "Content", Description: "Pages, search, books, and chapters of the knowledge base"},
}

func withGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = contentGroup
	return d
}
