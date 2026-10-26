package matomo

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
)

// maxTrackingAnswer bounds what is read of a tracker answer: the GIF Matomo returns has 43 bytes.
const maxTrackingAnswer = 4096

// neverSentParameters are tracking parameters that override what Matomo derives itself or need the token. The
// general form path refuses a form that holds one; only trackingAuth sets the overrides and the token, and cdo
// is never sent.
var neverSentParameters = []string{"cip", "cdt", "cdo", "country", "region", "city", "lat", "long", "token_auth"}

// overrideParameters are the neverSentParameters that trackingAuth may carry besides the token.
var overrideParameters = map[string]bool{"cip": true, "cdt": true, "country": true, "region": true, "city": true,
	"lat": true, "long": true}

// trackingAuth is the one way an override and the token reach the body: the validated overrides and the token
// that lets Matomo accept them. It exists only when at least one override is set.
type trackingAuth struct {
	token     string
	overrides url.Values
}

// trackingSender posts to the tracking endpoint of one connection. It holds no secret: a request without
// overrides needs no token, and one with overrides gets the token per call through trackingAuth, in the body
// only and never as an Authorization header. It shares the transport and the base URL rules of Client.
type trackingSender struct {
	origin  string
	http    *http.Client
	limiter *ratelimit.Limiter
}

// openTracking resolves the base URL of a connection and the website scope, and nothing else.
func openTracking(resolved *config.Resolved) (*trackingSender, error) {
	if resolved == nil {
		return nil, provider.Fail("open", "no connection was selected")
	}
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return nil, provider.Fail("open", err.Error())
	}
	return &trackingSender{origin: origin, http: provider.NoRedirectClient(defaultTimeout, transport),
		limiter: limiters.For("tracking\x00" + origin)}, nil
}

// randomValue is the cache buster of a tracking request.
func randomValue() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "1"
	}
	return strconv.FormatUint(binary.BigEndian.Uint64(b[:])>>1, 10)
}

// send posts the form once to matomo.php. Only an empty 204 or a GIF counts as sent; that proves no
// processing, because Matomo answers many invalid parameters the same way and a queue confirms before it
// processes. It never retries.
func (s *trackingSender) send(ctx context.Context, op string, site int, params url.Values, auth *trackingAuth) error {
	form := url.Values{}
	for name, values := range params {
		form[name] = values
	}
	for _, name := range neverSentParameters {
		if _, found := form[name]; found {
			return provider.Fail(op, "the request carries a parameter this provider never sends")
		}
	}
	if auth != nil {
		if auth.token == "" || len(auth.overrides) == 0 {
			return provider.Fail(op, "the request carries a token without an override")
		}
		for name, values := range auth.overrides {
			if !overrideParameters[name] || len(values) != 1 {
				return provider.Fail(op, "the request carries a parameter this provider never sends")
			}
			form[name] = values
		}
		form.Set("token_auth", auth.token)
	}
	form.Set("idsite", strconv.Itoa(site))
	form.Set("rec", "1")
	form.Set("apiv", "1")
	form.Set("rand", randomValue())
	form.Set("send_image", "0")
	if err := s.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, subject, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.origin+"/matomo.php", strings.NewReader(form.Encode()))
	if err != nil {
		return provider.Fail(op, "the request could not be built")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := s.http.Do(req)
	if err != nil {
		return provider.Transport(op, subject, err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNoContent:
		return nil
	case response.StatusCode == http.StatusOK:
		data, err := io.ReadAll(io.LimitReader(response.Body, maxTrackingAnswer+1))
		if err != nil || len(data) > maxTrackingAnswer || !strings.HasPrefix(string(data), "GIF8") {
			return provider.InvalidResponse(op, "the Matomo tracker returned an answer that is neither an empty "+
				"204 nor an image")
		}
		return nil
	}
	return s.failure(op, response, auth != nil)
}

// failure maps an HTTP status of the tracker to a class; the body is never read into the message.
func (s *trackingSender) failure(op string, response *http.Response, withToken bool) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxTrackingAnswer))
	switch response.StatusCode {
	case http.StatusBadRequest:
		// Matomo uses 400 for an unknown website and for an invalid request parameter; nothing was recorded.
		// It answers 400 as well when an override comes without a valid token of the Write role or better, and
		// when cdt is older than the raw data the instance keeps.
		if withToken {
			return &provider.InvalidRequestError{Message: "the Matomo tracker refused the request as invalid: the " +
				"website ID is unknown to the tracker, a parameter is invalid, the token does not have at least " +
				"the Write role for the website, or the timestamp is older than the raw data the instance keeps; " +
				"nothing was recorded"}
		}
		return &provider.InvalidRequestError{Message: "the Matomo tracker refused the request as invalid: the " +
			"website ID is unknown to the tracker or a parameter is invalid; nothing was recorded"}
	case http.StatusTooManyRequests:
		hold := provider.RetryAfter(response.Header)
		if hold <= 0 {
			hold = rateLimitHold
		}
		s.limiter.HoldFor(min(hold, maxRateLimitHold))
	case http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op,
			Message: "the Matomo tracker is unavailable: tracking is switched off or the instance is in maintenance; nothing was recorded"}
	}
	texts := provider.StatusTexts{
		Subject:    subject,
		Auth:       "the Matomo tracking endpoint asked for authentication, which this tool sends only with an override",
		Permission: "the Matomo tracking endpoint refused the request",
		NotFound:   "matomo.php was not found below the base URL of this connection",
	}
	if withToken {
		texts.Auth = "the Matomo tracking endpoint refused the token; the overrides need at least the Write role for the website"
		texts.Permission = texts.Auth
	}
	return provider.ClassifyStatus(op, response.StatusCode, texts)
}
