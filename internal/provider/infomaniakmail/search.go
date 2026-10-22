package infomaniakmail

import (
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
)

// Bounds of the typed SEARCH filters of messages.list.
const (
	maxSearchText     = 256
	maxHeaderName     = 64
	maxHeaders        = 3
	maxKeywords       = 5
	maxKeywordLength  = 64
	maxSearchSize     = 1<<32 - 1
	searchTextSchema  = `{"type":"string","minLength":1,"maxLength":256}`
	searchHeaderShape = `{"type":"array","maxItems":3,"items":{"type":"object","properties":{` +
		`"name":{"type":"string","pattern":"^[!-9;-~]{1,64}$"},` +
		`"value":{"type":"string","maxLength":256}},"required":["name","value"],"additionalProperties":false}}`
	// keywordShape allows the printable ASCII atom characters of RFC 3501 except the atom-specials
	// ( ) { % * " \ ] and so also a leading backslash.
	keywordShape = `{"type":"array","maxItems":5,"items":{"type":"string","minLength":1,"maxLength":64,` +
		`"pattern":"^[!#-$&-'+-\\[^-z|-~]+$"}}`
	searchSizeSchema = `{"type":"integer","minimum":0,"maximum":4294967295}`
	searchDateSchema = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}`
)

// headerArgument is one HEADER search pair.
type headerArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// searchArguments are the typed SEARCH filters beyond since, before, unread, and sender. A nil pointer is an
// absent argument, so an explicit empty or false value can be told apart.
type searchArguments struct {
	To         *string          `json:"to"`
	Cc         *string          `json:"cc"`
	Bcc        *string          `json:"bcc"`
	Subject    *string          `json:"subject"`
	Body       *string          `json:"body"`
	Text       *string          `json:"text"`
	Header     []headerArgument `json:"header"`
	Larger     *int64           `json:"larger"`
	Smaller    *int64           `json:"smaller"`
	On         string           `json:"on"`
	SentSince  string           `json:"sent_since"`
	SentBefore string           `json:"sent_before"`
	SentOn     string           `json:"sent_on"`
	Flagged    *bool            `json:"flagged"`
	Answered   *bool            `json:"answered"`
	Deleted    *bool            `json:"deleted"`
	Draft      *bool            `json:"draft"`
	Seen       *bool            `json:"seen"`
	Keyword    []string         `json:"keyword"`
	Unkeyword  []string         `json:"unkeyword"`
}

// searchFilters validates the typed SEARCH filters without any network access and returns them as library
// criteria. Since and Before carry the day window of on, because the library then sends ON; since and before
// are the existing filters that on excludes, and unread the one that seen must not contradict.
func searchFilters(in searchArguments, unread bool, since, before time.Time) (imap.SearchCriteria, error) {
	var c imap.SearchCriteria
	texts := []struct {
		name  string
		value *string
		key   string
	}{{"to", in.To, "TO"}, {"cc", in.Cc, "CC"}, {"bcc", in.Bcc, "BCC"}, {"subject", in.Subject, "SUBJECT"}}
	for _, t := range texts {
		if t.value == nil {
			continue
		}
		if !validSearchText(*t.value, 1) {
			return c, invalidRequest(t.name + " must be 1 to " + strconv.Itoa(maxSearchText) + " bytes without control characters")
		}
		c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: t.key, Value: *t.value})
	}
	if len(in.Header) > maxHeaders {
		return c, invalidRequest("header takes at most " + strconv.Itoa(maxHeaders) + " pairs")
	}
	for _, h := range in.Header {
		if !validHeaderName(h.Name) {
			return c, invalidRequest("header name must be 1 to " + strconv.Itoa(maxHeaderName) +
				" printable ASCII characters without a colon")
		}
		if !validSearchText(h.Value, 0) {
			return c, invalidRequest("header value must be at most " + strconv.Itoa(maxSearchText) +
				" bytes without control characters")
		}
		c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: h.Name, Value: h.Value})
	}
	if in.Body != nil {
		if !validSearchText(*in.Body, 1) {
			return c, invalidRequest("body must be 1 to " + strconv.Itoa(maxSearchText) + " bytes without control characters")
		}
		c.Body = []string{*in.Body}
	}
	if in.Text != nil {
		if !validSearchText(*in.Text, 1) {
			return c, invalidRequest("text must be 1 to " + strconv.Itoa(maxSearchText) + " bytes without control characters")
		}
		c.Text = []string{*in.Text}
	}

	for _, size := range []struct {
		name  string
		value *int64
	}{{"larger", in.Larger}, {"smaller", in.Smaller}} {
		if size.value != nil && (*size.value < 0 || *size.value > maxSearchSize) {
			return c, invalidRequest(size.name + " must be between 0 and 4294967295 bytes")
		}
	}
	if in.Smaller != nil && *in.Smaller == 0 {
		// SMALLER 0 can match nothing, and the library sends no SMALLER for 0.
		return c, invalidRequest("smaller must be at least 1 byte")
	}
	if in.Larger != nil {
		c.Larger = *in.Larger
	}
	if in.Smaller != nil {
		c.Smaller = *in.Smaller
		if in.Larger != nil && *in.Smaller <= *in.Larger {
			return c, invalidRequest("smaller must be greater than larger")
		}
	}

	if in.On != "" {
		if !since.IsZero() || !before.IsZero() {
			return c, invalidRequest("on cannot be combined with since or before")
		}
		day, err := parseDate(in.On)
		if err != nil {
			return c, invalidRequest("on must be a date as YYYY-MM-DD")
		}
		c.Since, c.Before = day, day.AddDate(0, 0, 1)
	}
	var err error
	if c.SentSince, err = parseDate(in.SentSince); err != nil {
		return c, invalidRequest("sent_since must be a date as YYYY-MM-DD")
	}
	if c.SentBefore, err = parseDate(in.SentBefore); err != nil {
		return c, invalidRequest("sent_before must be a date as YYYY-MM-DD")
	}
	if !c.SentSince.IsZero() && !c.SentBefore.IsZero() && !c.SentSince.Before(c.SentBefore) {
		return c, invalidRequest("sent_since must be earlier than sent_before")
	}
	if in.SentOn != "" {
		if !c.SentSince.IsZero() || !c.SentBefore.IsZero() {
			return c, invalidRequest("sent_on cannot be combined with sent_since or sent_before")
		}
		day, err := parseDate(in.SentOn)
		if err != nil {
			return c, invalidRequest("sent_on must be a date as YYYY-MM-DD")
		}
		c.SentSince, c.SentBefore = day, day.AddDate(0, 0, 1)
	}

	if unread && in.Seen != nil {
		return c, invalidRequest("seen cannot be combined with unread")
	}
	if unread {
		c.NotFlag = append(c.NotFlag, imap.FlagSeen)
	}
	for _, f := range []struct {
		value *bool
		flag  imap.Flag
	}{{in.Flagged, imap.FlagFlagged}, {in.Answered, imap.FlagAnswered}, {in.Deleted, imap.FlagDeleted},
		{in.Draft, imap.FlagDraft}, {in.Seen, imap.FlagSeen}} {
		switch {
		case f.value == nil:
		case *f.value:
			c.Flag = append(c.Flag, f.flag)
		default:
			c.NotFlag = append(c.NotFlag, f.flag)
		}
	}

	if len(in.Keyword) > maxKeywords || len(in.Unkeyword) > maxKeywords {
		return c, invalidRequest("keyword and unkeyword take at most " + strconv.Itoa(maxKeywords) + " entries each")
	}
	wanted := make(map[string]bool, len(in.Keyword))
	for _, k := range in.Keyword {
		if !validKeyword(k) {
			return c, invalidRequest("a keyword must be 1 to " + strconv.Itoa(maxKeywordLength) +
				" IMAP atom characters and must not start with a backslash")
		}
		wanted[strings.ToLower(k)] = true
		c.Flag = append(c.Flag, imap.Flag(k))
	}
	for _, k := range in.Unkeyword {
		if !validKeyword(k) {
			return c, invalidRequest("a keyword must be 1 to " + strconv.Itoa(maxKeywordLength) +
				" IMAP atom characters and must not start with a backslash")
		}
		if wanted[strings.ToLower(k)] {
			return c, invalidRequest("a keyword cannot be in both keyword and unkeyword")
		}
		c.NotFlag = append(c.NotFlag, imap.Flag(k))
	}
	return c, nil
}

// validSearchText accepts valid UTF-8 of min to maxSearchText bytes without control characters.
func validSearchText(value string, min int) bool {
	if len(value) < min || len(value) > maxSearchText || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validHeaderName accepts an RFC 5322 field name: printable ASCII (33 to 126) without a colon.
func validHeaderName(name string) bool {
	if name == "" || len(name) > maxHeaderName {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 33 || name[i] > 126 || name[i] == ':' {
			return false
		}
	}
	return true
}

// validKeyword accepts an IMAP atom (RFC 3501 ATOM-CHAR) that is no system flag.
func validKeyword(value string) bool {
	if value == "" || len(value) > maxKeywordLength {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte(`(){%*"\]`, c) >= 0 {
			return false
		}
	}
	return true
}
