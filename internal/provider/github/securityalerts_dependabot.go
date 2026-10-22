package github

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The members of a Dependabot alert beyond its compact flat members, and the filters of the list. Only
// the members here pass through the projection helpers; the flat members keep their handling. A member
// of an unexpected shape is left out and never fails the alert.

const (
	dependabotSummaryLimit = 1024
	dependabotPathLimit    = 1024
	dependabotPackageLimit = 256
	maxDependabotValues    = 10
)

var (
	dependabotClassifications = []string{"malware", "general"}
	dependabotEcosystems      = []string{"composer", "go", "maven", "npm", "nuget", "pip", "pub", "rubygems", "rust"}
	dependabotHas             = []string{"patch"}
	dependabotScopes          = []string{"development", "runtime"}
	dependabotRelationships   = []string{"direct", "transitive", "inconclusive", "unknown"}
	dependabotSorts           = []string{"created", "updated", "epss_percentage"}
	dependabotAlertLinkKeys   = []string{"url"}
)

// ---- Output

// DependabotPackage is the package of a dependency or vulnerability.
type DependabotPackage struct {
	Ecosystem string `json:"ecosystem,omitempty"`
	Name      string `json:"name,omitempty"`
}

// DependabotDependency is the vulnerable dependency of an alert.
type DependabotDependency struct {
	Package               *DependabotPackage `json:"package,omitempty"`
	ManifestPath          string             `json:"manifest_path,omitempty"`
	ManifestPathTruncated bool               `json:"manifest_path_truncated,omitempty"`
	VersionRequirement    string             `json:"version_requirement,omitempty"`
	Scope                 string             `json:"scope,omitempty"`
	Relationship          string             `json:"relationship,omitempty"`
}

// DependabotVulnerability is one vulnerable version range of an advisory.
type DependabotVulnerability struct {
	Package                *DependabotPackage `json:"package,omitempty"`
	Severity               string             `json:"severity,omitempty"`
	VulnerableVersionRange string             `json:"vulnerable_version_range,omitempty"`
	FirstPatchedVersion    string             `json:"first_patched_version,omitempty"`
}

// DependabotCVSS is a CVSS score and vector.
type DependabotCVSS struct {
	Score        json.Number `json:"score,omitempty"`
	VectorString string      `json:"vector_string,omitempty"`
}

// DependabotCVSSSeverities holds the CVSS 3 and 4 scores of an advisory.
type DependabotCVSSSeverities struct {
	CVSSV3 *DependabotCVSS `json:"cvss_v3,omitempty"`
	CVSSV4 *DependabotCVSS `json:"cvss_v4,omitempty"`
}

// DependabotEPSS is the exploit prediction score of an advisory.
type DependabotEPSS struct {
	Percentage json.Number `json:"percentage,omitempty"`
	Percentile json.Number `json:"percentile,omitempty"`
}

// DependabotCWE is a weakness of an advisory.
type DependabotCWE struct {
	CWEID string `json:"cwe_id,omitempty"`
	Name  string `json:"name,omitempty"`
}

// DependabotIdentifier identifies an advisory in a source.
type DependabotIdentifier struct {
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
}

// DependabotReference is a link to more advisory information; URL is untrusted and never fetched.
type DependabotReference struct {
	URL string `json:"url,omitempty"`
}

// DependabotAdvisory is the security advisory of an alert.
type DependabotAdvisory struct {
	GHSAID                   string                    `json:"ghsa_id,omitempty"`
	CVEID                    string                    `json:"cve_id,omitempty"`
	Summary                  string                    `json:"summary,omitempty"`
	SummaryTruncated         bool                      `json:"summary_truncated,omitempty"`
	Description              string                    `json:"description,omitempty"`
	DescriptionTruncated     bool                      `json:"description_truncated,omitempty"`
	Vulnerabilities          []DependabotVulnerability `json:"vulnerabilities,omitempty"`
	VulnerabilitiesTruncated bool                      `json:"vulnerabilities_truncated,omitempty"`
	Severity                 string                    `json:"severity,omitempty"`
	Classification           string                    `json:"classification,omitempty"`
	CVSS                     *DependabotCVSS           `json:"cvss,omitempty"`
	CVSSSeverities           *DependabotCVSSSeverities `json:"cvss_severities,omitempty"`
	EPSS                     *DependabotEPSS           `json:"epss,omitempty"`
	CWEs                     []DependabotCWE           `json:"cwes,omitempty"`
	CWEsTruncated            bool                      `json:"cwes_truncated,omitempty"`
	Identifiers              []DependabotIdentifier    `json:"identifiers,omitempty"`
	IdentifiersTruncated     bool                      `json:"identifiers_truncated,omitempty"`
	References               []DependabotReference     `json:"references,omitempty"`
	ReferencesTruncated      bool                      `json:"references_truncated,omitempty"`
	PublishedAt              string                    `json:"published_at,omitempty"`
	UpdatedAt                string                    `json:"updated_at,omitempty"`
	WithdrawnAt              string                    `json:"withdrawn_at,omitempty"`
}

