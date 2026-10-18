package infomaniakchat

import (
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// toolGroups are the subject areas the kChat tools are sorted into for display.
var toolGroups = []config.ToolGroup{
	{ID: "teams", Title: "Teams", Description: "The bound teams of the token, their members, and their activity rankings"},
	{ID: "channels", Title: "Channels", Description: "The channels of a bound team, their members, and the own sidebar categories and notifications"},
	{ID: "messages", Title: "Messages", Description: "Channel, direct, and group messages and threads: opening " +
		"direct and group channels, reading, sending, editing, pinning, flagging, and deleting"},
	{ID: "threads", Title: "Threads", Description: "Followed threads of a bound team: reading, following, and unfollowing"},
	{ID: "reactions", Title: "Reactions", Description: "Reactions on messages: reading, adding, and removing the own"},
	{ID: "files", Title: "Files", Description: "Message attachments: listing, metadata, upload from a released local file, and download to a released local directory"},
	{ID: "users", Title: "Users", Description: "Users and user groups of the bound teams, their presence, and the own status and profile"},
	{ID: "integrations", Title: "Integrations", Description: "Incoming and outgoing webhooks of bound teams and channels and custom slash commands of " +
		"bound teams: listing, reading, describing, and deleting; webhook IDs are secrets"},
	{ID: "administration", Title: "Administration", Description: "Instance-wide bot accounts (listing, reading, and " +
		"changing), the custom emoji catalog (listing, creating from a released local image, and deleting), and role definitions by name; only on a connection that binds every team of its token and has no channel target"},
}

// groupOf returns the group of a kChat tool ID (infomaniak.chat.<object>.<action>), or "" for an ID outside
// every group.
func groupOf(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) != 4 {
		return ""
	}
	switch parts[2] {
	case "teams", "channels", "messages", "reactions", "files", "users", "threads":
		return parts[2]
	case "status", "custom-status", "profile", "user-groups":
		return "users"
	case "channel-members", "archived-channels", "categories", "channel-notifications":
		return "channels"
	case "team-members", "insights":
		return "teams"
	case "direct", "group-messages", "pins", "flagged":
		return "messages"
	case "incoming-webhooks", "outgoing-webhooks", "commands":
		return "integrations"
	case "bots", "emoji", "roles":
		return "administration"
	}
	return ""
}

func withGroup(d capability.Descriptor) capability.Descriptor {
	d.Group = groupOf(d.ID)
	return d
}
