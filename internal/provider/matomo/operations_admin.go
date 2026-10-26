package matomo

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	minArchiveTTL      = 60
	maxArchiveTTL      = 86400
	maxTrustedHosts    = 50
	maxArchiveRangeDay = 366
	maxArchiveIDs      = 100
)

var (
	hostLabel     = `[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?`
	hostnameForm  = regexp.MustCompile(`^` + hostLabel + `(\.` + hostLabel + `)*$`)
	archiveDay    = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|today|yesterday)$`)
	archivePeriod = []string{"day", "week", "month", "year", "range"}

	pluginName = regexp.MustCompile(pluginPattern)

	operationsSentOnce = "; sent once and never repeated by Qatlas: after an unclear result, check the state in " +
		"Matomo before repeating it"
)

// operationsRisk is the risk of the operations tools: confirmed, open to the instance, and about instance
// metadata; the archiving is a repeatable execution.
func operationsRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: instanceSensitive}
}

const operationsNote = " It needs Super User rights and a connection whose targets name the instance, and it applies " +
	"to the whole instance"

var archiveSettingsSet = capability.Descriptor{
	ID: Provider + ".archive-settings.set", Version: 1, Title: "Set the archiving settings of Matomo",
	Description: "Set whether visits in the browser trigger the archiving of reports and how long the report data of " +
		"today stays valid (CoreAdminHome.setArchiveSettings). Both values are sent every time. Turning the browser " +
		"trigger off is right only where a cron job runs the archiving; otherwise reports stop updating. A value " +
		"fixed in Matomo's configuration file can override the stored one, and an instance that disables the general " +
		"settings in the interface refuses the change." + operationsNote + operationsSentOnce,
	Tags: []string{"matomo", "archiving", "settings", "update", "instance"}, Risk: operationsRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"enableBrowserTriggerArchiving":{"type":"boolean"},` +
		`"todayArchiveTimeToLive":{"type":"integer","minimum":` + strconv.Itoa(minArchiveTTL) + `,"maximum":` +
		strconv.Itoa(maxArchiveTTL) + `}},"required":["enableBrowserTriggerArchiving","todayArchiveTimeToLive"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"enableBrowserTriggerArchiving":{"type":"boolean"},` +
		`"todayArchiveTimeToLive":{"type":"integer"},"updated":{"type":"boolean"}},` +
		`"required":["enableBrowserTriggerArchiving","todayArchiveTimeToLive","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "enableBrowserTriggerArchiving", Required: true, Description: "True lets visitors' browsers trigger archiving"},
		{Name: "todayArchiveTimeToLive", Required: true, Description: "Seconds the reports of today stay valid, 60 to 86400"},
	},
	Fields: []capability.Field{
		{Name: "enableBrowserTriggerArchiving", Description: "The value that was sent"},
		{Name: "todayArchiveTimeToLive", Description: "The value that was sent"},
		{Name: "updated", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Hand the archiving to cron and refresh today every ten minutes",
		Arguments: json.RawMessage(`{"enableBrowserTriggerArchiving":false,"todayArchiveTimeToLive":600}`)}},
}

var trustedHostsSet = capability.Descriptor{
	ID: Provider + ".trusted-hosts.set", Version: 1, Title: "Set the trusted hosts of Matomo",
	Description: "WARNING: replace the complete list of host names under which Matomo accepts requests " +
		"(CoreAdminHome.setTrustedHosts). Matomo refuses every request whose host is not on the list, so a wrong list " +
		"locks everybody out of the instance, this connection included, and only a change in Matomo's configuration " +
		"file repairs it. Qatlas therefore refuses a list that lacks the host of the connection's base URL (the host " +
		"with its port when the URL names one other than 443) and sends nothing; entries must be host names, IPv4 " +
		"addresses, or bracketed IPv6 addresses, each with an optional port. A list is never empty, because Matomo " +
		"ignores an empty one." + operationsNote + "; offered only where a connection's tools list names it" +
		operationsSentOnce,
	Tags: []string{"matomo", "trusted-hosts", "security", "update", "instance"}, Risk: operationsRisk(capability.EffectUpdate),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"trustedHosts":{"type":"array","minItems":1,` +
		`"maxItems":` + strconv.Itoa(maxTrustedHosts) + `,"items":{"type":"string","minLength":1,"maxLength":260}}},` +
		`"required":["trustedHosts"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"trustedHosts":{"type":"array","items":{"type":"string"}},` +
		`"updated":{"type":"boolean"}},"required":["trustedHosts","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "trustedHosts", Required: true, Description: "The complete list, 1 to 50 " +
		"entries such as analytics.example.test or analytics.example.test:8443; it must contain the host of the " +
		"connection's base URL. Matomo also accepts subdomains of an entry"}},
	Fields: []capability.Field{
		{Name: "trustedHosts", Description: "The complete list that was sent, in lower case without duplicates"},
		{Name: "updated", Description: "True once Matomo accepted the change"},
	},
	Examples: []capability.Example{{Description: "Replace the list",
		Arguments: json.RawMessage(`{"trustedHosts":["analytics.example.test","matomo.example.test"]}`)}},
}

var brandingSet = capability.Descriptor{
	ID: Provider + ".branding.set", Version: 1, Title: "Switch the custom branding of Matomo",
	Description: "Switch the custom logo and favicon of the instance on or off (CoreAdminHome.setBrandingSettings). " +
		"Only the switches are set; no file is uploaded, and a logo or favicon is published only if an upload " +
		"waits in Matomo already. useCustomLogo false, or true with neither hasCustomLogo nor hasCustomFavicon, " +
		"deletes the uploaded logo files and turns the branding off, which cannot be undone." + operationsNote +
		operationsSentOnce,
	Tags: []string{"matomo", "branding", "logo", "update", "instance"}, Risk: operationsRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"useCustomLogo":{"type":"boolean"},` +
		`"hasCustomLogo":{"type":"boolean"},"hasCustomFavicon":{"type":"boolean"}},"required":["useCustomLogo"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"useCustomLogo":{"type":"boolean"},` +
		`"customLogoPath":{"type":"string"},"customFaviconPath":{"type":"string"}},"required":["useCustomLogo"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "useCustomLogo", Required: true, Description: "True turns the custom branding on, false off and deletes the logo files"},
		{Name: "hasCustomLogo", Description: "True publishes the waiting logo upload; false when omitted"},
		{Name: "hasCustomFavicon", Description: "True publishes the waiting favicon upload; false when omitted"},
	},
	Fields: []capability.Field{
		{Name: "useCustomLogo", Description: "Whether the custom branding is on afterwards"},
		{Name: "customLogoPath", Description: "Path of the published logo, only when one was published, untrusted"},
		{Name: "customFaviconPath", Description: "Path of the published favicon, only when one was published, untrusted"},
	},
	Examples: []capability.Example{{Description: "Turn the custom branding on",
		Arguments: json.RawMessage(`{"useCustomLogo":true,"hasCustomLogo":true}`)}},
}

var reportsArchive = capability.Descriptor{
	ID: Provider + ".reports.archive", Version: 1, Title: "Archive the reports of a Matomo website",
	Description: "Start the archiving of the reports of one website for one period (CoreAdminHome.archiveReports) " +
		"and wait for it. It computes and stores report data, can take long on a large website and may time out; a " +
		"timeout leaves the archiving state unknown. Matomo requires Super User rights for this call, so it needs a " +
		"connection whose targets name the instance and a website inside its site targets. period day, week, month, " +
		"or year takes one date; period range takes two dates of at most 366 days. plugin and report narrow the " +
		"work; segment archives for that segment." + operationsSentOnce,
	Tags: []string{"matomo", "archiving", "reports", "execute", "instance"}, Risk: operationsRisk(capability.EffectExecute),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer","minimum":1,"maximum":999999999},` +
		`"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `},` +
		`"plugin":{"type":"string","pattern":"` + pluginPattern + `","maxLength":64},` +
		`"report":{"type":"string","pattern":"` + namePattern.String() + `","maxLength":64}},` +
		`"required":["period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"period":{"type":"string"},` +
		`"date":{"type":"string"},"archived":{"type":"boolean"},"idarchives":{"type":"array","items":{"type":"integer"}},` +
		`"nb_visits":{"type":"integer"}},"required":["idSite","period","date","archived"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSite", Target: "site", Description: "Website ID from matomo.sites.list, always required here; it must lie " +
			"inside the connection's site targets"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "YYYY-MM-DD, today, or yesterday; for range two dates " +
			"YYYY-MM-DD,YYYY-MM-DD"},
		{Name: "segment", Description: "Segment expression as for matomo.reports.get; all visits when omitted"},
		{Name: "plugin", Description: "Plugin whose reports are archived, such as Actions; all plugins when omitted"},
		{Name: "report", Description: "Report to archive within the plugin, a unique report ID; all when omitted"},
	},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website that was archived"},
		{Name: "period", Description: "The period that was sent"},
		{Name: "date", Description: "The date that was sent"},
		{Name: "archived", Description: "True when Matomo answered with archives, false when it answered with none"},
		{Name: "idarchives", Description: "IDs of the archives that serve the period"},
		{Name: "nb_visits", Description: "Number of visits Matomo counted in the archive"},
	},
	Examples: []capability.Example{{Description: "Archive yesterday of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"period":"day","date":"yesterday"}`)}},
}

