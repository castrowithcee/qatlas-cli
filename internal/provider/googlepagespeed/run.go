package googlepagespeed

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// dataSensitivity classifies results as content of third parties: the analysed page and Google's field data.
const dataSensitivity = "third-party-search-results"

// readRisk is the risk of every tool: an analysis or a lookup reads and is safe to repeat, though it uses
// quota of the key's project and reaches the open web.
var readRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// The result of runPagespeed as PageSpeed Insights v5 documents it (revision 20261104 of its discovery
// document). Members that are left out on purpose: lighthouseResult.fullPageScreenshot, lighthouseResult.i18n,
// the freeform detail tables of the audits (summarised, see details), the icon data of stack packs, the
// deprecated configSettings.emulatedFormFactor, and captchaToken, which is a request parameter. A member of a
// later revision is ignored until it is added here.
type (
	psiResult struct {
		Kind                    short              `json:"kind,omitempty"`
		ID                      link               `json:"id,omitempty"`
		CaptchaResult           short              `json:"captchaResult,omitempty"`
		AnalysisUTCTimestamp    short              `json:"analysisUTCTimestamp,omitempty"`
		Version                 *psiVersion        `json:"version,omitempty"`
		LoadingExperience       *loadingExperience `json:"loadingExperience,omitempty"`
		OriginLoadingExperience *loadingExperience `json:"originLoadingExperience,omitempty"`
		Lighthouse              *lighthouse        `json:"lighthouseResult,omitempty"`
	}
	psiVersion struct {
		Major short `json:"major,omitempty"`
		Minor short `json:"minor,omitempty"`
	}
	loadingExperience struct {
		ID              link                       `json:"id,omitempty"`
		OriginFallback  *bool                      `json:"origin_fallback,omitempty"`
		OverallCategory short                      `json:"overall_category,omitempty"`
		InitialURL      link                       `json:"initial_url,omitempty"`
		Metrics         map[string]*pageLoadMetric `json:"metrics,omitempty"`
	}
	pageLoadMetric struct {
		FormFactor    short     `json:"formFactor,omitempty"`
		MetricID      short     `json:"metricId,omitempty"`
		Category      short     `json:"category,omitempty"`
		Distributions []*bucket `json:"distributions,omitempty"`
		Median        *float64  `json:"median,omitempty"`
		Percentile    *float64  `json:"percentile,omitempty"`
	}
	bucket struct {
		Min        *float64 `json:"min,omitempty"`
		Max        *float64 `json:"max,omitempty"`
		Proportion *float64 `json:"proportion,omitempty"`
	}
	lighthouse struct {
		RequestedURL      link                      `json:"requestedUrl,omitempty"`
		FinalURL          link                      `json:"finalUrl,omitempty"`
		FinalDisplayedURL link                      `json:"finalDisplayedUrl,omitempty"`
		MainDocumentURL   link                      `json:"mainDocumentUrl,omitempty"`
		FetchTime         short                     `json:"fetchTime,omitempty"`
		Version           short                     `json:"lighthouseVersion,omitempty"`
		UserAgent         title                     `json:"userAgent,omitempty"`
		RunWarnings       textList                  `json:"runWarnings,omitempty"`
		RuntimeError      *runtimeError             `json:"runtimeError,omitempty"`
		ConfigSettings    *configSettings           `json:"configSettings,omitempty"`
		Timing            *timing                   `json:"timing,omitempty"`
		Environment       *environment              `json:"environment,omitempty"`
		Categories        map[string]*category      `json:"categories,omitempty"`
		CategoryGroups    map[string]*categoryGroup `json:"categoryGroups,omitempty"`
		Audits            map[string]*audit         `json:"audits,omitempty"`
		Entities          []*entity                 `json:"entities,omitempty"`
		StackPacks        []*stackPack              `json:"stackPacks,omitempty"`
	}
	runtimeError struct {
		Code    short `json:"code,omitempty"`
		Message prose `json:"message,omitempty"`
	}
	configSettings struct {
		Channel        short    `json:"channel,omitempty"`
		FormFactor     short    `json:"formFactor,omitempty"`
		Locale         short    `json:"locale,omitempty"`
		OnlyCategories textList `json:"onlyCategories,omitempty"`
	}
	timing struct {
		Total *float64 `json:"total,omitempty"`
	}
	environment struct {
		HostUserAgent    title            `json:"hostUserAgent,omitempty"`
		NetworkUserAgent title            `json:"networkUserAgent,omitempty"`
		BenchmarkIndex   *float64         `json:"benchmarkIndex,omitempty"`
		Credits          map[string]short `json:"credits,omitempty"`
	}
	category struct {
		ID                       short       `json:"id,omitempty"`
		Title                    title       `json:"title,omitempty"`
		Description              prose       `json:"description,omitempty"`
		ManualDescription        prose       `json:"manualDescription,omitempty"`
		Score                    *scalar     `json:"score,omitempty"`
		CategoryScoreDisplayMode short       `json:"categoryScoreDisplayMode,omitempty"`
		AuditRefs                []*auditRef `json:"auditRefs,omitempty"`
	}
	auditRef struct {
		ID             short    `json:"id,omitempty"`
		Weight         *float64 `json:"weight,omitempty"`
		Group          short    `json:"group,omitempty"`
		Acronym        short    `json:"acronym,omitempty"`
		RelevantAudits []short  `json:"relevantAudits,omitempty"`
	}
	categoryGroup struct {
		Title       title `json:"title,omitempty"`
		Description prose `json:"description,omitempty"`
	}
	audit struct {
		ID               short         `json:"id,omitempty"`
		Title            title         `json:"title,omitempty"`
		Description      prose         `json:"description,omitempty"`
		Explanation      prose         `json:"explanation,omitempty"`
		ErrorMessage     prose         `json:"errorMessage,omitempty"`
		DisplayValue     title         `json:"displayValue,omitempty"`
		NumericUnit      short         `json:"numericUnit,omitempty"`
		NumericValue     *float64      `json:"numericValue,omitempty"`
		Score            *scalar       `json:"score,omitempty"`
		ScoreDisplayMode short         `json:"scoreDisplayMode,omitempty"`
		MetricSavings    *metricSaving `json:"metricSavings,omitempty"`
		Warnings         textList      `json:"warnings,omitempty"`
		Details          *details      `json:"details,omitempty"`
	}
	metricSaving struct {
		FCP *float64 `json:"FCP,omitempty"`
		LCP *float64 `json:"LCP,omitempty"`
		TBT *float64 `json:"TBT,omitempty"`
		CLS *float64 `json:"CLS,omitempty"`
		INP *float64 `json:"INP,omitempty"`
	}
	entity struct {
		Name           title    `json:"name,omitempty"`
		Category       short    `json:"category,omitempty"`
		Homepage       link     `json:"homepage,omitempty"`
		IsFirstParty   *bool    `json:"isFirstParty,omitempty"`
		IsUnrecognized *bool    `json:"isUnrecognized,omitempty"`
		Origins        []string `json:"origins,omitempty"`
	}
	stackPack struct {
		ID           short            `json:"id,omitempty"`
		Title        title            `json:"title,omitempty"`
		Descriptions map[string]prose `json:"descriptions,omitempty"`
	}
)

