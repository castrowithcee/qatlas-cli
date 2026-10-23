package github

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The project width of the owner list: the members of GitHub's ProjectV2 beyond the compact view the list
// answered before. The readme is cut like every long text. The repositories a project is linked to are read
// with their first page; the other connections are read with the tool for that object, so the list reports
// their counts, which add no nodes to GitHub's node count. Fields, views, and the owner of the project are
// lookups or back references of the call and are not repeated.

const (
	// maxProjectQuery bounds the search text of the project list.
	maxProjectQuery = 256
	// projectRepositoriesFirst is the first page of the linked repositories of each project. A page of 100
	// projects then asks for 100 + 100 * (1 + 10) nodes, far below GitHub's limit of 500000 nodes per query.
	projectRepositoriesFirst = "10"
)

// projectWidthSelection is the project node of the list beyond the compact view.
const projectWidthSelection = `id fullDatabaseId createdAt updatedAt closedAt creator{` + gqlAccountSelection + `} ` +
	`public readme shortDescription template viewerCanClose viewerCanReopen viewerCanUpdate resourcePath ` +
	`repositories(first:` + projectRepositoriesFirst + `){totalCount nodes{nameWithOwner url}} ` +
	`items{totalCount} fields{totalCount} views{totalCount} workflows{totalCount} statusUpdates{totalCount}`

// projectTeamsSelection counts the teams of a project, which GitHub shows only to a token that may read the
// organizations; the list asks for it only when team_counts says so.
const projectTeamsSelection = ` teams{totalCount}`

type projectWidthRaw struct {
	ID               *string                           `json:"id"`
	FullDatabaseID   *gqlBigInt                        `json:"fullDatabaseId"`
	CreatedAt        *string                           `json:"createdAt"`
	UpdatedAt        *string                           `json:"updatedAt"`
	ClosedAt         *string                           `json:"closedAt"`
	Creator          *gqlAccountRaw                    `json:"creator"`
	Public           *bool                             `json:"public"`
	Readme           *string                           `json:"readme"`
	ShortDescription *string                           `json:"shortDescription"`
	Template         *bool                             `json:"template"`
	ViewerCanClose   *bool                             `json:"viewerCanClose"`
	ViewerCanReopen  *bool                             `json:"viewerCanReopen"`
	ViewerCanUpdate  *bool                             `json:"viewerCanUpdate"`
	ResourcePath     *string                           `json:"resourcePath"`
	Repositories     *gqlNodes[gqlNestedRepositoryRaw] `json:"repositories"`
	Items            *gqlTotal                         `json:"items"`
	Fields           *gqlTotal                         `json:"fields"`
	Views            *gqlTotal                         `json:"views"`
	Workflows        *gqlTotal                         `json:"workflows"`
	StatusUpdates    *gqlTotal                         `json:"statusUpdates"`
	Teams            *gqlTotal                         `json:"teams"`
}

// ProjectRepository is a repository a project is linked to. Outside the targets of the connection it shows
// only its name.
type ProjectRepository struct {
	NameWithOwner *string `json:"name_with_owner,omitempty"`
	URL           *string `json:"url,omitempty"`
}

// ProjectWidth is the project beyond the compact view of the list.
type ProjectWidth struct {
	ID                      *string             `json:"id,omitempty"`
	FullDatabaseID          *string             `json:"full_database_id,omitempty"`
	CreatedAt               *string             `json:"created_at,omitempty"`
	UpdatedAt               *string             `json:"updated_at,omitempty"`
	ClosedAt                *string             `json:"closed_at,omitempty"`
	Creator                 *GraphQLAccount     `json:"creator,omitempty"`
	Public                  *bool               `json:"public,omitempty"`
	ShortDescription        *string             `json:"short_description,omitempty"`
	Readme                  *string             `json:"readme,omitempty"`
	ReadmeTruncated         bool                `json:"readme_truncated,omitempty"`
	Template                *bool               `json:"template,omitempty"`
	ViewerCanClose          *bool               `json:"viewer_can_close,omitempty"`
	ViewerCanReopen         *bool               `json:"viewer_can_reopen,omitempty"`
	ViewerCanUpdate         *bool               `json:"viewer_can_update,omitempty"`
	Links                   Links               `json:"links,omitempty"`
	Repositories            []ProjectRepository `json:"repositories,omitempty"`
	RepositoriesTruncated   bool                `json:"repositories_truncated,omitempty"`
	RepositoriesTotalCount  *int64              `json:"repositories_total_count,omitempty"`
	ItemsTotalCount         *int64              `json:"items_total_count,omitempty"`
	FieldsTotalCount        *int64              `json:"fields_total_count,omitempty"`
	ViewsTotalCount         *int64              `json:"views_total_count,omitempty"`
	WorkflowsTotalCount     *int64              `json:"workflows_total_count,omitempty"`
	StatusUpdatesTotalCount *int64              `json:"status_updates_total_count,omitempty"`
	TeamsTotalCount         *int64              `json:"teams_total_count,omitempty"`
	// Unreadable names the members GitHub refused this token inside this project.
	Unreadable []string `json:"unreadable,omitempty"`
}