// DependabotDismissalRequest is the active dismissal request of an alert.
type DependabotDismissalRequest struct {
	ID        *int64       `json:"id,omitempty"`
	Status    string       `json:"status,omitempty"`
	Requester *RESTAccount `json:"requester,omitempty"`
	CreatedAt string       `json:"created_at,omitempty"`
	Links     Links        `json:"links,omitempty"`
}

// ---- Raw

type dependabotPackageRaw struct {
	Ecosystem *string `json:"ecosystem"`
	Name      *string `json:"name"`
}

type dependabotVulnerabilityRaw struct {
	Package                *dependabotPackageRaw `json:"package"`
	Severity               *string               `json:"severity"`
	VulnerableVersionRange *string               `json:"vulnerable_version_range"`
	FirstPatchedVersion    *struct {
		Identifier *string `json:"identifier"`
	} `json:"first_patched_version"`
}

type dependabotCVSSRaw struct {
	Score        *json.Number `json:"score"`
	VectorString *string      `json:"vector_string"`
}

type dependabotDependencyRaw struct {
	Package            *dependabotPackageRaw `json:"package"`
	ManifestPath       *string               `json:"manifest_path"`
	VersionRequirement *string               `json:"version_requirement"`
	Scope              *string               `json:"scope"`
	Relationship       *string               `json:"relationship"`
}

type dependabotAdvisoryRaw struct {
	GHSAID          *string                      `json:"ghsa_id"`
	CVEID           *string                      `json:"cve_id"`
	Summary         *string                      `json:"summary"`
	Description     *string                      `json:"description"`
	Vulnerabilities []dependabotVulnerabilityRaw `json:"vulnerabilities"`
	Severity        *string                      `json:"severity"`
	Classification  *string                      `json:"classification"`
	CVSS            *dependabotCVSSRaw           `json:"cvss"`
	CVSSSeverities  *struct {
		V3 *dependabotCVSSRaw `json:"cvss_v3"`
		V4 *dependabotCVSSRaw `json:"cvss_v4"`
	} `json:"cvss_severities"`
	EPSS json.RawMessage `json:"epss"`
	CWEs []struct {
		CWEID *string `json:"cwe_id"`
		Name  *string `json:"name"`
	} `json:"cwes"`
	Identifiers []struct {
		Type  *string `json:"type"`
		Value *string `json:"value"`
	} `json:"identifiers"`
	References []struct {
		URL *string `json:"url"`
	} `json:"references"`
	PublishedAt *string `json:"published_at"`
	UpdatedAt   *string `json:"updated_at"`
	WithdrawnAt *string `json:"withdrawn_at"`
}

type dependabotDismissalRequestRaw struct {
	ID        *int64      `json:"id"`
	Status    *string     `json:"status"`
	Requester *accountRaw `json:"requester"`
	CreatedAt *string     `json:"created_at"`
	links     Links
}

func (r *dependabotDismissalRequestRaw) UnmarshalJSON(data []byte) (err error) {
	type plain dependabotDismissalRequestRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, dependabotAlertLinkKeys)
	return err
}

// dependabotExtrasRaw holds the members of an alert beyond its flat members.
type dependabotExtrasRaw struct {
	links            Links
	dependency       *dependabotDependencyRaw
	advisory         *dependabotAdvisoryRaw
	vulnerability    *dependabotVulnerabilityRaw
	dismissedBy      *accountRaw
	dismissalRequest *dependabotDismissalRequestRaw
	assignees        []accountRaw
}

