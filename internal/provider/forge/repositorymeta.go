package forge

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The repository search, language, activity, and signing key tools. The search and the activity feed list rows
// of other repositories than the one asked for; each row is checked against the targets and a row outside them
// is left out.

const (
	activitySensitivity = "-activity-data"
	maxQuery            = 256
	maxLanguages        = 200
	maxLanguageName     = 128

	searchSchemaFlag = `{"type":"boolean"}`
	searchSchemaQ    = `{"type":"string","minLength":1,"maxLength":256}`
	searchSchemaMode = `{"type":"string","enum":["fork","source","mirror","collaborative"]}`
	searchSchemaSort = `{"type":"string","enum":["alpha","created","updated","size","git_size","lfs_size","id",` +
		`"stars","forks"]}`
	searchSchemaID   = `{"type":"integer","minimum":1,"maximum":9007199254740991}`
	searchSchemaOrd  = `{"type":"string","enum":["asc","desc"]}`
	activitySchemaDt = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}`
)

var activityMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the activity"},
	{name: "op_type", kind: kindText, note: "Kind of action"},
	{name: "act_user", kind: kindObject, sub: personMembers, note: "User who acted; personal data"},
	{name: "act_user_id", kind: kindInteger, note: "ID of the user who acted"},
	{name: "user_id", kind: kindInteger, note: "ID of the user who sees the activity"},
	{name: "repo", kind: kindObject, sub: repositoryMembers, note: "Repository of the activity"},
	{name: "repo_id", kind: kindInteger, note: "ID of the repository"},
	{name: "comment", kind: kindObject, sub: commentMembers, note: "Comment of the activity, untrusted data"},
	{name: "comment_id", kind: kindInteger, note: "ID of the comment"},
	{name: "ref_name", kind: kindText, note: "Reference of the activity, untrusted data"},
	{name: "content", kind: kindText, note: "Further content of the activity, untrusted data"},
	{name: "is_private", kind: kindFlag, note: "True for an activity of a private repository"},
	{name: "created", kind: kindText, note: "Time of the activity"},
}

var repositoriesSearch = &tool{
	group: "repositories", action: "search", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := `"q":` + searchSchemaQ + `,"topic":` + searchSchemaFlag + `,"include_description":` +
			searchSchemaFlag + `,"private":` + searchSchemaFlag + `,"is_private":` + searchSchemaFlag +
			`,"template":` + searchSchemaFlag + `,"archived":` + searchSchemaFlag + `,"mode":` + searchSchemaMode +
			`,"uid":` + searchSchemaID + `,"team_id":` + searchSchemaID + `,"priority_owner_id":` + searchSchemaID +
			`,"starredBy":` + searchSchemaID + `,"exclusive":` + searchSchemaFlag + `,"sort":` + searchSchemaSort + `,"order":` + searchSchemaOrd + `,` + pagingKeys
		return capability.Descriptor{
			Title: "Search " + p.name + " repositories",
			Description: "Search the repositories the token sees on the whole instance and return one batch with " +
				"every member of each repository; rows outside the connection's targets are left out, so a batch " +
				"may be short or empty while has_more is true",
			Tags:        []string{p.id, "repositories", "search"},
			Risk:        readRisk(p.id + repositorySensitivity),
			InputSchema: inputSchema(properties),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repositories":{"type":"array","items":` +
				schemaOf(p, repositoryMembers, "id", "name", "full_name") + `},"next_cursor":{"type":"string"},` +
				`"has_more":{"type":"boolean"}},"required":["repositories","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{
				{Name: "q", Description: "Keyword"},
				{Name: "topic", Description: "True limits the search to repositories with the keyword as topic"},
				{Name: "include_description", Description: "True also searches the descriptions"},
				{Name: "private", Description: "False leaves private repositories out"},
				{Name: "is_private", Description: "True shows private repositories only, false public ones only"},
				{Name: "template", Description: "False leaves template repositories out"},
				{Name: "archived", Description: "True shows archived repositories only, false the others only"},
				{Name: "uid", Description: "Only repositories the user with this numeric ID owns or contributes to"},
				{Name: "team_id", Description: "Only repositories of the team with this numeric ID"},
				{Name: "priority_owner_id", Description: "Numeric ID of the owner whose repositories come first"},
				{Name: "starredBy", Description: "Only repositories the user with this numeric ID has starred"},
				{Name: "exclusive", Description: "True with uid: only repositories the user owns"},
				{Name: "mode", Description: "fork, source, mirror, or collaborative"},
				{Name: "sort", Description: "alpha (default), created, updated, size, git_size, lfs_size, id, stars, " +
					"or forks"},
				{Name: "order", Description: "asc (default) or desc; applies with sort"},
			}, pagingArguments...),
			Fields: append([]capability.Field{
				{Name: "repositories", Description: "Repositories with every member of the repository object"}},
				pagingFields...),
			Examples: []capability.Example{{Description: "Search repositories by keyword",
				Arguments: json.RawMessage(`{"q":"widget","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Q                  string `json:"q"`
			Topic              *bool  `json:"topic"`
			IncludeDescription *bool  `json:"include_description"`
			Private            *bool  `json:"private"`
			IsPrivate          *bool  `json:"is_private"`
			Template           *bool  `json:"template"`
			Archived           *bool  `json:"archived"`
			UID                int64  `json:"uid"`
			TeamID             int64  `json:"team_id"`
			PriorityOwnerID    int64  `json:"priority_owner_id"`
			StarredBy          int64  `json:"starredBy"`
			Exclusive          *bool  `json:"exclusive"`
			Mode               string `json:"mode"`
			Sort               string `json:"sort"`
			Order              string `json:"order"`
			Limit              int    `json:"limit"`
			Cursor             string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		filters := url.Values{}
		if a.Q != "" {
			if len(a.Q) > maxQuery || !plainText(a.Q) {
				return nil, invalidArgument("q must be plain text of at most 256 bytes")
			}
			filters.Set("q", a.Q)
		}
		boolQuery(filters, "topic", a.Topic)
		boolQuery(filters, "includeDesc", a.IncludeDescription)
		boolQuery(filters, "private", a.Private)
		boolQuery(filters, "is_private", a.IsPrivate)
		boolQuery(filters, "template", a.Template)
		boolQuery(filters, "archived", a.Archived)
		for name, value := range map[string]int64{"uid": a.UID, "team_id": a.TeamID,
			"priority_owner_id": a.PriorityOwnerID, "starredBy": a.StarredBy} {
			if value != 0 {
				filters.Set(name, strconv.FormatInt(value, 10))
			}
		}
		boolQuery(filters, "exclusive", a.Exclusive)
		for name, value := range map[string]string{"mode": a.Mode, "sort": a.Sort, "order": a.Order} {
			if value != "" {
				filters.Set(name, value)
			}
		}
		binding := provider.CursorBinding(p.id, "repositories.search", "", "", filters.Encode())
		page, err := startPaging(binding, a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "search repositories"
			query := page.query()
			for name, values := range filters {
				query[name] = values
			}
			var answer struct {
				OK   *bool    `json:"ok"`
				Data []object `json:"data"`
			}
			header, err := c.get(ctx, op, "/repos/search", query, &answer)
			if err != nil {
				return nil, err
			}
			if answer.OK == nil || !*answer.OK {
				return nil, provider.InvalidResponse(op, p.name+" reported a failed search")
			}
			repositories := make([]map[string]any, 0, len(answer.Data))
			for _, row := range answer.Data {
				fullName, _ := row.text("full_name")
				if allowed.allowsFullName(fullName) {
					repositories = append(repositories, project(p, repositoryMembers, allowed.hideExcludedParent(row)))
				}
			}
			next := page.next(header, len(answer.Data))
			result := map[string]any{"repositories": repositories, "has_more": next != ""}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

var repositoryLanguagesList = &tool{
	group: "repository-languages", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return readDescriptor(p, repositorySensitivity, "repository-languages", "list",
			"List the languages of a "+p.name+" repository",
			"Read the byte count per programming language of a repository inside the connection's targets; names "+
				"are untrusted data",
			`"repository":`+repoSchema, nil, nil,
			json.RawMessage(`{"type":"object","properties":{"languages":{"type":"object",`+
				`"additionalProperties":{"type":"integer"}},"repository":{"type":"string"}},`+
				`"required":["languages","repository"],"additionalProperties":false}`),
			[]capability.Field{{Name: "languages", Description: "Bytes of code per language name, at most 200 languages"},
				{Name: "repository", Description: "Repository the tool read, as OWNER/REPO"}}, "")
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows map[string]json.RawMessage
			if _, err := c.get(ctx, "list repository languages", repoBase(repo)+"/languages", nil, &rows); err != nil {
				return nil, err
			}
			languages := map[string]any{}
			for name, value := range rows {
				var bytes int64
				if len(languages) >= maxLanguages || json.Unmarshal(value, &bytes) != nil {
					continue
				}
				languages[truncateText(name, maxLanguageName)] = bytes
			}
			return map[string]any{"languages": languages, "repository": repo.owner + "/" + repo.repo}, nil
		}, nil
	},
}