func (r *projectWidthRaw) width(allowed allowlist) ProjectWidth {
	readme, cut := gqlBody(r.Readme)
	w := ProjectWidth{ID: gqlName(r.ID), FullDatabaseID: r.FullDatabaseID.text(), CreatedAt: gqlName(r.CreatedAt),
		UpdatedAt: gqlName(r.UpdatedAt), ClosedAt: gqlName(r.ClosedAt), Creator: r.Creator.project(),
		Public: r.Public, ShortDescription: gqlText(r.ShortDescription), Readme: readme, ReadmeTruncated: cut,
		Template: r.Template, ViewerCanClose: r.ViewerCanClose, ViewerCanReopen: r.ViewerCanReopen,
		ViewerCanUpdate: r.ViewerCanUpdate, Links: gqlLinks(gqlResourceLinkKeys, r.ResourcePath),
		ItemsTotalCount: r.Items.count(), FieldsTotalCount: r.Fields.count(), ViewsTotalCount: r.Views.count(),
		WorkflowsTotalCount: r.Workflows.count(), StatusUpdatesTotalCount: r.StatusUpdates.count(),
		TeamsTotalCount: r.Teams.count()}
	w.Repositories, w.RepositoriesTotalCount, w.RepositoriesTruncated = pageItems(r.Repositories,
		func(repository *gqlNestedRepositoryRaw) *ProjectRepository {
			if repository == nil || gqlName(repository.NameWithOwner) == nil {
				return nil
			}
			linked := &ProjectRepository{NameWithOwner: gqlName(repository.NameWithOwner)}
			if repository.admitted(allowed) {
				linked.URL = address(repository.URL)
			}
			return linked
		})
	return w
}

// ---- Filters

// ProjectFilters are the optional structured filters and the order of the project list. Unset, the list is
// in number order, ascending, over the projects the token can read.
type ProjectFilters struct {
	Query              string `json:"query"`
	OrderBy            string `json:"order_by"`
	Direction          string `json:"direction"`
	MinPermissionLevel string `json:"min_permission_level"`
}

var (
	projectOrderFields     = []string{"CREATED_AT", "NUMBER", "TITLE", "UPDATED_AT"}
	projectDirections      = []string{"ASC", "DESC"}
	projectPermissionLevel = []string{"READ", "WRITE", "ADMIN"}
)

// check validates the filters before a credential is resolved. The query travels as a variable, so only its
// length and its characters are bound.
func (f *ProjectFilters) check() error {
	if utf8.RuneCountInString(f.Query) > maxProjectQuery || !utf8.ValidString(f.Query) ||
		strings.IndexFunc(f.Query, unicode.IsControl) >= 0 {
		return invalidRequest("query must be at most 256 characters without control characters")
	}
	for _, value := range []struct {
		name, value string
		allowed     []string
	}{{"order_by", f.OrderBy, projectOrderFields}, {"direction", f.Direction, projectDirections},
		{"min_permission_level", f.MinPermissionLevel, projectPermissionLevel}} {
		if value.value != "" && !containsExact(value.allowed, value.value) {
			return invalidRequest(value.name + " must be one of " + strings.Join(value.allowed, ", "))
		}
	}
	return nil
}

// order is the order GitHub applies: the number, ascending, unless the caller names another.
func (f *ProjectFilters) order() map[string]string {
	order := map[string]string{"field": "NUMBER", "direction": "ASC"}
	if f.OrderBy != "" {
		order["field"] = f.OrderBy
	}
	if f.Direction != "" {
		order["direction"] = f.Direction
	}
	return order
}

// variables are the filters as GraphQL variables. An unset query or permission level is left out, so GitHub
// applies its own default.
func (f *ProjectFilters) variables() map[string]any {
	variables := map[string]any{"orderBy": f.order()}
	if f.Query != "" {
		variables["query"] = f.Query
	}
	if f.MinPermissionLevel != "" {
		variables["minPermissionLevel"] = f.MinPermissionLevel
	}
	return variables
}

