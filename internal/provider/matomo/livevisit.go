package matomo

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	maxItemDetails      = 50
	maxCategories       = 5
	maxCustomDimensions = 30
	maxFlatIndex        = 10000
)

// row is one object of a Matomo answer whose members are read one by one: nothing in it is trusted, and a
// member of another shape than expected reads as absent.
type row map[string]json.RawMessage

func (r row) get(key string) *loose {
	raw, found := r[key]
	if !found {
		return nil
	}
	var value loose
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}

// text reads a text member. Matomo reports a missing text as the flag false, which reads as empty; a text
// "false" is a string and stays.
func (r row) text(key string, limit int) string {
	if string(r[key]) == "false" {
		return ""
	}
	return r.get(key).str(limit)
}

func (r row) integer(key string) *int { return r.get(key).number() }

// kind is how a member of a visit or an action is read.
type kind int

const (
	kText kind = iota
	kURL
	kInt
	kFlag
)

// member maps one member of the Live answer to one member of the result. Members that only present another
// member (icons, pretty dates and durations, rendered titles) have no entry.
type member struct {
	from, to string
	kind     kind
	doc      string
}

func (m member) read(r row) (any, bool) {
	switch m.kind {
	case kInt:
		if n := r.integer(m.from); n != nil {
			return *n, true
		}
	case kFlag:
		if f := r.get(m.from).flag(); f != nil {
			return *f, true
		}
	case kURL:
		if t := r.text(m.from, maxUrlLength); t != "" {
			return t, true
		}
	default:
		if t := r.text(m.from, maxStringLength); t != "" {
			return t, true
		}
	}
	return nil, false
}

func readMembers(r row, members []member, into map[string]any) {
	for _, m := range members {
		if value, ok := m.read(r); ok {
			into[m.to] = value
		}
	}
}

