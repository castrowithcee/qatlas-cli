package twentycrm

import (
	"encoding/json"
	"sort"
)

// companyCompositeKeys lists, per composite company field, the subfields of the Twenty company object and
// their Qatlas names. A subfield outside the list is not reported.
var (
	linksKeys = map[string]string{"primaryLinkUrl": "primary_link_url", "primaryLinkLabel": "primary_link_label",
		"secondaryLinks": "secondary_links"}
	currencyKeys = map[string]string{"amountMicros": "amount_micros", "currencyCode": "currency_code"}
	addressKeys  = map[string]string{"addressStreet1": "address_street1", "addressStreet2": "address_street2",
		"addressCity": "address_city", "addressPostcode": "address_postcode", "addressState": "address_state",
		"addressCountry": "address_country", "addressLat": "address_lat", "addressLng": "address_lng"}
	actorKeys = map[string]string{"source": "source", "workspaceMemberId": "workspace_member_id",
		"name": "name", "context": "context"}
)

// companyStandardFields are the members of the Twenty company object. The core ones are reported by name, the
// others are never reported: addressOld is marked deprecated, searchVector is the derived search index, and
// the relations are not part of a record read at depth 0.
var companyStandardFields = map[string]bool{
	"id": true, "name": true, "domainName": true, "createdAt": true, "updatedAt": true, "deletedAt": true,
	"linkedinLink": true, "annualRevenue": true, "address": true, "addressOld": true, "position": true,
	"createdBy": true, "updatedBy": true, "searchVector": true, "accountOwnerId": true,
	"people": true, "accountOwner": true, "taskTargets": true, "noteTargets": true, "agentChatThreadTargets": true,
	"calendarEventTargets": true, "messageThreadTargets": true, "opportunities": true, "attachments": true,
	"timelineActivities": true,
}

// maxOmittedNames bounds the list of field names a company reports as omitted.
const maxOmittedNames = maxValueItems

// fullCompany adds every further field of a company record to company. Without the workspace catalog the
// values are recognized by their shape: a list element with a fileId loses its url, an object with a
// blocknote member keeps its markdown, and an address of the connection's own host with a token is dropped.
// A value beyond the size limits is left out and named in OmittedFields, never shortened.
func (o *recordObject) fullCompany(company *Company, item map[string]json.RawMessage) {
	omit := func(name string) {
		if len(company.OmittedFields) < maxOmittedNames {
			company.OmittedFields = append(company.OmittedFields, name)
		}
	}
	// value decodes and bounds one field value; ok is false when it is absent, empty, or left out.
	value := func(name string, raw json.RawMessage, custom bool) (any, bool) {
		decoded, err := decodeValue(raw)
		if err != nil {
			omit(name)
			return nil, false
		}
		if custom {
			decoded = richTextOnly(catalogField{}, decoded)
		}
		decoded, keep := o.withholdAddresses(catalogField{}, decoded)
		if !keep {
			return nil, false
		}
		if checkValue(decoded, 0) != nil {
			omit(name)
			return nil, false
		}
		return decoded, !emptyValue(decoded)
	}
	composite := func(name, field string, keys map[string]string) map[string]any {
		raw, ok := item[field]
		if !ok {
			return nil
		}
		decoded, ok := value(name, raw, false)
		object, isObject := decoded.(map[string]any)
		if !ok || !isObject {
			return nil
		}
		result := map[string]any{}
		for key, renamed := range keys {
			if member, present := object[key]; present && !emptyValue(member) {
				result[renamed] = member
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	}
	company.DomainName = composite("domain_name", "domainName", linksKeys)
	company.LinkedinLink = composite("linkedin_link", "linkedinLink", linksKeys)
	company.AnnualRevenue = composite("annual_revenue", "annualRevenue", currencyKeys)
	company.Address = composite("address", "address", addressKeys)
	company.CreatedBy = composite("created_by", "createdBy", actorKeys)
	company.UpdatedBy = composite("updated_by", "updatedBy", actorKeys)
	if raw, ok := item["position"]; ok {
		if decoded, ok := value("position", raw, false); ok {
			if number, isNumber := decoded.(json.Number); isNumber {
				company.Position = number
			}
		}
	}
	for field, target := range map[string]*string{"accountOwnerId": &company.AccountOwnerID, "deletedAt": &company.DeletedAt} {
		if raw, ok := item[field]; ok {
			if decoded, ok := value(field, raw, false); ok {
				if text, isText := decoded.(string); isText {
					*target = text
				}
			}
		}
	}

	names := make([]string, 0, len(item))
	for name := range item {
		if !companyStandardFields[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if !fieldNamePattern.MatchString(name) {
			continue
		}
		if len(company.CustomFields) >= maxValueItems {
			omit("custom_fields." + name)
			continue
		}
		if decoded, ok := value("custom_fields."+name, item[name], true); ok {
			if company.CustomFields == nil {
				company.CustomFields = map[string]any{}
			}
			company.CustomFields[name] = decoded
		}
	}
	sort.Strings(company.OmittedFields)
}

// emptyValue reports a value Twenty uses for "nothing": null, an empty text, or an empty list or object.
func emptyValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}
