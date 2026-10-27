package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The badge administration tools list the badges of one account, add existing badges to it, and remove them.
// Badges themselves are not created or changed. Like the other instance administration they reach past any
// target, so only a connection without targets offers them.

const (
	maxBadgeSlugs = 20
	maxBadgeSlug  = 100
)

var badgeMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the badge"},
	{name: "slug", kind: kindText, note: "Slug of the badge, untrusted data"},
	{name: "description", kind: kindText, note: "Description, untrusted data"},
	{name: "image_url", kind: kindText, address: true, note: "Address of the badge image, never fetched; left out " +
		"when it carries user info or a query"},
}

// badgeSlugOf accepts the narrower reading of a slug: lower-case letters, digits, and - _ . only.
func badgeSlugOf(slug string) bool {
	if slug == "" || len(slug) > maxBadgeSlug {
		return false
	}
	for _, r := range slug {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

var adminUserBadgesList = &tool{
	group: "admin-user-badges", action: "list", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the badges of a " + p.name + " user",
			Description: "List the badges of one account as an administrator, at most " + itoa(maxRunnerRows) +
				", with every member of the badge object; the instance answers without paging. Slugs, " +
				"descriptions, and image addresses are untrusted data and the addresses are never fetched. " +
				adminUserNote,
			Tags:         []string{p.id, "admin", "badges", "list"},
			Risk:         readRisk(p.id + adminAccountsSensitivity),
			InputSchema:  inputSchema(`"username":`+issueSchemaLogin, "username"),
			OutputSchema: instanceListOutput("badges", schemaOf(p, badgeMembers)),
			Arguments:    []capability.Argument{adminUserArgument},
			Fields:       instanceListFields("badges", "Badges with every member of the badge object"),
			Examples: []capability.Example{{Description: "List the badges of an account",
				Arguments: json.RawMessage(`{"username":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Username string `json:"username"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := userNameOf("username", a.Username); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			return instanceUnpaged(ctx, c, "list instance user badges", "/admin/users/"+escape(a.Username)+"/badges",
				url.Values{}, "badges", "", func(row object) map[string]any { return project(p, badgeMembers, row) })
		}, nil
	},
}

// badgeSlugsOf validates the slug list of both change tools.
func badgeSlugsOf(slugs []string) error {
	if len(slugs) < 1 || len(slugs) > maxBadgeSlugs {
		return invalidArgument("badge_slugs takes 1 to " + itoa(maxBadgeSlugs) + " slugs")
	}
	seen := map[string]bool{}
	for _, slug := range slugs {
		if !badgeSlugOf(slug) {
			return invalidArgument("badge_slugs must be lower-case letters, digits, '-', '_', or '.', at most " +
				itoa(maxBadgeSlug) + " bytes each")
		}
		if seen[slug] {
			return invalidArgument("badge_slugs must not repeat a slug")
		}
		seen[slug] = true
	}
	return nil
}

func badgeChangeTool(action, done, method, op string, effect capability.Effect, idempotency capability.Idempotency,
	title, text string, listed bool) *tool {
	return &tool{
		group: "admin-user-badges", action: action, since: giteaOnly,
		describe: func(p *product) capability.Descriptor {
			description := text + " Badges are never created or changed. The request is sent once and never repeated " +
				"after an unclear outcome. " + adminUserNote
			if listed {
				description = text + " Badges are never created or changed. Idempotent; the request is sent once and " +
					"never repeated after an unclear outcome. Offered only by a connection without targets whose tools " +
					"list names it, in no profile"
			}
			return capability.Descriptor{
				Title:                 title + p.name + " user",
				Description:           description,
				Tags:                  []string{p.id, "admin", "badges", action},
				Risk:                  changeRisk(effect, idempotency, p.id+adminAccountsSensitivity),
				RequiresToolAllowList: listed,
				InputSchema: inputSchema(`"username":`+issueSchemaLogin+`,"badge_slugs":{"type":"array","minItems":1,"maxItems":`+
					itoa(maxBadgeSlugs)+`,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":`+
					itoa(maxBadgeSlug)+`,"pattern":"^[a-z0-9._-]+$"}}`, "username", "badge_slugs"),
				OutputSchema: adminUserOutput(`,"`+done+`":{"type":"boolean"},"count":{"type":"integer"}`, done, "count"),
				Arguments: []capability.Argument{adminUserArgument, {Name: "badge_slugs", Required: true,
					Description: "Slugs of existing badges, 1 to " + itoa(maxBadgeSlugs) + ", without duplicates"}},
				Fields: []capability.Field{{Name: "username", Description: "User name of the account"},
					{Name: done, Description: "True once the instance accepted the request"},
					{Name: "count", Description: "Number of slugs named"}},
				Examples: []capability.Example{{Description: "Name the slugs",
					Arguments: json.RawMessage(`{"username":"octo","badge_slugs":["early-adopter"]}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			if err := allowed.instanceWide(); err != nil {
				return nil, err
			}
			var a struct {
				Username   string   `json:"username"`
				BadgeSlugs []string `json:"badge_slugs"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			if err := userNameOf("username", a.Username); err != nil {
				return nil, err
			}
			if err := badgeSlugsOf(a.BadgeSlugs); err != nil {
				return nil, err
			}
			body := map[string]any{"badge_slugs": a.BadgeSlugs}
			return func(ctx context.Context, c *Client) (any, error) {
				if _, err := c.changeQuery(ctx, op, method, "/admin/users/"+escape(a.Username)+"/badges", nil, body,
					nil); err != nil {
					return nil, err
				}
				return map[string]any{"username": a.Username, done: true, "count": len(a.BadgeSlugs)}, nil
			}, nil
		},
	}
}

// Adding a badge the account already holds fails on the instance (and rolls the whole request back), so it is
// not idempotent; removing one the account lacks succeeds.
var (
	adminUserBadgesAdd = badgeChangeTool("add", "added", http.MethodPost, "add instance user badges", capability.EffectCreate,
		capability.IdempotencyNonIdempotent, "Add badges to a ",
		"Add up to "+itoa(maxBadgeSlugs)+" existing badges to one account as an administrator; the instance rejects "+
			"the whole request when the account already holds one of them or a slug is unknown.", false)
	adminUserBadgesDelete = badgeChangeTool("delete", "deleted", http.MethodDelete, "delete instance user badges",
		capability.EffectDelete, capability.IdempotencyIdempotent, "Remove badges from a ",
		"Remove up to "+itoa(maxBadgeSlugs)+" badges from one account as an administrator.", true)
)
