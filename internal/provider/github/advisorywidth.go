package github

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The members of the advisory and code quality finding tools beyond their compact views. Each is cleaned and
// bounded like the shared projections; a list over its bound is cut and says so in its own *_truncated
// member, never in truncated. The flat members of the compact views stay as they are.

const (
	findingMarkdownLimit = 2000
	epssTop              = 1.0
)

var (
	globalAdvisorySorts    = []string{"updated", "published", "epss_percentage", "epss_percentile"}
	globalAdvisoryLinkKeys = []string{"url", "repository_advisory_url"}
	repoAdvisoryLinkKeys   = []string{"url"}
)

// ---- Output

// AdvisoryIdentifier is one identifier of an advisory, such as a CVE or a GHSA ID.
type AdvisoryIdentifier struct {
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
}

// AdvisoryCVSS is one CVSS rating; the score keeps the number as GitHub sent it.
type AdvisoryCVSS struct {
	VectorString string      `json:"vector_string,omitempty"`
	Score        json.Number `json:"score,omitempty"`
}

// AdvisoryCVSSSeverities holds the CVSS ratings by version.
type AdvisoryCVSSSeverities struct {
	V3 *AdvisoryCVSS `json:"cvss_v3,omitempty"`
	V4 *AdvisoryCVSS `json:"cvss_v4,omitempty"`
}

// AdvisoryCWE is one weakness with its name.
type AdvisoryCWE struct {
	CWEID string `json:"cwe_id,omitempty"`
	Name  string `json:"name,omitempty"`
}

// AdvisoryCredit is one credit of a global advisory.
type AdvisoryCredit struct {
	User *RESTAccount `json:"user,omitempty"`
	Type string       `json:"type,omitempty"`
}

// AdvisoryRepositoryCredit is one credit of a repository advisory by login.
type AdvisoryRepositoryCredit struct {
	Login string `json:"login,omitempty"`
	Type  string `json:"type,omitempty"`
}

// AdvisoryCreditDetail is one credit of a repository advisory with the credited account and its state.
type AdvisoryCreditDetail struct {
	User  *RESTAccount `json:"user,omitempty"`
	Type  string       `json:"type,omitempty"`
	State string       `json:"state,omitempty"`
}

// AdvisorySubmission tells whether a private vulnerability report was accepted.
type AdvisorySubmission struct {
	Accepted *bool `json:"accepted,omitempty"`
}

// AdvisoryCommon holds the members both advisory kinds carry.
type AdvisoryCommon struct {
	Identifiers          []AdvisoryIdentifier    `json:"identifiers,omitempty"`
	IdentifiersTruncated bool                    `json:"identifiers_truncated,omitempty"`
	CVSSSeverities       *AdvisoryCVSSSeverities `json:"cvss_severities,omitempty"`
	CWEsDetails          []AdvisoryCWE           `json:"cwes_details,omitempty"`
	CWEsDetailsTruncated bool                    `json:"cwes_details_truncated,omitempty"`
}

// MessageDetail is the message of a code quality finding in both forms.
type MessageDetail struct {
	Text              string `json:"text,omitempty"`
	Markdown          string `json:"markdown,omitempty"`
	MarkdownTruncated bool   `json:"markdown_truncated,omitempty"`
}

// ---- Raw

type advisoryIdentifierRaw struct {
	Type  *string `json:"type"`
	Value *string `json:"value"`
}

type advisoryCVSSRaw struct {
	VectorString *string      `json:"vector_string"`
	Score        *json.Number `json:"score"`
}

type advisoryCVSSSeveritiesRaw struct {
	V3 *advisoryCVSSRaw `json:"cvss_v3"`
	V4 *advisoryCVSSRaw `json:"cvss_v4"`
}

type advisoryCWERaw struct {
	ID   string  `json:"cwe_id"`
	Name *string `json:"name"`
}

// advisoryCreditRaw reads a credit of either advisory kind: a global credit holds user, a repository credit
// login, and a detailed repository credit user and state.
type advisoryCreditRaw struct {
	User  *accountRaw `json:"user"`
	Login *string     `json:"login"`
	Type  *string     `json:"type"`
	State *string     `json:"state"`
}