// readExtras decodes each member on its own, so one of an unexpected shape only drops itself.
func readExtras(data []byte) dependabotExtrasRaw {
	var x dependabotExtrasRaw
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) != nil {
		return x
	}
	x.links, _ = readLinks(data, dependabotAlertLinkKeys)
	for key, dst := range map[string]any{"dependency": &x.dependency, "security_advisory": &x.advisory,
		"security_vulnerability": &x.vulnerability, "dismissed_by": &x.dismissedBy,
		"dismissal_request": &x.dismissalRequest, "assignees": &x.assignees} {
		if json.Unmarshal(all[key], dst) != nil {
			switch p := dst.(type) {
			case **dependabotDependencyRaw:
				*p = nil
			case **dependabotAdvisoryRaw:
				*p = nil
			case **dependabotVulnerabilityRaw:
				*p = nil
			case **accountRaw:
				*p = nil
			case **dependabotDismissalRequestRaw:
				*p = nil
			case *[]accountRaw:
				*p = nil
			}
		}
	}
	return x
}

// ---- Projection

func (r *dependabotPackageRaw) project() *DependabotPackage {
	if r == nil {
		return nil
	}
	p := DependabotPackage{Ecosystem: shortString(r.Ecosystem), Name: shortString(r.Name)}
	if p == (DependabotPackage{}) {
		return nil
	}
	return &p
}

func (r dependabotVulnerabilityRaw) project() DependabotVulnerability {
	v := DependabotVulnerability{Package: r.Package.project(), Severity: shortString(r.Severity),
		VulnerableVersionRange: shortString(r.VulnerableVersionRange)}
	if r.FirstPatchedVersion != nil {
		v.FirstPatchedVersion = shortString(r.FirstPatchedVersion.Identifier)
	}
	return v
}

func (r *dependabotCVSSRaw) project() *DependabotCVSS {
	if r == nil {
		return nil
	}
	c := DependabotCVSS{VectorString: shortString(r.VectorString)}
	if r.Score != nil {
		c.Score = *r.Score
	}
	if c == (DependabotCVSS{}) {
		return nil
	}
	return &c
}

// shortString is a cleaned name, or empty when GitHub left it out.
func shortString(p *string) string {
	if text := shortText(p); text != nil {
		return *text
	}
	return ""
}

// textOf cleans a single-line text, cuts it at limit characters, and reports the cut.
func textOf(p *string, limit int) (string, bool) {
	text, cut := cleanText(filled(p), limit, false)
	if text == nil {
		return "", false
	}
	return *text, cut
}

func (x dependabotExtrasRaw) addTo(v *DependabotAlert, detail bool) {
	v.Links = x.links
	v.DismissedByDetail = x.dismissedBy.project()
	v.Assignees, v.AssigneesTruncated = accounts(x.assignees)

	if d := x.dependency; d != nil {
		dep := DependabotDependency{Package: d.Package.project(), VersionRequirement: shortString(d.VersionRequirement),
			Scope: shortString(d.Scope), Relationship: shortString(d.Relationship)}
		dep.ManifestPath, dep.ManifestPathTruncated = textOf(d.ManifestPath, dependabotPathLimit)
		if dep != (DependabotDependency{}) {
			v.Dependency = &dep
		}
	}
	if s := x.vulnerability; s != nil {
		if vulnerability := s.project(); vulnerability != (DependabotVulnerability{}) {
			v.SecurityVulnerability = &vulnerability
		}
	}
	if a := x.advisory; a != nil {
		advisory := a.project(detail)
		if !isZeroAdvisory(advisory) {
			v.SecurityAdvisory = &advisory
		}
	}
	if r := x.dismissalRequest; r != nil {
		request := DependabotDismissalRequest{ID: r.ID, Status: shortString(r.Status),
			Requester: r.Requester.project(), CreatedAt: shortString(r.CreatedAt), Links: r.links}
		if request.ID != nil || request.Status != "" || request.Requester != nil || request.CreatedAt != "" ||
			request.Links != nil {
			v.DismissalRequest = &request
		}
	}
}

