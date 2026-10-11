package infomaniakchat

import (
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// toolGroups are the subject areas the kChat tools are sorted into for display.
var toolGroups = []config.ToolGroup{
	{ID: "teams", Title: "Teams", Description: "The bound teams of the token"},
	{ID: "channels", Title: "Channels", Description: "The channels of a bound team, their members, and the own sidebar categories and notifications"},
	{ID: "messages", Title: "Messages", Description: "Channel, direct, and group messages and threads: opening " +
		"direct and group channels, reading, sending, editing, pinning, flagging, and deleting"},
	{ID: "threads", Title: "Threads", Description: "Followed threads of a bound team: reading, following, and unfollowing"},
	{ID: "reactions", Title: "Reactions", Description: "Reactions on messages: reading, adding, and removing the own"},
	{ID: "files", Title: "Files", Description: "Message attachments: listing, metadata, upload from a released local file, and download to a released local directory"},
	{ID: "users", Title: "Users", Description: "Users of the bound teams, their presence, and the own status and profile"},
	{ID: "integrations", Title: "Integrations", Description: "Incoming and outgoing webhooks of bound teams and channels and custom slash commands of " +
		"bound teams: listing, reading, describing, and deleting; webhook IDs are secrets"},
	{ID: "administration", Title: "Administration", Description: "Instance-wide bot accounts (listing, reading, and " +
		"changing) and the custom emoji catalog (listing); only on a connection that binds every team of its token and has no channel target"},
}

// groupOf returns the group of a kChat tool ID (infomaniakchat.<segment>.<action>), or "" for an ID outside
// every group.
func groupOf(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 3 {
		return ""
	}
	switch parts[1] {
	case "teams", "channels", "messages", "reactions", "files", "users", "threads":
		return parts[1]
	case "status", "customstatus", "profile":
		return "users"
	case "channelmembers", "archivedchannels", "categories", "channelnotifications":
		return "channels"
	case "teammembers":
		return "teams"
	case "direct", "groupmessages", "pins", "flagged":
		return "messages"
	case "incomingwebhooks", "outgoingwebhooks", "commands":
		return "integrations"
	case "bots", "emoji":
		return "administration"
	}
	return ""
}

func withGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = groupOf(d.ID)
	return d
}