// visitMembers are the members the plugins delivered with Matomo 5 add to a visit (Live, CoreHome, Actions,
// Events, Goals, Ecommerce, Referrers, UserCountry, DevicesDetection, DevicePlugins, Resolution,
// UserLanguage, VisitTime, VisitorInterest). Personal data are marked in the description.
var visitMembers = []member{
	{"idVisit", "id_visit", kInt, "Visit ID"},
	{"idSite", "id_site", kInt, "Website ID"},
	{"siteName", "site_name", kText, "Website name, untrusted"},
	{"siteCurrency", "site_currency", kText, "Currency code of the website"},
	{"siteCurrencySymbol", "site_currency_symbol", kText, "Currency symbol of the website"},
	{"visitIp", "visit_ip", kText, "IP address of the visitor, personal data; Matomo may anonymize it"},
	{"visitorId", "visitor_id", kText, "Visitor ID, personal data"},
	{"fingerprint", "fingerprint", kText, "Device fingerprint hash, personal data"},
	{"userId", "user_id", kText, "User ID the website assigned to the visitor, personal data, untrusted"},
	{"visitorType", "visitor_type", kText, "new, returning, or returningCustomer"},
	{"visitCount", "visit_count", kInt, "Number of this visit of the visitor"},
	{"visitDuration", "visit_duration", kInt, "Duration of the visit in seconds"},
	{"visitConverted", "visit_converted", kFlag, "True when the visit converted a goal"},
	{"goalConversions", "goal_conversions", kInt, "Goal conversions of the visit"},
	{"visitEcommerceStatus", "ecommerce_status", kText, "Ecommerce state of the visit as Matomo reports it"},
	{"actions", "action_count", kInt, "Actions of the visit"},
	{"searches", "search_count", kInt, "Site searches of the visit"},
	{"events", "event_count", kInt, "Events of the visit"},
	{"interactions", "interaction_count", kInt, "Interactions of the visit"},
	{"firstActionTimestamp", "first_action_timestamp", kInt, "Unix time of the first action"},
	{"lastActionTimestamp", "last_action_timestamp", kInt, "Unix time of the last action"},
	{"lastActionDateTime", "last_action_datetime", kText, "Date and time of the last action as Matomo reports it (UTC)"},
	{"serverDate", "server_date", kText, "Date of the last action on the server"},
	{"visitServerHour", "visit_server_hour", kInt, "Server hour of the last action"},
	{"visitLocalTime", "visit_local_time", kText, "Local time of the visitor, personal data"},
	{"visitLocalHour", "visit_local_hour", kInt, "Local hour of the visitor, personal data"},
	{"daysSinceFirstVisit", "days_since_first_visit", kInt, "Days since the first visit of the visitor"},
	{"secondsSinceFirstVisit", "seconds_since_first_visit", kInt, "Seconds since the first visit of the visitor"},
	{"daysSinceLastVisit", "days_since_last_visit", kInt, "Days since the previous visit of the visitor"},
	{"secondsSinceLastVisit", "seconds_since_last_visit", kInt, "Seconds since the previous visit of the visitor"},
	{"daysSinceLastEcommerceOrder", "days_since_last_ecommerce_order", kInt, "Days since the last ecommerce order of the visitor"},
	{"secondsSinceLastEcommerceOrder", "seconds_since_last_ecommerce_order", kInt, "Seconds since the last ecommerce order of the visitor"},
	{"totalEcommerceRevenue", "total_ecommerce_revenue", kText, "Lifetime ecommerce revenue of the visitor, as Matomo reports it"},
	{"totalEcommerceConversions", "total_ecommerce_conversions", kInt, "Lifetime ecommerce orders of the visitor"},
	{"totalEcommerceItems", "total_ecommerce_items", kInt, "Lifetime ordered items of the visitor"},
	{"totalAbandonedCartsRevenue", "total_abandoned_carts_revenue", kText, "Lifetime revenue of the abandoned carts of the visitor"},
	{"totalAbandonedCarts", "total_abandoned_carts", kInt, "Abandoned carts of the visitor"},
	{"totalAbandonedCartsItems", "total_abandoned_carts_items", kInt, "Items in the abandoned carts of the visitor"},
	{"referrerType", "referrer_type", kText, "Kind of referrer, such as direct, search, website, or campaign"},
	{"referrerTypeName", "referrer_type_name", kText, "Name of the referrer kind"},
	{"referrerName", "referrer_name", kText, "Referrer name, untrusted"},
	{"referrerKeyword", "referrer_keyword", kText, "Search keyword of the referrer, untrusted"},
	{"referrerKeywordPosition", "referrer_keyword_position", kText, "Position of the page in the search results, as Matomo reports it"},
	{"referrerUrl", "referrer_url", kURL, "Referrer address, untrusted text that is never fetched"},
	{"referrerSearchEngineUrl", "referrer_search_engine_url", kURL, "Main address of the search engine, untrusted text that is never fetched"},
	{"referrerSocialNetworkUrl", "referrer_social_network_url", kURL, "Main address of the social network, untrusted text that is never fetched"},
	{"referrerAIAssistantUrl", "referrer_ai_assistant_url", kURL, "Main address of the AI assistant, untrusted text that is never fetched"},
	{"continent", "continent", kText, "Continent of the visitor, personal data"},
	{"continentCode", "continent_code", kText, "Continent code of the visitor, personal data"},
	{"country", "country", kText, "Country of the visitor, personal data"},
	{"countryCode", "country_code", kText, "Country code of the visitor, personal data"},
	{"region", "region", kText, "Region of the visitor, personal data"},
	{"regionCode", "region_code", kText, "Region code of the visitor, personal data"},
	{"city", "city", kText, "City of the visitor, personal data"},
	{"location", "location", kText, "Location of the visitor as one text, personal data"},
	{"latitude", "latitude", kText, "Latitude of the visitor, personal data"},
	{"longitude", "longitude", kText, "Longitude of the visitor, personal data"},
	{"deviceType", "device_type", kText, "Device type, such as smartphone or desktop"},
	{"deviceBrand", "device_brand", kText, "Device brand"},
	{"deviceModel", "device_model", kText, "Device model"},
	{"operatingSystem", "operating_system", kText, "Operating system with version"},
	{"operatingSystemName", "operating_system_name", kText, "Operating system name"},
	{"operatingSystemCode", "operating_system_code", kText, "Operating system code"},
	{"operatingSystemVersion", "operating_system_version", kText, "Operating system version"},
	{"browserFamily", "browser_family", kText, "Browser engine"},
	{"browserFamilyDescription", "browser_family_description", kText, "Browser engine with its description"},
	{"browser", "browser", kText, "Browser with version"},
	{"browserName", "browser_name", kText, "Browser name"},
	{"browserCode", "browser_code", kText, "Browser code"},
	{"browserVersion", "browser_version", kText, "Browser version"},
	{"plugins", "browser_plugins", kText, "Browser plugins of the visitor, comma separated"},
	{"resolution", "resolution", kText, "Screen resolution of the visitor"},
	{"languageCode", "language_code", kText, "Language code of the visitor"},
	{"language", "language", kText, "Language of the visitor"},
}