func isZeroAdvisory(a DependabotAdvisory) bool {
	return a.GHSAID == "" && a.CVEID == "" && a.Summary == "" && a.Description == "" && a.Severity == "" &&
		a.Classification == "" && a.CVSS == nil && a.CVSSSeverities == nil && a.EPSS == nil &&
		len(a.Vulnerabilities) == 0 && len(a.CWEs) == 0 && len(a.Identifiers) == 0 && len(a.References) == 0 &&
		a.PublishedAt == "" && a.UpdatedAt == "" && a.WithdrawnAt == ""
}

// project cuts the description at 500 characters in a list and at 4000 in a get.
func (r *dependabotAdvisoryRaw) project(detail bool) DependabotAdvisory {
	a := DependabotAdvisory{GHSAID: shortString(r.GHSAID), CVEID: shortString(r.CVEID),
		Severity: shortString(r.Severity), Classification: shortString(r.Classification), CVSS: r.CVSS.project(),
		PublishedAt: shortString(r.PublishedAt), UpdatedAt: shortString(r.UpdatedAt), WithdrawnAt: shortString(r.WithdrawnAt)}
	a.Summary, a.SummaryTruncated = textOf(r.Summary, dependabotSummaryLimit)
	limit := alertTextLimit
	if detail {
		limit = alertHelpLimit
	}
	if r.Description != nil && *r.Description != "" {
		a.Description, a.DescriptionTruncated = clipClean(*r.Description, limit)
	}
	vulnerabilities, cut := capped(r.Vulnerabilities)
	for _, vulnerability := range vulnerabilities {
		a.Vulnerabilities = append(a.Vulnerabilities, vulnerability.project())
	}
	a.VulnerabilitiesTruncated = cut
	if s := r.CVSSSeverities; s != nil {
		severities := DependabotCVSSSeverities{CVSSV3: s.V3.project(), CVSSV4: s.V4.project()}
		if severities != (DependabotCVSSSeverities{}) {
			a.CVSSSeverities = &severities
		}
	}
	// An unusable epss only drops itself.
	var e struct {
		Percentage *json.Number `json:"percentage"`
		Percentile *json.Number `json:"percentile"`
	}
	if json.Unmarshal(r.EPSS, &e) == nil {
		epss := DependabotEPSS{}
		if e.Percentage != nil {
			epss.Percentage = *e.Percentage
		}
		if e.Percentile != nil {
			epss.Percentile = *e.Percentile
		}
		if epss != (DependabotEPSS{}) {
			a.EPSS = &epss
		}
	}
	cwes, cut := capped(r.CWEs)
	for _, cwe := range cwes {
		if entry := (DependabotCWE{CWEID: shortString(cwe.CWEID), Name: shortString(cwe.Name)}); entry != (DependabotCWE{}) {
			a.CWEs = append(a.CWEs, entry)
		}
	}
	a.CWEsTruncated = cut
	identifiers, cut := capped(r.Identifiers)
	for _, identifier := range identifiers {
		if entry := (DependabotIdentifier{Type: shortString(identifier.Type), Value: shortString(identifier.Value)}); entry != (DependabotIdentifier{}) {
			a.Identifiers = append(a.Identifiers, entry)
		}
	}
	a.IdentifiersTruncated = cut
	references, cut := capped(r.References)
	for _, reference := range references {
		if uri := address(reference.URL); uri != nil {
			a.References = append(a.References, DependabotReference{URL: *uri})
		}
	}
	a.ReferencesTruncated = cut
	return a
}

// ---- Schema and fields

var (
	dependabotPackageSchema = objectSchema(`"ecosystem":{"type":"string"},"name":{"type":"string"}`)
	dependabotCVSSSchema    = objectSchema(`"score":{"type":"number"},"vector_string":{"type":"string"}`)
	dependabotVulnProps     = `"package":` + dependabotPackageSchema + `,"severity":{"type":"string"},` +
		`"vulnerable_version_range":{"type":"string"},"first_patched_version":{"type":"string"}`
	dependabotLinksSchema = linksSchema(dependabotAlertLinkKeys)
)

func listSchema(item string) string {
	return `{"type":"array","maxItems":100,"items":` + item + `}`
}

