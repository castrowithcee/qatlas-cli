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
	{ID: "messages", Title: "Messages", Description: "Channel, direct, and group messages and threads: opening " +
		"direct and group channels, reading, sending, editing, pinning, and deleting"},
	{ID: "reactions", Title: "Reactions", Description: "Reactions on messages: reading, adding, and removing the own"},
	{ID: "files", Title: "Files", Description: "Message attachments: listing, metadata, upload from a released local file, and download to a released local directory"},
	{ID: "users", Title: "Users", Description: "Users of the bound teams and their presence"},
}

// groupOf returns the group of a kChat tool ID (infomaniakchat.<segment>.<action>), or "" for an ID outside
// every group.
func groupOf(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 3 {
		return ""
	}
	switch parts[1] {
	case "teams", "channels", "messages", "reactions", "files", "users":
		return parts[1]
	case "channelmembers", "archivedchannels":
		return "channels"
	case "teammembers":
		return "teams"
	case "direct", "groupmessages", "pins":
		return "messages"
	}
	return ""
}

func withGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = groupOf(d.ID)
	return d
}
