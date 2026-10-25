package brave

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxWindows bounds how many rate-limit windows an answer is read for.
const maxWindows = 4

// window is one rate-limit window of a plan: a sliding second or a quota period. Every member is optional,
// because Brave documents the headers but not that each answer carries all of them.
type window struct {
	limit     *int64
	remaining *int64
	reset     *int64
	seconds   *int64
}

// rateInfo is what the X-RateLimit-* headers of one answer say, one window per list position. The
// X-RateLimit-Policy header names the window sizes; without it the first window is the sliding second.
type rateInfo struct{ windows []window }

// parseRate reads the four comma-separated rate-limit headers. An entry that is not a non-negative whole
// number is treated as absent.
func parseRate(h http.Header) rateInfo {
	limits := numberList(h.Get("X-RateLimit-Limit"))
	remaining := numberList(h.Get("X-RateLimit-Remaining"))
	resets := numberList(h.Get("X-RateLimit-Reset"))
	policyLimits, sizes := policyList(h.Get("X-RateLimit-Policy"))

	count := max(len(limits), len(remaining), len(resets), len(sizes))
	count = min(count, maxWindows)
	windows := make([]window, count)
	for i := range windows {
		windows[i] = window{
			limit: at(limits, i), remaining: at(remaining, i), reset: at(resets, i), seconds: at(sizes, i),
		}
		if windows[i].limit == nil {
			windows[i].limit = at(policyLimits, i)
		}
	}
	return rateInfo{windows: windows}
}

// numberList reads "1, 15000" into its entries; an unusable entry is nil.
func numberList(value string) []*int64 {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]*int64, len(parts))
	for i, part := range parts {
		out[i] = number(part)
	}
	return out
}

func number(text string) *int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

// policyList reads "1;w=1, 15000;w=2592000" into the limits and the window sizes in seconds.
func policyList(value string) (limits, sizes []*int64) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	for _, part := range strings.Split(value, ",") {
		limit, rest, _ := strings.Cut(part, ";")
		limits = append(limits, number(limit))
		var size *int64
		for _, parameter := range strings.Split(rest, ";") {
			if text, ok := strings.CutPrefix(strings.TrimSpace(parameter), "w="); ok {
				size = number(text)
			}
		}
		sizes = append(sizes, size)
	}
	return limits, sizes
}

func at(list []*int64, i int) *int64 {
	if i < len(list) {
		return list[i]
	}
	return nil
}

// first returns the first window, whose per-second limit gives the spacing between two requests.
func (r rateInfo) first() (window, bool) {
	if len(r.windows) == 0 {
		return window{}, false
	}
	return r.windows[0], true
}

// exhausted returns how long the used-up windows still take to reset: short for those that reset within
// maxHold, which a request can wait out, and long for the others, which are a quota. Zero means none. A
// used-up window without a reset time counts as one second, the size of the sliding window.
func (r rateInfo) exhausted() (short, long time.Duration) {
	for _, w := range r.windows {
		if w.remaining == nil || *w.remaining != 0 {
			continue
		}
		reset := time.Second
		if w.reset != nil {
			reset = time.Duration(min(*w.reset, int64(1<<40))) * time.Second
		}
		if reset > maxHold {
			long = max(long, reset)
		} else {
			short = max(short, reset)
		}
	}
	return short, long
}

// quotaWindow is one window as a result shows it.
type quotaWindow struct {
	WindowSeconds *int64 `json:"window_seconds,omitempty"`
	Limit         *int64 `json:"limit,omitempty"`
	Remaining     *int64 `json:"remaining,omitempty"`
	ResetSeconds  *int64 `json:"reset_seconds,omitempty"`
}

// quota is the compact rate and quota state every result carries.
type quota struct {
	Windows []quotaWindow `json:"windows"`
}

// quotaOf returns the quota of an answer, or nil when it carried no usable rate-limit header.
func quotaOf(info rateInfo) *quota {
	var out quota
	for _, w := range info.windows {
		if w.limit == nil && w.remaining == nil && w.reset == nil && w.seconds == nil {
			continue
		}
		out.Windows = append(out.Windows, quotaWindow{
			WindowSeconds: w.seconds, Limit: w.limit, Remaining: w.remaining, ResetSeconds: w.reset,
		})
	}
	if len(out.Windows) == 0 {
		return nil
	}
	return &out
}

// quotaSchema is the output schema of the quota member. A limit of 0 means the window is unlimited.
const quotaSchema = `{"type":"object","properties":{"windows":{"type":"array","items":{"type":"object",` +
	`"properties":{"window_seconds":{"type":"integer"},"limit":{"type":"integer"},` +
	`"remaining":{"type":"integer"},"reset_seconds":{"type":"integer"}},"additionalProperties":false}}},` +
	`"additionalProperties":false}`