var dependabotDetailProperties = `,"links":` + dependabotLinksSchema + `,"dependency":` +
	objectSchema(`"package":`+dependabotPackageSchema+`,"manifest_path":{"type":"string"},`+
		`"manifest_path_truncated":{"type":"boolean"},"version_requirement":{"type":"string"},`+
		`"scope":{"type":"string"},"relationship":{"type":"string"}`) + `,"security_advisory":` +
	objectSchema(`"ghsa_id":{"type":"string"},"cve_id":{"type":"string"},"summary":{"type":"string"},`+
		`"summary_truncated":{"type":"boolean"},"description":{"type":"string"},`+
		`"description_truncated":{"type":"boolean"},"vulnerabilities":`+listSchema(objectSchema(dependabotVulnProps))+
		`,"vulnerabilities_truncated":{"type":"boolean"},"severity":{"type":"string"},`+
		`"classification":{"type":"string"},"cvss":`+dependabotCVSSSchema+`,"cvss_severities":`+
		objectSchema(`"cvss_v3":`+dependabotCVSSSchema+`,"cvss_v4":`+dependabotCVSSSchema)+
		`,"epss":`+objectSchema(`"percentage":{"type":"number"},"percentile":{"type":"number"}`)+
		`,"cwes":`+listSchema(objectSchema(`"cwe_id":{"type":"string"},"name":{"type":"string"}`))+
		`,"cwes_truncated":{"type":"boolean"},"identifiers":`+
		listSchema(objectSchema(`"type":{"type":"string"},"value":{"type":"string"}`))+
		`,"identifiers_truncated":{"type":"boolean"},"references":`+
		listSchema(objectSchema(`"url":{"type":"string"}`))+`,"references_truncated":{"type":"boolean"},`+
		`"published_at":{"type":"string"},"updated_at":{"type":"string"},"withdrawn_at":{"type":"string"}`) +
	`,"security_vulnerability":` + objectSchema(dependabotVulnProps) + `,"dismissed_by_detail":` +
	restAccountSchema + `,"dismissal_request":` + objectSchema(`"id":{"type":"integer"},"status":{"type":"string"},`+
	`"requester":`+restAccountSchema+`,"created_at":{"type":"string"},"links":`+dependabotLinksSchema) +
	`,"assignees":` + listSchema(restAccountSchema) + `,"assignees_truncated":{"type":"boolean"}`

var dependabotDetailFields = []capability.Field{
	{Name: "links", Description: "REST url of the alert by its GitHub name, untrusted data, never fetched"},
	{Name: "dependency", Description: "Vulnerable dependency: package (ecosystem and name), manifest_path, " +
		"version_requirement, scope, and relationship; untrusted data, a cut manifest_path says so in " +
		"manifest_path_truncated"},
	{Name: "security_advisory", Description: "Advisory: ghsa_id, cve_id, summary, description, vulnerabilities " +
		"(package, severity, vulnerable_version_range, first_patched_version), severity, classification, cvss " +
		"(score, vector_string), cvss_severities (cvss_v3 and cvss_v4), epss (percentage, percentile), cwes " +
		"(cwe_id, name), identifiers (type, value), references (url), published_at, updated_at, and " +
		"withdrawn_at; untrusted data, summary and description are cut and each cut text and list of at most " +
		"100 entries says so in its *_truncated member"},
	{Name: "security_vulnerability", Description: "Vulnerable version range of the alert: package, severity, " +
		"vulnerable_version_range, and first_patched_version"},
	{Name: "dismissed_by_detail", Description: "Account that dismissed the alert: " + restAccountMembers},
	{Name: "dismissal_request", Description: "Active dismissal request: id, status, requester (an account), " +
		"created_at, and links (the REST url); untrusted data"},
	{Name: "assignees", Description: "Accounts the alert is assigned to, at most 100: " + restAccountMembers},
	{Name: "assignees_truncated", Description: "True when the alert has more assignees than shown"},
}

// ---- List filters

const (
	dependabotStringSchema = `{"type":"string","minLength":1,"maxLength":%d}`
	dependabotEPSSSchema   = `{"type":"number","minimum":0,"maximum":1}`
)