// actionMembers are the members of an action of a visit: page views, site searches, outlinks, downloads, events,
// content impressions, goal conversions, and ecommerce orders and carts.
var actionMembers = []member{
	{"type", "type", kText, "action, search, outlink, download, event, content, goal, ecommerceOrder, or ecommerceAbandonedCart"},
	{"url", "url", kURL, "Address of the action, untrusted text that is never fetched"},
	{"pageTitle", "page_title", kText, "Page title, untrusted"},
	{"timestamp", "timestamp", kInt, "Unix time of the action"},
	{"timeSpent", "time_spent", kInt, "Seconds spent on the page"},
	{"timeSpentRef", "time_spent_ref", kInt, "Seconds until the next action as Matomo stores it"},
	{"pageIdAction", "page_id_action", kInt, "ID of the page address in Matomo"},
	{"idpageview", "page_view_id", kText, "ID of the page view the action belongs to"},
	{"pageId", "page_id", kInt, "ID of the action in the log"},
	{"pageviewPosition", "pageview_position", kInt, "Position of the page view in the visit"},
	{"generationTimeMilliseconds", "generation_time_ms", kInt, "Page generation time in milliseconds"},
	{"pageLoadTimeMilliseconds", "page_load_time_ms", kInt, "Page load time in milliseconds"},
	{"siteSearchKeyword", "site_search_keyword", kText, "Site search keyword, untrusted"},
	{"siteSearchCategory", "site_search_category", kText, "Site search category, untrusted"},
	{"siteSearchCount", "site_search_count", kInt, "Results of the site search"},
	{"eventCategory", "event_category", kText, "Event category, untrusted"},
	{"eventAction", "event_action", kText, "Event action, untrusted"},
	{"eventName", "event_name", kText, "Event name, untrusted"},
	{"eventValue", "event_value", kText, "Event value as Matomo reports it"},
	{"contentName", "content_name", kText, "Content name, untrusted"},
	{"contentPiece", "content_piece", kText, "Content piece, untrusted"},
	{"contentTarget", "content_target", kText, "Content target, untrusted"},
	{"contentInteraction", "content_interaction", kText, "Content interaction, untrusted"},
	{"goalId", "goal_id", kInt, "ID of the converted goal"},
	{"goalName", "goal_name", kText, "Name of the converted goal, untrusted"},
	{"goalPageId", "goal_page_id", kInt, "ID of the action that converted the goal"},
	{"orderId", "order_id", kText, "Ecommerce order ID, untrusted"},
	{"revenue", "revenue", kText, "Revenue of the action as Matomo reports it"},
	{"revenueSubTotal", "revenue_sub_total", kText, "Sub total of the order"},
	{"revenueTax", "revenue_tax", kText, "Tax of the order"},
	{"revenueShipping", "revenue_shipping", kText, "Shipping of the order"},
	{"revenueDiscount", "revenue_discount", kText, "Discount of the order"},
	{"items", "items", kInt, "Items of the order or cart"},
	{"referrerType", "referrer_type", kText, "Kind of referrer of a conversion"},
	{"referrerName", "referrer_name", kText, "Referrer name of a conversion, untrusted"},
	{"referrerKeyword", "referrer_keyword", kText, "Referrer keyword of a conversion, untrusted"},
	{"productViewName", "product_view_name", kText, "Name of the viewed product, untrusted"},
	{"productViewSku", "product_view_sku", kText, "SKU of the viewed product, untrusted"},
	{"productViewPrice", "product_view_price", kText, "Price of the viewed product as Matomo reports it"},
}

// itemMembers are the items of an ecommerce order or cart.
var itemMembers = []member{
	{"itemSKU", "item_sku", kText, "SKU of the item, untrusted"},
	{"itemName", "item_name", kText, "Name of the item, untrusted"},
	{"itemCategory", "item_category", kText, "Category of the item, untrusted"},
	{"itemCategory2", "item_category2", kText, "Second category of the item, untrusted"},
	{"itemCategory3", "item_category3", kText, "Third category of the item, untrusted"},
	{"itemCategory4", "item_category4", kText, "Fourth category of the item, untrusted"},
	{"itemCategory5", "item_category5", kText, "Fifth category of the item, untrusted"},
	{"price", "price", kText, "Price of one item as Matomo reports it"},
	{"quantity", "quantity", kInt, "Quantity"},
}

var dimensionKey = regexp.MustCompile(`^dimension[0-9]{1,4}$`)

