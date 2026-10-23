package dav

import "github.com/castrowithcee/qatlas-cli/internal/capability"

const (
	str  = `{"type":"string"}`
	flag = `{"type":"boolean"}`
	num  = `{"type":"integer"}`
)

func listOf(items string) string { return `{"type":"array","items":` + items + `}` }

func objectOf(properties string) string {
	return `{"type":"object","properties":{` + properties + `},"additionalProperties":false}`
}

// PersonSchema describes an organizer or attendee.
const PersonSchema = `{"type":"object","properties":{"address":` + str + `,"name":` + str + `,"status":` + str + `,` +
	`"role":` + str + `,"cutype":` + str + `,"rsvp":` + flag + `,"member":{"type":"array","items":` + str + `},` +
	`"member_truncated":` + flag + `,"delegated_to":{"type":"array","items":` + str + `},` +
	`"delegated_to_truncated":` + flag + `,"delegated_from":{"type":"array","items":` + str + `},` +
	`"delegated_from_truncated":` + flag + `,"sent_by":` + str + `,"dir":` + str + `,"language":` + str + `},` +
	`"additionalProperties":false}`

var attachmentSchema = objectOf(`"uri":` + str + `,"format_type":` + str)

var alarmSchema = objectOf(`"action":` + str + `,"trigger":` + str + `,"trigger_related":` + str + `,` +
	`"trigger_absolute":` + flag + `,"duration":` + str + `,"repeat":` + num + `,"description":` + str + `,` +
	`"summary":` + str + `,"attendees":` + listOf(objectOf(`"address":`+str+`,"name":`+str)) + `,` +
	`"attendees_truncated":` + flag + `,"attachments":` + listOf(attachmentSchema) + `,` +
	`"attachments_truncated":` + flag)

// bodyProperties lists the members of an event except its resource identity.
var bodyProperties = `"uid":` + str + `,"summary":` + str + `,"description":` + str + `,"location":` + str + `,` +
	`"start":` + str + `,"end":` + str + `,"all_day":` + flag + `,"timezone":` + str + `,"status":` + str + `,` +
	`"rrule":` + str + `,"organizer":` + PersonSchema + `,"attendees":` + listOf(PersonSchema) + `,` +
	`"attendees_truncated":` + flag + `,"end_timezone":` + str + `,"duration":` + str + `,` +
	`"exdates":` + listOf(str) + `,"exdates_truncated":` + flag + `,"rdates":` + listOf(str) + `,` +
	`"rdates_truncated":` + flag + `,"recurrence_id":` + str + `,"recurrence_range":` + str + `,` +
	`"class":` + str + `,"transp":` + str + `,"priority":` + num + `,"sequence":` + num + `,"geo":` + str + `,` +
	`"url":` + str + `,"created":` + str + `,"last_modified":` + str + `,"dtstamp":` + str + `,` +
	`"categories":` + listOf(str) + `,"categories_truncated":` + flag + `,"comments":` + listOf(str) + `,` +
	`"comments_truncated":` + flag + `,"contacts":` + listOf(str) + `,"contacts_truncated":` + flag + `,` +
	`"resources":` + listOf(str) + `,"resources_truncated":` + flag + `,` +
	`"related_to":` + listOf(objectOf(`"uid":`+str+`,"reltype":`+str)) + `,"related_to_truncated":` + flag + `,` +
	`"request_status":` + listOf(objectOf(`"code":`+str+`,"description":`+str+`,"data":`+str)) + `,` +
	`"request_status_truncated":` + flag + `,"attachments":` + listOf(attachmentSchema) + `,` +
	`"attachments_truncated":` + flag + `,"alarms":` + listOf(alarmSchema) + `,"alarms_truncated":` + flag

// EventSchema describes an event of the list and get tools. The compact list leaves most members out.
var EventSchema = `{"type":"object","properties":{"id":` + str + `,` + bodyProperties + `,"etag":` + str + `,` +
	`"overrides":` + listOf(`{"type":"object","properties":{`+bodyProperties+`},"additionalProperties":false}`) + `,` +
	`"overrides_truncated":` + flag + `},"required":["id"],"additionalProperties":false}`

// EventFieldList documents the shared event members, for the descriptor of each provider.
func EventFieldList() []capability.Field {
	return []capability.Field{
		{Name: "end_timezone", Description: "Time zone name of a local end"},
		{Name: "duration", Description: "Duration as written in the event, at most 64 bytes; end is never derived from it"},
		{Name: "exdates", Description: "At most 100 excluded occurrences in the form of start; exdates_truncated when there were more"},
		{Name: "rdates", Description: "At most 100 additional occurrences in the form of start, a period as written; rdates_truncated when there were more"},
		{Name: "recurrence_id", Description: "Occurrence an override replaces, in the form of start; only in an override"},
		{Name: "recurrence_range", Description: "RANGE of the recurrence id, for example THISANDFUTURE; only in an override"},
		{Name: "class", Description: "Access classification, untrusted data"},
		{Name: "transp", Description: "Time transparency, untrusted data"},
		{Name: "priority", Description: "Priority from 0 to 9 when the event carries a valid one"},
		{Name: "sequence", Description: "Revision number when the event carries a valid one"},
		{Name: "geo", Description: "Position as the raw text lat;lon, at most 64 bytes"},
		{Name: "url", Description: "Address of the event, untrusted text that is never fetched; omitted when it carries user information, a query, or a fragment"},
		{Name: "created", Description: "Creation time in the form of start"},
		{Name: "last_modified", Description: "Last modification time in the form of start"},
		{Name: "dtstamp", Description: "Time stamp of the event object in the form of start"},
		{Name: "categories", Description: "At most 20 categories of 64 bytes, untrusted data; categories_truncated when there were more"},
		{Name: "comments", Description: "At most 20 comments of 1024 bytes, untrusted data; comments_truncated when there were more"},
		{Name: "contacts", Description: "At most 20 contacts of 256 bytes, untrusted data; contacts_truncated when there were more"},
		{Name: "resources", Description: "At most 20 resources of 64 bytes, untrusted data; resources_truncated when there were more"},
		{Name: "related_to", Description: "At most 20 relations with uid and reltype; related_to_truncated when there were more"},
		{Name: "request_status", Description: "At most 20 request statuses with code, description, and data; request_status_truncated when there were more"},
		{Name: "attachments", Description: "At most 20 attachments with uri (untrusted text, never fetched) and format_type; embedded binary data and addresses with user information, query, or fragment are omitted"},
		{Name: "alarms", Description: "At most 20 alarms with action, trigger, trigger_related, trigger_absolute, duration, repeat, description, summary, attendees, and attachments, untrusted data; alarms_truncated when there were more"},
		{Name: "overrides", Description: "At most 50 overrides of single occurrences in the form of an event without id, etag, and overrides, each with recurrence_id; overrides_truncated when there were more"},
	}
}