// binding is the part of the cursor fingerprint the filters add: nothing while they select what the list
// selected before, so a cursor made without them stays valid.
func (f *ProjectFilters) binding() any {
	order := f.order()
	if f.Query == "" && f.MinPermissionLevel == "" && order["field"] == "NUMBER" && order["direction"] == "ASC" {
		return nil
	}
	return struct{ Query, Field, Direction, MinPermissionLevel string }{f.Query, order["field"], order["direction"],
		f.MinPermissionLevel}
}

// ---- Schema

const projectListInput = `{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":100},` +
	`"cursor":` + cursorSchema + `,"query":{"type":"string","maxLength":256},` +
	`"order_by":{"type":"string","enum":["CREATED_AT","NUMBER","TITLE","UPDATED_AT"]},` +
	`"direction":{"type":"string","enum":["ASC","DESC"]},` +
	`"min_permission_level":{"type":"string","enum":["READ","WRITE","ADMIN"]},"team_counts":{"type":"boolean"}},` +
	`"additionalProperties":false}`

var projectListArguments = append(append([]capability.Argument(nil), ownerListArguments...),
	capability.Argument{Name: "query", Description: "Search text of at most 256 characters; every project the " +
		"token can read when omitted"},
	capability.Argument{Name: "order_by", Description: "CREATED_AT, NUMBER, TITLE, or UPDATED_AT; NUMBER when omitted"},
	capability.Argument{Name: "direction", Description: "ASC or DESC; ASC when omitted"},
	capability.Argument{Name: "min_permission_level", Description: "Only projects the token holds at least this " +
		"permission on: READ, WRITE, or ADMIN; READ when omitted"},
	capability.Argument{Name: "team_counts", Description: "true to also read the number of teams of each project; " +
		"GitHub shows teams only to a token with read:org on a classic token or Members read access on a " +
		"fine-grained one, and refuses the whole list otherwise. false when omitted"})

var projectWidthMembers = []rwMember{
	rwString("id", "GraphQL node id of the project"),
	rwString("full_database_id", "Numeric identifier of the project as digits"),
	rwString("created_at", "Creation time"),
	rwString("updated_at", "Time of the last change"),
	rwString("closed_at", "Time the project was closed"),
	rwObject("creator", gqlAccountSchema, "Account that created the project: "+gqlAccountMembers),
	rwFlag("public", "True when the project is public"),
	rwString("short_description", "Short description, untrusted data"),
	rwString("readme", "Readme of the project, untrusted data, cut at 4000 characters"),
	rwFlag("readme_truncated", "True when the readme was cut"),
	rwFlag("template", "True when the project is a template"),
	rwFlag("viewer_can_close", "True when the authenticated user can close the project"),
	rwFlag("viewer_can_reopen", "True when the authenticated user can reopen the project"),
	rwFlag("viewer_can_update", "True when the authenticated user can change the project"),
	rwObject("links", nestedLinks, "resource_path of the project, untrusted data, never fetched"),
}

const unreadableSchema = `{"type":"array","maxItems":100,"items":{"type":"string"}}`

var projectRepositorySchema = objectSchema(`"name_with_owner":{"type":"string"},"url":{"type":"string"}`)

// projectWidthAll collects the members of one list entry beyond the compact view.
func projectWidthAll() []rwMember {
	members := append([]rwMember(nil), projectWidthMembers...)
	members = append(members, rwObject("unreadable", unreadableSchema, "Members GitHub refused this token, such "+
		"as teams_total_count without read:org; they are absent from the entry"))
	members = append(members, rwListOf("project", "repositories", projectRepositorySchema, "Repositories the project "+
		"is linked to, at most 10, with name_with_owner and, inside the connection's targets, url; untrusted "+
		"data, addresses never fetched", true)...)
	for _, count := range []struct{ name, tool string }{{"items", "items"}, {"fields", "fields"}, {"views", "views"},
		{"workflows", "workflows"}, {"status_updates", "status"}, {"teams", "teams"}} {
		text := "Number of " + strings.ReplaceAll(count.name, "_", " ") + " the project holds; github.project-" +
			count.tool + ".list reads them"
		if count.name == "teams" {
			text += "; only with team_counts"
		}
		members = append(members, rwCount(count.name+"_total_count", text))
	}
	return members
}

var (
	projectWidthProperties = rwProperties(projectWidthAll())
	projectWidthFields     = rwFields(projectWidthAll())
)