// ArchiveSettingsSet is the answer of archive-settings.set.
type ArchiveSettingsSet struct {
	EnableBrowserTriggerArchiving bool `json:"enableBrowserTriggerArchiving"`
	TodayArchiveTimeToLive        int  `json:"todayArchiveTimeToLive"`
	Updated                       bool `json:"updated"`
}

// TrustedHostsSet is the answer of trusted-hosts.set.
type TrustedHostsSet struct {
	TrustedHosts []string `json:"trustedHosts"`
	Updated      bool     `json:"updated"`
}

// BrandingSet is the answer of branding.set.
type BrandingSet struct {
	UseCustomLogo     bool   `json:"useCustomLogo"`
	CustomLogoPath    string `json:"customLogoPath,omitempty"`
	CustomFaviconPath string `json:"customFaviconPath,omitempty"`
}

// ReportsArchived is the answer of reports.archive.
type ReportsArchived struct {
	IDSite     int    `json:"idSite"`
	Period     string `json:"period"`
	Date       string `json:"date"`
	Archived   bool   `json:"archived"`
	IDArchives []int  `json:"idarchives,omitempty"`
	NbVisits   *int   `json:"nb_visits,omitempty"`
}

// onlyKeys refuses a key outside allowed before anything else happens.
func onlyKeys(raw json.RawMessage, allowed ...string) error {
	var keys map[string]json.RawMessage
	if len(raw) > 0 && json.Unmarshal(raw, &keys) != nil {
		return invalidArgument("the arguments could not be read")
	}
	for key := range keys {
		if !slices.Contains(allowed, key) {
			return invalidArgument("an argument is not accepted by this tool")
		}
	}
	return nil
}

func invokeArchiveSettingsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set archive settings"
	var args struct {
		Enable *bool `json:"enableBrowserTriggerArchiving"`
		TTL    *int  `json:"todayArchiveTimeToLive"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := onlyKeys(raw, "enableBrowserTriggerArchiving", "todayArchiveTimeToLive"); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.Enable == nil {
		return nil, invalidArgument("enableBrowserTriggerArchiving is required and must be true or false")
	}
	if args.TTL == nil || *args.TTL < minArchiveTTL || *args.TTL > maxArchiveTTL {
		return nil, invalidArgument("todayArchiveTimeToLive must be 60 to 86400 seconds")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"enableBrowserTriggerArchiving": {flagValue(args.Enable)},
		"todayArchiveTimeToLive": {strconv.Itoa(*args.TTL)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, baseRequirement(archiveSettingsSet.ID), "CoreAdminHome.setArchiveSettings", params,
		operationsSentOnce, &answer); err != nil {
		return nil, err
	}
	if !confirmedByMatomo(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the change", operationsSentOnce)
	}
	return &ArchiveSettingsSet{EnableBrowserTriggerArchiving: *args.Enable, TodayArchiveTimeToLive: *args.TTL,
		Updated: true}, nil
}

// trustedHostEntry checks one entry and returns it in lower case: a host name, an IPv4 address, or a bracketed
// IPv6 address, with an optional port. Matomo checks nothing here, so the form is narrow on purpose.
func trustedHostEntry(entry string) (string, bool) {
	entry = strings.ToLower(strings.TrimSpace(entry))
	host, port := entry, ""
	if strings.HasPrefix(entry, "[") {
		end := strings.Index(entry, "]")
		if end < 0 {
			return "", false
		}
		addr, err := netip.ParseAddr(entry[1:end])
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", false
		}
		host, port = entry[:end+1], entry[end+1:]
		if port != "" && port[0] != ':' {
			return "", false
		}
		port = strings.TrimPrefix(port, ":")
		if strings.HasSuffix(entry, ":") {
			return "", false
		}
	} else if name, rest, found := strings.Cut(entry, ":"); found {
		if rest == "" {
			return "", false
		}
		host, port = name, rest
	}
	if !strings.HasPrefix(host, "[") && (len(host) > 253 || !hostnameForm.MatchString(host)) {
		return "", false
	}
	if port != "" {
		number, valid := parseID(port)
		if !valid || number > 65535 {
			return "", false
		}
	}
	return entry, true
}

// baseURLHosts are the entries of the trusted list that keep this connection reachable.
func baseURLHosts(base string) []string {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Hostname() == "" {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	switch parsed.Port() {
	case "":
		return []string{host}
	case "443":
		return []string{host, host + ":443"}
	}
	return []string{host + ":" + parsed.Port()}
}

func invokeTrustedHostsSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set trusted hosts"
	var args struct {
		TrustedHosts []string `json:"trustedHosts"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := onlyKeys(raw, "trustedHosts"); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if len(args.TrustedHosts) == 0 || len(args.TrustedHosts) > maxTrustedHosts {
		return nil, invalidArgument("trustedHosts must hold 1 to 50 entries")
	}
	list := make([]string, 0, len(args.TrustedHosts))
	for _, entry := range args.TrustedHosts {
		clean, valid := trustedHostEntry(entry)
		if !valid {
			return nil, invalidArgument("an entry of trustedHosts is not a host name, IP address, or host:port")
		}
		if !slices.Contains(list, clean) {
			list = append(list, clean)
		}
	}
	need := baseURLHosts(resolved.BaseURL)
	if len(need) == 0 || !slices.ContainsFunc(need, func(host string) bool { return slices.Contains(list, host) }) {
		return nil, invalidArgument("trustedHosts must contain the host of the connection's base URL, or the " +
			"connection would be locked out")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Matomo asks for the password only for a session token, never for the personal token used here.
	params := url.Values{"trustedHosts[]": list}
	var answer json.RawMessage
	if err := client.change(ctx, op, baseRequirement(trustedHostsSet.ID), "CoreAdminHome.setTrustedHosts", params,
		operationsSentOnce, &answer); err != nil {
		return nil, err
	}
	if !confirmedByMatomo(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the change", operationsSentOnce)
	}
	return &TrustedHostsSet{TrustedHosts: list, Updated: true}, nil
}

func invokeBrandingSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set branding"
	var args struct {
		UseCustomLogo    *bool `json:"useCustomLogo"`
		HasCustomLogo    *bool `json:"hasCustomLogo"`
		HasCustomFavicon *bool `json:"hasCustomFavicon"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := onlyKeys(raw, "useCustomLogo", "hasCustomLogo", "hasCustomFavicon"); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.UseCustomLogo == nil {
		return nil, invalidArgument("useCustomLogo is required and must be true or false")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"useCustomLogo": {flagValue(args.UseCustomLogo)}, "hasCustomLogo": {flagValue(args.HasCustomLogo)},
		"hasCustomFavicon": {flagValue(args.HasCustomFavicon)}}
	var answer struct {
		UseCustomLogo     *bool  `json:"useCustomLogo"`
		CustomLogoPath    string `json:"customLogoPath"`
		CustomFaviconPath string `json:"customFaviconPath"`
	}
	if err := client.change(ctx, op, baseRequirement(brandingSet.ID), "CoreAdminHome.setBrandingSettings", params,
		operationsSentOnce, &answer); err != nil {
		return nil, err
	}
	if answer.UseCustomLogo == nil {
		return nil, unclearChange(op, "Matomo returned no readable branding state", operationsSentOnce)
	}
	return &BrandingSet{UseCustomLogo: *answer.UseCustomLogo, CustomLogoPath: bounded(answer.CustomLogoPath),
		CustomFaviconPath: bounded(answer.CustomFaviconPath)}, nil
}

// checkArchiveDate accepts one date for day, week, month, and year and two for range; lastN and previousN
// would archive an open number of periods and are refused.
func checkArchiveDate(period, date string) error {
	if !slices.Contains(archivePeriod, period) {
		return invalidArgument("period must be day, week, month, year, or range")
	}
	if period != "range" {
		if !archiveDay.MatchString(date) {
			return invalidArgument("date must be YYYY-MM-DD, today, or yesterday")
		}
		if date[0] >= '0' && date[0] <= '9' {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return invalidArgument("date must be a valid day")
			}
		}
		return nil
	}
	first, last, found := strings.Cut(date, ",")
	from, errFrom := time.Parse("2006-01-02", first)
	to, errTo := time.Parse("2006-01-02", last)
	if !found || !singleDate.MatchString(first) || !singleDate.MatchString(last) || errFrom != nil || errTo != nil ||
		from.After(to) || to.Sub(from) > time.Duration(maxArchiveRangeDay-1)*24*time.Hour {
		return invalidArgument("a range needs two valid dates YYYY-MM-DD,YYYY-MM-DD, the first not after the second, " +
			"at most 366 days apart")
	}
	return nil
}

// archiveResult reads the answer of archiveReports: an object with the archive IDs and the visit count, or the
// empty forms PHP gives when there is nothing to archive.
func archiveResult(answer json.RawMessage) (ids []int, visits *int, archived, ok bool) {
	switch strings.TrimSpace(string(answer)) {
	case "[]", "null", "false", "{}":
		return nil, nil, false, true
	}
	var object struct {
		IDArchives json.RawMessage `json:"idarchives"`
		NbVisits   json.RawMessage `json:"nb_visits"`
	}
	if json.Unmarshal(answer, &object) != nil || object.IDArchives == nil {
		return nil, nil, false, false
	}
	var one int
	var many []int
	switch {
	case json.Unmarshal(object.IDArchives, &many) == nil:
		ids = many
	case json.Unmarshal(object.IDArchives, &one) == nil:
		ids = []int{one}
	default:
		return nil, nil, false, false
	}
	if len(ids) > maxArchiveIDs {
		ids = ids[:maxArchiveIDs]
	}
	var count int
	if object.NbVisits != nil && json.Unmarshal(object.NbVisits, &count) == nil && count >= 0 {
		visits = &count
	}
	return ids, visits, len(ids) > 0, true
}

func invokeReportsArchive(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "archive reports"
	var args struct {
		IDSite  *int    `json:"idSite"`
		Period  *string `json:"period"`
		Date    *string `json:"date"`
		Segment string  `json:"segment"`
		Plugin  string  `json:"plugin"`
		Report  string  `json:"report"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := onlyKeys(raw, "idSite", "period", "date", "segment", "plugin", "report"); err != nil {
		return nil, err
	}
	if args.IDSite == nil {
		return nil, invalidArgument("idSite is required")
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	if args.Period == nil || args.Date == nil {
		return nil, invalidArgument("period and date are required")
	}
	if err := checkArchiveDate(*args.Period, *args.Date); err != nil {
		return nil, err
	}
	if args.Segment != "" && !validSegmentExpression(args.Segment) {
		return nil, invalidArgument("segment is not a valid segment expression")
	}
	if args.Plugin != "" && !pluginName.MatchString(args.Plugin) {
		return nil, invalidArgument("plugin must be a plugin name")
	}
	if args.Report != "" && (len(args.Report) > 64 || !namePattern.MatchString(args.Report)) {
		return nil, invalidArgument("report must be a report ID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "period": {*args.Period}, "date": {*args.Date}}
	for name, value := range map[string]string{"segment": args.Segment, "plugin": args.Plugin, "report": args.Report} {
		if value != "" {
			params.Set(name, value)
		}
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, baseRequirement(reportsArchive.ID), "CoreAdminHome.archiveReports", params,
		operationsSentOnce, &answer); err != nil {
		return nil, err
	}
	ids, visits, archived, readable := archiveResult(answer)
	if !readable {
		return nil, unclearChange(op, "Matomo returned no readable archiving result", operationsSentOnce)
	}
	return &ReportsArchived{IDSite: id, Period: *args.Period, Date: *args.Date, Archived: archived, IDArchives: ids,
		NbVisits: visits}, nil
}

func operationsAdminOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: archiveSettingsSet, Handler: capability.Handler(invokeArchiveSettingsSet)},
		{Descriptor: trustedHostsSet, Handler: capability.Handler(invokeTrustedHostsSet)},
		{Descriptor: brandingSet, Handler: capability.Handler(invokeBrandingSet)},
		{Descriptor: reportsArchive, Handler: capability.Handler(invokeReportsArchive)},
	}
}