func enumListSchema(values []string) string {
	return `{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(len(values)) + `,"uniqueItems":true,"items":` +
		enumSchema(values) + `}`
}

func namesListSchema(limit int) string {
	return `{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxDependabotValues) + `,"items":` +
		fmt.Sprintf(dependabotStringSchema, limit) + `}`
}

var dependabotFilterSchema = `"classification":` + enumListSchema(dependabotClassifications) + `,"ecosystem":` +
	enumListSchema(dependabotEcosystems) + `,"package":` + namesListSchema(dependabotPackageLimit) + `,"manifest":` +
	namesListSchema(dependabotPathLimit) + `,"epss_min":` + dependabotEPSSSchema + `,"epss_max":` +
	dependabotEPSSSchema + `,"has":` + enumListSchema(dependabotHas) + `,"assignee":{"type":"array","minItems":1,` +
	`"maxItems":10,"items":{"type":"string","minLength":1,"maxLength":100,"pattern":"` + alertAssigneesReg + `"}},` +
	`"scope":` + enumSchema(dependabotScopes) + `,"relationship":` + enumListSchema(dependabotRelationships) +
	`,"sort":` + enumSchema(dependabotSorts) + `,"direction":` + enumSchema(codeScanningDirections)

var dependabotFilterArguments = []capability.Argument{
	{Name: "classification", Description: "Return only alerts of these advisory classifications: " +
		strings.Join(dependabotClassifications, ", ")},
	{Name: "ecosystem", Description: "Return only alerts of these package ecosystems: " +
		strings.Join(dependabotEcosystems, ", ")},
	{Name: "package", Description: "Return only alerts of these package names, at most 10 of 256 characters"},
	{Name: "manifest", Description: "Return only alerts of these manifest paths, at most 10 of 1024 characters"},
	{Name: "epss_min", Description: "Return only alerts whose EPSS percentage is at least this number, 0.0 to 1.0"},
	{Name: "epss_max", Description: "Return only alerts whose EPSS percentage is at most this number, 0.0 to 1.0"},
	{Name: "has", Description: "Return only alerts that have all of these: " + strings.Join(dependabotHas, ", ")},
	{Name: "assignee", Description: "Return only alerts assigned to any of these logins, at most 10; [\"*\"] " +
		"returns alerts with at least one assignee and [\"none\"] alerts with none, each only alone"},
	{Name: "scope", Description: "Return only alerts of this dependency scope: " + strings.Join(dependabotScopes, ", ")},
	{Name: "relationship", Description: "Return only alerts of these dependency relationships: " +
		strings.Join(dependabotRelationships, ", ")},
	{Name: "sort", Description: "Sort by " + strings.Join(dependabotSorts, ", ") + "; created when omitted"},
	{Name: "direction", Description: "Sort direction, " + strings.Join(codeScanningDirections, " or ") +
		"; desc when omitted"},
}

// hasDependabotFilters reports whether a request sets a filter beyond state and severity.
func (a *alertArguments) hasDependabotFilters() bool {
	return len(a.Classification) > 0 || len(a.Ecosystem) > 0 || len(a.Package) > 0 || len(a.Manifest) > 0 ||
		a.EPSSMin != nil || a.EPSSMax != nil || len(a.Has) > 0 || len(a.Assignee) > 0 || a.Scope != "" ||
		len(a.Relationship) > 0 || a.Sort != "" || a.Direction != ""
}