// advisoryCommonRaw holds the members both advisory kinds read.
type advisoryCommonRaw struct {
	Identifiers    []advisoryIdentifierRaw    `json:"identifiers"`
	CVSSSeverities *advisoryCVSSSeveritiesRaw `json:"cvss_severities"`
	CWEs           []advisoryCWERaw           `json:"cwes"`
	Credits        []advisoryCreditRaw        `json:"credits"`
}

// ---- Projection

func (r *advisoryCVSSRaw) project() *AdvisoryCVSS {
	if r == nil {
		return nil
	}
	out := AdvisoryCVSS{VectorString: valueOr(nameText(r.VectorString))}
	if r.Score != nil {
		out.Score = *r.Score
	}
	if out == (AdvisoryCVSS{}) {
		return nil
	}
	return &out
}

func (r *advisoryCommonRaw) project() AdvisoryCommon {
	var out AdvisoryCommon
	identifiers, cut := capped(r.Identifiers)
	for _, identifier := range identifiers {
		entry := AdvisoryIdentifier{Type: valueOr(nameText(identifier.Type)), Value: valueOr(nameText(identifier.Value))}
		if entry != (AdvisoryIdentifier{}) {
			out.Identifiers = append(out.Identifiers, entry)
		}
	}
	out.IdentifiersTruncated = cut
	if s := r.CVSSSeverities; s != nil {
		if v3, v4 := s.V3.project(), s.V4.project(); v3 != nil || v4 != nil {
			out.CVSSSeverities = &AdvisoryCVSSSeverities{V3: v3, V4: v4}
		}
	}
	cwes := r.CWEs
	if len(cwes) > advisoryMaxCWEs {
		cwes, out.CWEsDetailsTruncated = cwes[:advisoryMaxCWEs], true
	}
	for _, cwe := range cwes {
		id := cwe.ID
		entry := AdvisoryCWE{CWEID: valueOr(nameText(&id)), Name: valueOr(nameText(cwe.Name))}
		if entry != (AdvisoryCWE{}) {
			out.CWEsDetails = append(out.CWEsDetails, entry)
		}
	}
	return out
}

// credits projects the credits of a global advisory.
func (r *advisoryCommonRaw) credits() ([]AdvisoryCredit, bool) {
	raw, cut := capped(r.Credits)
	var out []AdvisoryCredit
	for _, credit := range raw {
		entry := AdvisoryCredit{User: credit.User.project(), Type: valueOr(nameText(credit.Type))}
		if entry != (AdvisoryCredit{}) {
			out = append(out, entry)
		}
	}
	return out, cut
}

// repositoryCredits projects the credits of a repository advisory, which name the credited login only.
func (r *advisoryCommonRaw) repositoryCredits() ([]AdvisoryRepositoryCredit, bool) {
	raw, cut := capped(r.Credits)
	var out []AdvisoryRepositoryCredit
	for _, credit := range raw {
		entry := AdvisoryRepositoryCredit{Login: valueOr(nameText(credit.Login)), Type: valueOr(nameText(credit.Type))}
		if entry != (AdvisoryRepositoryCredit{}) {
			out = append(out, entry)
		}
	}
	return out, cut
}

func detailedCredits(raw []advisoryCreditRaw) ([]AdvisoryCreditDetail, bool) {
	raw, cut := capped(raw)
	var out []AdvisoryCreditDetail
	for _, credit := range raw {
		entry := AdvisoryCreditDetail{User: credit.User.project(), Type: valueOr(nameText(credit.Type)),
			State: valueOr(nameText(credit.State))}
		if entry != (AdvisoryCreditDetail{}) {
			out = append(out, entry)
		}
	}
	return out, cut
}

// ---- Schema and fields

const advisoryCVSSSchema = `{"type":"object","properties":{"vector_string":{"type":"string"},` +
	`"score":{"type":"number"}},"additionalProperties":false}`

