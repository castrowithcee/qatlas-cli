package infomaniakchat

import (
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// toolGroups are the subject areas the kChat tools are sorted into for display.
var toolGroups = []config.ToolGroup{
	{ID: "teams", Title: "Teams", Description: "The bound teams of the token"},
	{ID: "channels", Title: "Channels", Description: "The channels of a bound team"},
	{ID: "messages", Title: "Messages", Description: "Channel messages and threads: reading, sending, editing, and deleting"},
}

// groupOf returns the group of a kChat tool ID (infomaniakchat.<segment>.<action>), or "" for an ID outside
// every group.
func groupOf(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 3 {
		return ""
	}
	switch parts[1] {
	case "teams", "channels", "messages":
		return parts[1]
	}
	return ""
}

func withGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = groupOf(d.ID)
	return d
}