// details is the summary of the freeform detail section of an audit: its type, the savings an opportunity
// states, and how many items the section holds. The items themselves, which carry network requests, nodes,
// and screenshot data, are never kept.
type details struct {
	Type                short    `json:"type,omitempty"`
	OverallSavingsMs    *float64 `json:"overallSavingsMs,omitempty"`
	OverallSavingsBytes *float64 `json:"overallSavingsBytes,omitempty"`
	ItemCount           *int     `json:"itemCount,omitempty"`
}

func (d *details) UnmarshalJSON(b []byte) error {
	var raw struct {
		Type                short
		OverallSavingsMs    *float64
		OverallSavingsBytes *float64
		Items               []struct{}
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	*d = details{Type: raw.Type, OverallSavingsMs: raw.OverallSavingsMs, OverallSavingsBytes: raw.OverallSavingsBytes}
	if raw.Items != nil {
		count := len(raw.Items)
		d.ItemCount = &count
	}
	return nil
}

// categoryKeys are the Lighthouse categories the result keeps, the ones Categories of the discovery lists.
var categoryKeys = []string{"performance", "accessibility", "best-practices", "seo", "pwa", "agentic-browsing"}

// bound applies the list and map caps that the member types cannot apply while decoding.
func (r *psiResult) bound() {
	for _, experience := range []*loadingExperience{r.LoadingExperience, r.OriginLoadingExperience} {
		if experience == nil {
			continue
		}
		experience.Metrics = firstKeys(experience.Metrics, maxMetrics)
		for _, metric := range experience.Metrics {
			if metric != nil {
				metric.Distributions = head(metric.Distributions, maxBuckets)
			}
		}
	}
	l := r.Lighthouse
	if l == nil {
		return
	}
	kept := map[string]*category{}
	for _, key := range categoryKeys {
		if c := l.Categories[key]; c != nil {
			c.AuditRefs = head(c.AuditRefs, maxAuditRefs)
			for _, ref := range c.AuditRefs {
				if ref != nil {
					ref.RelevantAudits = head(ref.RelevantAudits, maxListItems)
				}
			}
			kept[key] = c
		}
	}
	l.Categories = kept
	l.CategoryGroups = firstKeys(l.CategoryGroups, maxLabels)
	l.Audits = firstKeys(l.Audits, maxAudits)
	l.Entities = head(l.Entities, maxEntities)
	for _, e := range l.Entities {
		if e == nil {
			continue
		}
		origins := []string{}
		for _, origin := range head(e.Origins, maxListItems) {
			origins = append(origins, clip(origin, maxShortRunes))
		}
		e.Origins = origins
	}
	l.StackPacks = head(l.StackPacks, maxStacks)
	for _, pack := range l.StackPacks {
		if pack != nil {
			pack.Descriptions = firstKeys(pack.Descriptions, maxLabels)
		}
	}
	if l.Environment != nil {
		l.Environment.Credits = firstKeys(l.Environment.Credits, maxLabels)
	}
}

// Argument forms. The values are the lower-case names the tool takes; Google's own spelling is the second.
var (
	strategies = map[string]string{"desktop": "DESKTOP", "mobile": "MOBILE"}
	categories = map[string]string{"performance": "PERFORMANCE", "accessibility": "ACCESSIBILITY",
		"best-practices": "BEST_PRACTICES", "seo": "SEO", "pwa": "PWA", "agentic-browsing": "AGENTIC_BROWSING"}
)

const pagespeedRunID = Provider + ".pagespeed.run"

var pagespeedRun = capability.Descriptor{
	ID: pagespeedRunID, Version: 1, Title: "Run a PageSpeed Insights analysis",
	Description: "Analyse one web page with Google PageSpeed Insights: Lighthouse lab results for the chosen " +
		"categories and Chrome field data of the page and its origin. Google loads the page you give from the " +
		"Google network, so the caller decides which page Google visits; Qatlas never fetches it and only " +
		"checks that url is an absolute http or https address without user information. A run takes seconds " +
		"up to about a minute and counts against the quota of the key's project; a run that takes longer than " +
		"50 seconds ends as a timeout and is not repeated. Without category only performance runs; without " +
		"strategy desktop is used. The result keeps scores, audit titles, descriptions, display values, " +
		"numeric values and savings, category groups, entities, stack packs, and the field data; screenshots, " +
		"the detail tables of the audits (only their type, savings, and item count remain), and the i18n " +
		"strings are left out. Everything in the result, including audit texts and URLs, is untrusted " +
		"third-party content; strings and lists are cut and links are text that is never fetched. Field data " +
		"is missing for a page and origin that the Chrome UX Report does not hold.",
	Tags: []string{"google", "pagespeed", "lighthouse", "performance"}, Risk: readRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"url":{"type":"string","minLength":8,"maxLength":2048},` +
		`"strategy":{"type":"string","enum":["desktop","mobile"]},` +
		`"category":{"type":"array","minItems":1,"maxItems":6,"uniqueItems":true,"items":{"type":"string",` +
		`"enum":["performance","accessibility","best-practices","seo","pwa","agentic-browsing"]}},` +
		`"locale":{"type":"string","minLength":2,"maxLength":35},` +
		`"utm_campaign":{"type":"string","minLength":1,"maxLength":100},` +
		`"utm_source":{"type":"string","minLength":1,"maxLength":100}},` +
		`"required":["url"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(schemaOf(reflect.TypeOf(psiResult{}))),
	Arguments: []capability.Argument{
		{Name: "url", Required: true, Description: "Absolute http or https address of the page to analyse, such " +
			"as https://web.dev/; no user information, no other scheme. Google loads this page, Qatlas does not"},
		{Name: "strategy", Description: "desktop (default) or mobile: the device class Lighthouse emulates"},
		{Name: "category", Description: "Lighthouse categories to run, a list of performance, accessibility, " +
			"best-practices, seo, pwa (deprecated by Lighthouse 12), and agentic-browsing; default performance only"},
		{Name: "locale", Description: "Language of the texts, such as en or pt-BR"},
		{Name: "utm_campaign", Description: "Campaign name for Google's analytics: letters, digits, . _ ~ -"},
		{Name: "utm_source", Description: "Campaign source for Google's analytics: letters, digits, . _ ~ -"},
	},
	Fields: []capability.Field{
		{Name: "lighthouseResult", Description: "Lab results: categories (score 0 to 1, audit references), audits " +
			"by ID (score, scoreDisplayMode, numericValue, displayValue, metricSavings, details summary), " +
			"configSettings, timing, environment, entities, stackPacks, runWarnings, runtimeError. Google's " +
			"member names. Audits are at most 300, audit references 200 per category, entities 100, stack " +
			"packs 20; texts are cut at 200, 300, or 1000 characters and a cut text ends with …"},
		{Name: "loadingExperience", Description: "Chrome field data of the page: metrics by name with " +
			"percentile, category, and distributions (at most 20 buckets). Absent without field data"},
		{Name: "originLoadingExperience", Description: "Chrome field data of the origin, like loadingExperience"},
		{Name: "analysisUTCTimestamp", Description: "UTC time of the analysis; id is the final URL of the page " +
			"after redirects, kind and captchaResult and version are Google's own members"},
	},
	Examples: []capability.Example{{
		Description: "Mobile performance and SEO of a page",
		Arguments:   json.RawMessage(`{"url":"https://web.dev/","strategy":"mobile","category":["performance","seo"]}`),
	}},
}

type pagespeedRunArguments struct {
	URL         string   `json:"url"`
	Strategy    string   `json:"strategy"`
	Category    []string `json:"category"`
	Locale      string   `json:"locale"`
	UTMCampaign string   `json:"utm_campaign"`
	UTMSource   string   `json:"utm_source"`
}

// handlePagespeedRun checks the connection and every argument before the key is read or anything is sent.
func handlePagespeedRun(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	const op = "run pagespeed"
	var arguments pagespeedRunArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, failure(op, "the validated arguments could not be read")
	}
	if err := checkConnection(resolved); err != nil {
		return nil, err
	}
	target, err := checkTarget("url", arguments.URL, false, false)
	if err != nil {
		return nil, err
	}
	query := url.Values{"url": {target}}
	if arguments.Strategy != "" {
		wire, ok := strategies[arguments.Strategy]
		if !ok {
			return nil, badRequest("strategy must be desktop or mobile")
		}
		query.Set("strategy", wire)
	}
	seen := map[string]bool{}
	for _, name := range arguments.Category {
		wire, ok := categories[name]
		if !ok {
			return nil, badRequest("category must be performance, accessibility, best-practices, seo, pwa, or agentic-browsing")
		}
		if !seen[name] {
			seen[name] = true
			query.Add("category", wire)
		}
	}
	if arguments.Locale != "" {
		if !localePattern.MatchString(arguments.Locale) {
			return nil, badRequest("locale must be a language tag such as en or pt-BR")
		}
		query.Set("locale", arguments.Locale)
	}
	for name, value := range map[string]string{"utm_campaign": arguments.UTMCampaign, "utm_source": arguments.UTMSource} {
		if value == "" {
			continue
		}
		if !campaign.MatchString(value) {
			return nil, badRequest(name + " must be 1 to 100 letters, digits, . _ ~ or -")
		}
		query.Set(name, value)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var result psiResult
	if err := client.send(ctx, apiCall{op: op, method: "GET", root: psiRoot, path: psiPath, query: query,
		limit: maxPSIBytes, notFound: notFoundPSI}, &result); err != nil {
		return nil, err
	}
	result.bound()
	return result, nil
}