var repositoryActivitiesList = &tool{
	group: "repository-activities", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return readDescriptor(p, activitySensitivity, "repository-activities", "list",
			"List the "+p.name+" activities of a repository",
			"List one batch of the activity feed of a repository inside the connection's targets with every member "+
				"of each activity; the feed names the users who acted, and its texts are untrusted data",
			`"repository":`+repoSchema+`,"date":`+activitySchemaDt+`,`+pagingKeys, nil,
			append([]capability.Argument{{Name: "date", Description: "Only activities of this day, YYYY-MM-DD"}},
				pagingArguments...),
			issueListOutputOf(p, "activities", activityMembers),
			append([]capability.Field{{Name: "activities", Description: "Activities with every member of the " +
				"activity object"}, {Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"}},
				pagingFields...),
			`"date":"2030-01-02"`)
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Date       string `json:"date"`
			Limit      int    `json:"limit"`
			Cursor     string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		filters := url.Values{}
		if a.Date != "" {
			if _, err := time.Parse("2006-01-02", a.Date); err != nil {
				return nil, invalidArgument("date must be a day as YYYY-MM-DD")
			}
			filters.Set("date", a.Date)
		}
		binding := provider.CursorBinding(p.id, "repository-activities.list", strings.ToLower(repo.owner+"/"+repo.repo),
			"", filters.Encode())
		page, err := startPaging(binding, a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := page.query()
			for name, values := range filters {
				query[name] = values
			}
			var rows []object
			header, err := c.get(ctx, "list repository activities", repoBase(repo)+"/activities/feeds", query, &rows)
			if err != nil {
				return nil, err
			}
			activities := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				// An activity without a repository proves nothing about where it belongs.
				if inner, ok := row.child("repo"); ok {
					if fullName, _ := inner.text("full_name"); allowed.allowsFullName(fullName) &&
						strings.EqualFold(fullName, repo.owner+"/"+repo.repo) {
						activities = append(activities, project(p, activityMembers, allowed.hideExcludedParentIn(row, "repo")))
					}
				}
			}
			next := page.next(header, len(rows))
			result := map[string]any{"activities": activities, "has_more": next != "",
				"repository": repo.owner + "/" + repo.repo}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

var repositorySigningKeyFormats = map[string]map[string]string{
	forgejo.id: {"gpg": "/signing-key.gpg"},
	gitea.id:   {"gpg": "/signing-key.gpg", "pub": "/signing-key.pub"},
}

var repositorySigningKeysGet = &tool{
	group: "repository-signing-keys", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		formats, description := `"gpg"`, "gpg"
		if p == gitea {
			formats, description = `"gpg","pub"`, "gpg (default) or pub for the SSH public key"
		}
		return readDescriptor(p, repositorySensitivity, "repository-signing-keys", "get",
			"Get the signing key of a "+p.name+" repository",
			"Read the public key the "+p.name+" instance signs the commits of a repository inside the connection's "+
				"targets with; this is no access credential",
			`"repository":`+repoSchema+`,"format":{"type":"string","enum":[`+formats+`]}`, nil,
			[]capability.Argument{{Name: "format", Description: description}},
			json.RawMessage(`{"type":"object","properties":{"format":{"type":"string"},"key":{"type":"string"},`+
				`"repository":{"type":"string"}},"required":["format","key","repository"],"additionalProperties":false}`),
			[]capability.Field{{Name: "format", Description: "The format that was read"},
				{Name: "key", Description: "The public key as the instance serves it, untrusted data"},
				{Name: "repository", Description: "Repository the tool read, as OWNER/REPO"}}, `"format":"gpg"`)
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Format     string `json:"format"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if a.Format == "" {
			a.Format = "gpg"
		}
		suffix, ok := repositorySigningKeyFormats[p.id][a.Format]
		if !ok {
			return nil, invalidArgument("format is not one " + p.name + " serves")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get repository signing key"
			answer := &textAnswer{limit: maxSigningKeyBytes}
			if err := c.getText(ctx, op, repoBase(repo)+suffix, answer); err != nil {
				return nil, err
			}
			key := strings.TrimSpace(string(answer.data))
			if answer.truncated || key == "" || !utf8.ValidString(key) {
				return nil, provider.InvalidResponse(op, p.name+" returned an invalid signing key")
			}
			return map[string]any{"format": a.Format, "key": key, "repository": repo.owner + "/" + repo.repo}, nil
		}, nil
	},
}