const advisoryCommonProperties = `"identifiers":{"type":"array","maxItems":100,"items":{"type":"object",` +
	`"properties":{"type":{"type":"string"},"value":{"type":"string"}},"additionalProperties":false}},` +
	`"identifiers_truncated":{"type":"boolean"},"cvss_severities":{"type":"object","properties":{` +
	`"cvss_v3":` + advisoryCVSSSchema + `,"cvss_v4":` + advisoryCVSSSchema + `},"additionalProperties":false},` +
	`"cwes_details":{"type":"array","maxItems":20,"items":{"type":"object","properties":{` +
	`"cwe_id":{"type":"string"},"name":{"type":"string"}},"additionalProperties":false}},` +
	`"cwes_details_truncated":{"type":"boolean"}`

func creditsSchema(properties string) string {
	return `{"type":"array","maxItems":100,"items":` + objectSchema(properties) + `}`
}

var advisoryTypeProperty = `"type":{"type":"string"}`

var globalAdvisoryWidthProperties = advisoryCommonProperties + `,"credits":` +
	creditsSchema(`"user":`+restAccountSchema+`,`+advisoryTypeProperty) + `,"credits_truncated":{"type":"boolean"},` +
	`"links":` + linksSchema(globalAdvisoryLinkKeys)

var repositoryAdvisoryWidthProperties = advisoryCommonProperties + `,"cwe_ids":{"type":"array","maxItems":20,` +
	`"items":{"type":"string"}},"submission":` + objectSchema(`"accepted":{"type":"boolean"}`) +
	`,"credits":` + creditsSchema(`"login":{"type":"string"},`+advisoryTypeProperty) +
	`,"credits_truncated":{"type":"boolean"},"credits_detailed":` +
	creditsSchema(`"user":`+restAccountSchema+`,`+advisoryTypeProperty+`,"state":{"type":"string"}`) +
	`,"credits_detailed_truncated":{"type":"boolean"},"collaborating_users":{"type":"array","maxItems":100,` +
	`"items":` + restAccountSchema + `},"collaborating_users_truncated":{"type":"boolean"},` +
	`"collaborating_teams":{"type":"array","maxItems":100,"items":` + teamSimpleSchema + `},` +
	`"collaborating_teams_truncated":{"type":"boolean"},"private_fork":` + restRepositorySchema + `,` +
	detailProperty("author", restAccountSchema, false) + `,` + detailProperty("publisher", restAccountSchema, false) +
	`,"links":` + linksSchema(repoAdvisoryLinkKeys)

var advisoryCommonFields = []capability.Field{
	{Name: "identifiers", Description: "Identifiers of the advisory as type and value, at most 100; untrusted data"},
	{Name: "identifiers_truncated", Description: "True when the advisory holds more identifiers than shown"},
	{Name: "cvss_severities", Description: "CVSS ratings by version, cvss_v3 and cvss_v4, each with " +
		"vector_string and score; the flat cvss_score and cvss_vector stay as they are"},
	{Name: "cwes_details", Description: "Weaknesses as cwe_id and name, at most 20; untrusted data; cwes keeps the " +
		"identifiers"},
	{Name: "cwes_details_truncated", Description: "True when the advisory holds more weaknesses than shown"},
}

var globalAdvisoryWidthFields = append(append([]capability.Field(nil), advisoryCommonFields...),
	capability.Field{Name: "credits", Description: "Credited accounts, at most 100, each with user (" +
		restAccountMembers + ") and the credit type"},
	capability.Field{Name: "credits_truncated", Description: "True when the advisory holds more credits than shown"},
	capability.Field{Name: "links", Description: "The REST url and the repository_advisory_url of the advisory, " +
		"untrusted data, never fetched"})