// customDimensions reads the dimensionN members of a visit or an action, which exist per website and scope.
func customDimensions(r row) map[string]string {
	keys := make([]string, 0)
	for key := range r {
		if dimensionKey.MatchString(key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	if len(keys) > maxCustomDimensions {
		keys = keys[:maxCustomDimensions]
	}
	out := map[string]string{}
	for _, key := range keys {
		if value := r.text(key, maxStringLength); value != "" {
			out[key] = value
		}
	}
	return out
}

// nestedRows reads a list of objects of a row, nested as Matomo answers by default or flattened to
// <name>_<n>_<member> keys when flat was asked for. more reports a list cut at limit.
func nestedRows(r row, name string, limit int) (list []row, more bool) {
	var nested []row
	if raw, found := r[name]; found && json.Unmarshal(raw, &nested) == nil {
		list = nested
	}
	flat := map[int]row{}
	for key, value := range r {
		rest, found := strings.CutPrefix(key, name+"_")
		index, field, cut := strings.Cut(rest, "_")
		n, err := strconv.Atoi(index)
		if !found || !cut || err != nil || n < 0 || n > maxFlatIndex {
			continue
		}
		if flat[n] == nil {
			flat[n] = row{}
		}
		flat[n][field] = value
	}
	indexes := make([]int, 0, len(flat))
	for n := range flat {
		indexes = append(indexes, n)
	}
	slices.Sort(indexes)
	for _, n := range indexes {
		list = append(list, flat[n])
	}
	if len(list) > limit {
		return list[:limit], true
	}
	return list, false
}

// categories reads the product view categories, a list of texts or its flattened form.
func categories(r row) []string {
	var out []string
	var nested []json.RawMessage
	if raw, found := r["productViewCategories"]; found && json.Unmarshal(raw, &nested) == nil {
		for _, one := range nested {
			var l loose
			if json.Unmarshal(one, &l) == nil && l.str(maxStringLength) != "" {
				out = append(out, l.str(maxStringLength))
			}
		}
	}
	for i := 0; i < maxCategories && len(out) < maxCategories; i++ {
		if t := r.text("productViewCategories_"+strconv.Itoa(i), maxStringLength); t != "" && len(nested) == 0 {
			out = append(out, t)
		}
	}
	if len(out) > maxCategories {
		out = out[:maxCategories]
	}
	return out
}

func actionOf(r row) map[string]any {
	out := map[string]any{}
	readMembers(r, actionMembers, out)
	if cats := categories(r); len(cats) > 0 {
		out["product_view_categories"] = cats
	}
	if items, _ := nestedRows(r, "itemDetails", maxItemDetails); len(items) > 0 {
		list := make([]map[string]any, 0, len(items))
		for _, item := range items {
			one := map[string]any{}
			readMembers(item, itemMembers, one)
			list = append(list, one)
		}
		out["item_details"] = list
	}
	if dims := customDimensions(r); len(dims) > 0 {
		out["custom_dimensions"] = dims
	}
	return out
}

func visitOf(r row, actions bool) map[string]any {
	out := map[string]any{}
	readMembers(r, visitMembers, out)
	if dims := customDimensions(r); len(dims) > 0 {
		out["custom_dimensions"] = dims
	}
	if actions {
		list, more := nestedRows(r, "actionDetails", maxActions)
		if len(list) > 0 {
			converted := make([]map[string]any, 0, len(list))
			for _, one := range list {
				converted = append(converted, actionOf(one))
			}
			out["actions"] = converted
		}
		if more {
			out["actions_truncated"] = true
		}
	}
	return out
}

// visitFields describes the result of visits.list from the member tables.
func visitFields() []capability.Field {
	fields := []capability.Field{{Name: "visits", Description: "Visits of this batch, newest first, only of the " +
		"website asked for"}}
	for _, m := range visitMembers {
		fields = append(fields, capability.Field{Name: "visits[]." + m.to, Description: m.doc})
	}
	fields = append(fields, capability.Field{Name: "visits[].custom_dimensions", Description: "Visit-scope custom " +
		"dimensions as dimensionN to value, up to 30, untrusted"},
		capability.Field{Name: "visits[].actions", Description: "Only with include_actions: up to 50 actions of the " +
			"visit, oldest first"},
		capability.Field{Name: "visits[].actions_truncated", Description: "True when the visit has more actions than are returned"})
	for _, m := range actionMembers {
		fields = append(fields, capability.Field{Name: "visits[].actions[]." + m.to, Description: m.doc})
	}
	fields = append(fields,
		capability.Field{Name: "visits[].actions[].product_view_categories", Description: "Categories of the viewed " +
			"product, up to 5, untrusted"},
		capability.Field{Name: "visits[].actions[].item_details", Description: "Items of an order or cart, up to 50"},
		capability.Field{Name: "visits[].actions[].custom_dimensions", Description: "Action-scope custom dimensions " +
			"as dimensionN to value, up to 30, untrusted"})
	for _, m := range itemMembers {
		fields = append(fields, capability.Field{Name: "visits[].actions[].item_details[]." + m.to, Description: m.doc})
	}
	return fields
}
