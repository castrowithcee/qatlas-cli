package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	// defaultResponseBytes bounds the response of every tool that does not need more.
	defaultResponseBytes = 64 << 10
	// maxUploadBytes is the Bot API's limit for one uploaded file; a larger body is refused before any I/O.
	maxUploadBytes = 50 << 20
	// uncertain is appended to a failure of a changing request whose outcome is unknown: the change may have
	// taken effect although no confirmation arrived. Qatlas never repeats such a request by itself.
	uncertain = "; the change may have taken effect, check the chat before repeating it"
)

// spec describes one fixed Bot API method call. Method is always a literal of the calling tool, never
// derived from an argument.
type spec struct {
	op       string
	method   string
	limit    int
	readOnly bool
}

// filePart is one uploaded file of a multipart request.
type filePart struct {
	field, name string
	data        []byte
}

// field is one plain form field of a multipart request.
type field struct{ name, value string }

// call sends exactly one JSON POST to the fixed method and returns the envelope's result. It never retries.
func (c *Client) call(ctx context.Context, s spec, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, providerError(s.op, "the request could not be encoded")
	}
	return c.exchange(ctx, s, http.MethodPost, "application/json", bytes.NewReader(body))
}

// callMultipart sends exactly one multipart POST for methods that upload files. It never retries.
func (c *Client) callMultipart(ctx context.Context, s spec, fields []field, files []filePart) (json.RawMessage, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	total := 0
	for _, f := range fields {
		total += len(f.value)
		if err := writer.WriteField(f.name, f.value); err != nil {
			return nil, providerError(s.op, "the request could not be encoded")
		}
	}
	for _, f := range files {
		total += len(f.data)
		if total > maxUploadBytes {
			return nil, providerError(s.op, "the upload is larger than Telegram accepts")
		}
		part, err := writer.CreateFormFile(f.field, f.name)
		if err != nil {
			return nil, providerError(s.op, "the request could not be encoded")
		}
		if _, err := part.Write(f.data); err != nil {
			return nil, providerError(s.op, "the request could not be encoded")
		}
	}
	if err := writer.Close(); err != nil {
		return nil, providerError(s.op, "the request could not be encoded")
	}
	return c.exchange(ctx, s, http.MethodPost, writer.FormDataContentType(), &buf)
}

// envelope is the Bot API response frame. Telegram's description is deliberately not decoded: it is
// provider text and never reaches an error message.
type envelope struct {
	OK         bool            `json:"ok"`
	Result     json.RawMessage `json:"result"`
	Parameters struct {
		RetryAfter      int64 `json:"retry_after"`
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

func (c *Client) exchange(ctx context.Context, s spec, httpMethod, contentType string, body io.Reader) (json.RawMessage, error) {
	target := *c.base
	target.Path = strings.TrimRight(target.Path, "/") + "/bot" + c.token + "/" + s.method
	target.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, httpMethod, target.String(), body)
	if err != nil {
		return nil, providerError(s.op, "the request could not be built")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(s.op, "Telegram", err)
		if !s.readOnly && failure.MayHaveArrived() {
			failure.Message += uncertain
		}
		return nil, failure
	}
	data, readErr := readResponse(response.Body, s.limit)
	var env envelope
	parsed := readErr == nil && json.Unmarshal(data, &env) == nil
	if response.StatusCode == http.StatusOK {
		if !parsed || !env.OK || len(env.Result) == 0 {
			return nil, withUncertainty(s, invalidResponse(s.op))
		}
		return env.Result, nil
	}
	if parsed {
		switch {
		case env.Parameters.MigrateToChatID != 0:
			return nil, providerError(s.op,
				"the Telegram chat was migrated to a supergroup; adjust the connection's target")
		case env.Parameters.RetryAfter > 0:
			return nil, &provider.Error{Class: provider.ClassRateLimited, Op: s.op,
				Message: "Telegram rate-limited the operation"}
		}
	}
	failure := statusError(s.op, response.StatusCode)
	if response.StatusCode >= 500 {
		return nil, withUncertainty(s, failure)
	}
	return nil, failure
}

func withUncertainty(s spec, err error) error {
	var providerErr *provider.Error
	if !s.readOnly && errors.As(err, &providerErr) {
		providerErr.Message += uncertain
	}
	return err
}

func readResponse(body io.ReadCloser, limit int) ([]byte, error) {
	defer body.Close()
	value, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil || len(value) > limit {
		return nil, errors.New("response could not be read within the limit")
	}
	return value, nil
}

func invalidResponse(op string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "Telegram returned an invalid response"}
}

func statusError(op string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Telegram rejected the bot token"}
	case http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "Telegram refused this operation; check the rights of the bot in Telegram"}
	case http.StatusTooManyRequests:
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Telegram rate-limited the operation"}
	case http.StatusConflict:
		// No dedicated conflict class exists; provider-error is the narrowest fit.
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Telegram reported a conflict with another request or webhook of this bot"}
	default:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("Telegram rejected the operation (HTTP %d)", status),
		}
	}
}

// parseBase is the single origin check shared by opening a connection and configuration validation: only
// https with a host and without user, query, or fragment. There is no loopback exception.
func parseBase(raw string) (*url.URL, error) {
	base, err := url.Parse(raw)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil ||
		base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" {
		return nil, errors.New("the Telegram base URL must be a plain HTTPS URL")
	}
	return base, nil
}