var repositoryAdvisoryWidthFields = append(append([]capability.Field(nil), advisoryCommonFields...),
	capability.Field{Name: "cwe_ids", Description: "CWE identifiers of the weaknesses, as GitHub names them"},
	capability.Field{Name: "submission", Description: "accepted: whether a private vulnerability report was accepted"},
	capability.Field{Name: "credits", Description: "Credits as the credited login and the credit type, at most 100; " +
		"untrusted data"},
	capability.Field{Name: "credits_truncated", Description: "True when the advisory holds more credits than shown"},
	capability.Field{Name: "credits_detailed", Description: "Credits with the credited account (" +
		restAccountMembers + "), the credit type, and the state accepted, declined, or pending, at most 100"},
	capability.Field{Name: "credits_detailed_truncated", Description: "True when the advisory holds more detailed " +
		"credits than shown"},
	capability.Field{Name: "collaborating_users", Description: "Accounts collaborating on the advisory, at most 100: " +
		restAccountMembers},
	capability.Field{Name: "collaborating_users_truncated", Description: "True when more users collaborate than shown"},
	capability.Field{Name: "collaborating_teams", Description: "Teams collaborating on the advisory, at most 100, each " +
		"with id, node_id, name, slug, description, privacy, notification_setting, permission, url (the web " +
		"address), and links; untrusted data"},
	capability.Field{Name: "collaborating_teams_truncated", Description: "True when more teams collaborate than shown"},
	capability.Field{Name: "private_fork", Description: "The temporary private fork for collaborating on a fix, as a " +
		"repository; outside the connection's targets only id, node_id, and full_name"},
	detailField("author", "The account", false),
	detailField("publisher", "The account", false),
	capability.Field{Name: "links", Description: "The REST url of the advisory, untrusted data, never fetched"})

// ---- Filters

const epssBoundSchema = `{"type":"number","minimum":0,"maximum":1}`

const epssKeys = `"epss_percentage_min":` + epssBoundSchema + `,"epss_percentage_max":` + epssBoundSchema +
	`,"epss_percentile_min":` + epssBoundSchema + `,"epss_percentile_max":` + epssBoundSchema

// epssRange turns the optional bounds of one EPSS filter into the form GitHub documents: n, >=n, <=n, or
// n..n. A bound is a number from 0.0 to 1.0, and the lower bound is at most the upper.
func epssRange(name string, min, max *json.Number) (string, error) {
	read := func(bound string, n *json.Number) (float64, string, error) {
		if n == nil {
			return 0, "", nil
		}
		f, err := strconv.ParseFloat(string(*n), 64)
		if err != nil || math.IsNaN(f) || f < 0 || f > epssTop {
			return 0, "", invalidRequest(name + "_" + bound + " must be a number from 0.0 to 1.0")
		}
		return f, strconv.FormatFloat(f, 'f', -1, 64), nil
	}
	lo, loText, err := read("min", min)
	if err != nil {
		return "", err
	}
	hi, hiText, err := read("max", max)
	if err != nil {
		return "", err
	}
	switch {
	case min != nil && max != nil && lo > hi:
		return "", invalidRequest(name + "_min must not exceed " + name + "_max")
	case min != nil && max != nil && lo == hi:
		return loText, nil
	case min != nil && max != nil:
		return loText + ".." + hiText, nil
	case min != nil:
		return ">=" + loText, nil
	case max != nil:
		return "<=" + hiText, nil
	}
	return "", nil
}

// globalFilterBinding returns the cursor binding parts of the filters this width added, or none when none
// is set, so a cursor issued before them stays valid.
func (a *advisoryArguments) globalFilterBinding() []any {
	if a.Sort == "" && a.Direction == "" && a.epssPercentage == "" && a.epssPercentile == "" {
		return nil
	}
	return []any{strings.ToLower(a.Sort), strings.ToLower(a.Direction), a.epssPercentage, a.epssPercentile}
}

func (a *advisoryArguments) checkGlobalFilters() error {
	if a.Sort != "" && !containsFold(globalAdvisorySorts, a.Sort) {
		return invalidRequest("sort must be one of " + strings.Join(globalAdvisorySorts, ", "))
	}
	if a.Direction != "" && !containsFold(advisoryDirections, a.Direction) {
		return invalidRequest("direction must be one of " + strings.Join(advisoryDirections, ", "))
	}
	var err error
	if a.epssPercentage, err = epssRange("epss_percentage", a.EPSSPercentageMin, a.EPSSPercentageMax); err != nil {
		return err
	}
	a.epssPercentile, err = epssRange("epss_percentile", a.EPSSPercentileMin, a.EPSSPercentileMax)
	return err
}