// checkDependabotFilters rejects a value or combination GitHub would not take, before any credential is
// resolved.
func (a *alertArguments) checkDependabotFilters() error {
	for _, list := range []struct {
		name    string
		values  []string
		allowed []string
	}{{"classification", a.Classification, dependabotClassifications}, {"ecosystem", a.Ecosystem, dependabotEcosystems},
		{"has", a.Has, dependabotHas}, {"relationship", a.Relationship, dependabotRelationships}} {
		seen := map[string]bool{}
		for _, value := range list.values {
			if !containsFold(list.allowed, value) || seen[strings.ToLower(value)] {
				return invalidRequest(list.name + " must list each of " + strings.Join(list.allowed, ", ") + " at most once")
			}
			seen[strings.ToLower(value)] = true
		}
	}
	for _, list := range []struct {
		name   string
		values []string
		limit  int
	}{{"package", a.Package, dependabotPackageLimit}, {"manifest", a.Manifest, dependabotPathLimit}} {
		if len(list.values) > maxDependabotValues {
			return invalidRequest(fmt.Sprintf("%s accepts at most %d values", list.name, maxDependabotValues))
		}
		for _, value := range list.values {
			if value == "" || utf8.RuneCountInString(value) > list.limit || strings.ContainsRune(value, ',') ||
				strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return invalidRequest(fmt.Sprintf("%s must be names of at most %d characters without commas "+
					"or control characters", list.name, list.limit))
			}
		}
	}
	for _, bound := range []*float64{a.EPSSMin, a.EPSSMax} {
		if bound != nil && !(*bound >= 0 && *bound <= 1) {
			return invalidRequest("epss_min and epss_max must be numbers from 0.0 to 1.0")
		}
	}
	if a.EPSSMin != nil && a.EPSSMax != nil && *a.EPSSMin > *a.EPSSMax {
		return invalidRequest("epss_min must not exceed epss_max")
	}
	if len(a.Assignee) > maxAssignees {
		return invalidRequest(fmt.Sprintf("assignee accepts at most %d values", maxAssignees))
	}
	for _, login := range a.Assignee {
		special := login == "*" || strings.EqualFold(login, "none")
		if special && len(a.Assignee) > 1 {
			return invalidRequest("assignee accepts * or none only alone")
		}
		if !special && !validLogin(login) {
			return invalidRequest("assignee must be GitHub logins, or * or none")
		}
	}
	if a.Scope != "" && !containsFold(dependabotScopes, a.Scope) {
		return invalidRequest("scope must be one of " + strings.Join(dependabotScopes, ", "))
	}
	if a.Sort != "" && !containsFold(dependabotSorts, a.Sort) {
		return invalidRequest("sort must be one of " + strings.Join(dependabotSorts, ", "))
	}
	if a.Direction != "" && !containsFold(codeScanningDirections, a.Direction) {
		return invalidRequest("direction must be one of " + strings.Join(codeScanningDirections, ", "))
	}
	return nil
}

// epssQuery is the epss_percentage value: n, >=n, <=n, or n..n.
func (a *alertArguments) epssQuery() string {
	format := func(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }
	switch {
	case a.EPSSMin != nil && a.EPSSMax != nil && *a.EPSSMin == *a.EPSSMax:
		return format(*a.EPSSMin)
	case a.EPSSMin != nil && a.EPSSMax != nil:
		return format(*a.EPSSMin) + ".." + format(*a.EPSSMax)
	case a.EPSSMin != nil:
		return ">=" + format(*a.EPSSMin)
	case a.EPSSMax != nil:
		return "<=" + format(*a.EPSSMax)
	}
	return ""
}

func lowerJoin(values []string) string { return strings.ToLower(strings.Join(values, ",")) }

// dependabotBinding lists the filter values the cursor binds; a request without these filters binds nothing.
func (a *alertArguments) dependabotBinding() []any {
	return []any{lowerJoin(a.Classification), lowerJoin(a.Ecosystem), strings.Join(a.Package, ","),
		strings.Join(a.Manifest, ","), a.epssQuery(), lowerJoin(a.Has), lowerJoin(a.Assignee),
		strings.ToLower(a.Scope), lowerJoin(a.Relationship), strings.ToLower(a.Sort), strings.ToLower(a.Direction)}
}

func (a *alertArguments) setDependabotQuery(query map[string][]string) {
	set := func(name, value string) {
		if value != "" {
			query[name] = []string{value}
		}
	}
	set("classification", lowerJoin(a.Classification))
	set("ecosystem", lowerJoin(a.Ecosystem))
	set("package", strings.Join(a.Package, ","))
	set("manifest", strings.Join(a.Manifest, ","))
	set("epss_percentage", a.epssQuery())
	set("has", lowerJoin(a.Has))
	set("assignee", lowerJoin(a.Assignee))
	set("scope", strings.ToLower(a.Scope))
	set("relationship", lowerJoin(a.Relationship))
	set("sort", strings.ToLower(a.Sort))
	set("direction", strings.ToLower(a.Direction))
}
