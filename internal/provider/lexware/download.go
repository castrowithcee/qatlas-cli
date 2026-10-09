package lexware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// downloadPaths maps the voucher_type values to their fixed file endpoints. The argument never becomes part of
// a path itself.
var downloadPaths = map[string]string{
	"invoice":              "/v1/invoices/",
	"credit_note":          "/v1/credit-notes/",
	"delivery_note":        "/v1/delivery-notes/",
	"dunning":              "/v1/dunnings/",
	"down_payment_invoice": "/v1/down-payment-invoices/",
	"order_confirmation":   "/v1/order-confirmations/",
	"quotation":            "/v1/quotations/",
}

// downloadAccepts maps the format values to the only Accept headers a download sends. image asks for png,
// which Lexware answers with the original png or jpg image of the voucher.
var downloadAccepts = map[string]string{
	"default": "*/*",
	"pdf":     "application/pdf",
	"xml":     "application/xml",
	"image":   "image/png",
}

// maxDownloadBytes bounds one downloaded document, well below the limit of the local file package.
const maxDownloadBytes = 20 << 20

const (
	// notFinalizedMessage is the fixed text for a draft: Lexware creates the document at finalization.
	notFinalizedMessage = "the document exists only after the voucher is finalized"
	tooLargeMessage     = "the file is larger than the limit of 20 MiB"
	// noFormatMessage is the fixed text of a 404 for a requested format the voucher does not offer.
	noFormatMessage = "Lexware has no file in the requested format for this voucher, or does not show it to this API key"
)

const (
	downloadVoucherTypes = `["invoice","credit_note","delivery_note","dunning","down_payment_invoice",` +
		`"order_confirmation","quotation"]`
	downloadOutputSchema = `{"type":"object","properties":{"id":{"type":"string"},"size":{"type":"integer"},` +
		`"sha256":{"type":"string"}},"required":["id","size","sha256"],"additionalProperties":false}`
)

func downloadPathArgument() capability.Argument {
	a := localfile.DownloadPathArgument()
	a.Required = true
	return a
}

var downloadFields = []capability.Field{
	{Name: "id", Description: "Identifier of the voucher or file that was written"},
	{Name: "size", Description: "Size of the written file in bytes"},
	{Name: "sha256", Description: "SHA-256 of the written content as hex"},
}

var documentsDownload = capability.Descriptor{
	ID:      Provider + ".documents.download",
	Version: 1,
	Title:   "Download a Lexware sales voucher document to a local path",
	Description: "Write the final document of one finalized sales voucher to local_path, in a directory the " +
		"connection releases for writing; format xml selects the XML of an XRechnung. Lexware generates the PDF of " +
		"an XRechnung only as a preview, which is not a valid e-invoice. The content is never returned, only its " +
		"identifier, size, and SHA-256. An existing local file is replaced only with confirmation, a transfer " +
		"larger than 20 MiB or incomplete leaves no file, and a draft has no document",
	Tags:       []string{"lexware", "documents", "download", "local", "accounting"},
	Risk:       lexwareReadRisk,
	Provider:   Provider,
	LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"voucher_type":{"type":"string","enum":` +
		downloadVoucherTypes + `},"id":` + uuidSchema + `,"format":{"type":"string","enum":["default","pdf","xml"]},` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["voucher_type","id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(downloadOutputSchema),
	Arguments: []capability.Argument{
		{Name: "voucher_type", Description: "invoice, credit_note, delivery_note, dunning, down_payment_invoice, " +
			"order_confirmation, or quotation", Required: true},
		{Name: "id", Description: "Sales voucher identifier as a UUID, as returned by lexware.voucherlist.list", Required: true},
		{Name: "format", Description: "default (Lexware's own choice), pdf, or xml; default is the default"},
		downloadPathArgument(),
	},
	Fields: downloadFields,
	Examples: []capability.Example{{Description: "Write the PDF of an invoice to a released local directory",
		Arguments: json.RawMessage(`{"voucher_type":"invoice","id":"11111111-2222-3333-4444-555555555555",` +
			`"format":"pdf","local_path":"~/downloads/invoice.pdf"}`)}},
}

var filesDownload = capability.Descriptor{
	ID:      Provider + ".files.download",
	Version: 1,
	Title:   "Download a Lexware bookkeeping voucher file to a local path",
	Description: "Write one file of a bookkeeping voucher to local_path, in a directory the connection releases " +
		"for writing, by the file identifier lexware.vouchers.get reports. The content is never returned, only its " +
		"identifier, size, and SHA-256. An existing local file is replaced only with confirmation, and a transfer " +
		"larger than 20 MiB or incomplete leaves no file",
	Tags:       []string{"lexware", "files", "download", "local", "bookkeeping", "accounting"},
	Risk:       capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: bookkeepingSensitivity},
	Provider:   Provider,
	LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,` +
		`"format":{"type":"string","enum":["default","pdf","xml","image"]},` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(downloadOutputSchema),
	Arguments: []capability.Argument{
		{Name: "id", Description: "File identifier as a UUID, from the files of lexware.vouchers.get", Required: true},
		{Name: "format", Description: "default (the original file), pdf, xml for an e-invoice, or image; default is the default"},
		downloadPathArgument(),
	},
	Fields: downloadFields,
	Examples: []capability.Example{{Description: "Write the original file of a bookkeeping voucher",
		Arguments: json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555","local_path":"~/downloads/receipt.pdf"}`)}},
}

// DownloadResult is what both download tools report: metadata of the written file, never its content.
type DownloadResult struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type downloadArguments struct {
	VoucherType string `json:"voucher_type"`
	ID          string `json:"id"`
	Format      string `json:"format"`
	LocalPath   string `json:"local_path"`
}

func invokeDocumentsDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "download document"
	return invokeDownload(ctx, resolved, secrets, red, raw, op, resourceVoucher, true, func(a downloadArguments) (string, bool) {
		prefix, ok := downloadPaths[a.VoucherType]
		return prefix, ok
	})
}

func invokeFilesDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "download file"
	return invokeDownload(ctx, resolved, secrets, red, raw, op, resourceFile, false, func(downloadArguments) (string, bool) {
		return "/v1/files/", true
	})
}

// invokeDownload validates every argument and prepares the local target before the credential is resolved,
// so an unusable request or a path outside the release is refused without secret access or provider I/O.
func invokeDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, op, resource string, draftable bool, prefix func(downloadArguments) (string, bool)) (any, error) {
	var input downloadArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	base, ok := prefix(input)
	if !ok {
		return nil, providerError(op, "the voucher type is not supported")
	}
	if input.Format == "" {
		input.Format = "default"
	}
	accept, ok := downloadAccepts[input.Format]
	// Only a sales voucher document is a draft candidate, and it has no image representation.
	if !ok || (draftable && input.Format == "image") {
		return nil, providerError(op, "the format is not supported")
	}
	if !validUUID(input.ID) {
		return nil, providerError(op, "the identifier must be a UUID")
	}
	download, err := localfile.CreateForDownload(ctx, resolved, input.LocalPath)
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = download.Abort()
		}
	}()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.fetchFile(ctx, op, resource, base+url.PathEscape(input.ID), accept, input.Format != "default", draftable, download); err != nil {
		return nil, err
	}
	done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	return &DownloadResult{ID: input.ID, Size: download.Size(), SHA256: sum}, nil
}

// fetchFile streams one file into download. Only an answer of 200 counts, and the announced size is checked
// against the written content.
func (c *Client) fetchFile(ctx context.Context, op, resource, path, accept string, explicit, draftable bool,
	download *localfile.Download) error {
	if err := c.verifyOrganization(ctx, op); err != nil {
		return err
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Lexware", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateway+path, nil)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", accept)
	response, err := c.http.Do(req)
	if err != nil {
		return provider.Transport(op, "Lexware", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return downloadStatusError(op, resource, response.StatusCode, explicit, draftable)
	}
	if response.ContentLength > maxDownloadBytes {
		return providerError(op, tooLargeMessage)
	}
	if response.ContentLength >= 0 {
		if err := download.ExpectSize(response.ContentLength); err != nil {
			return err
		}
	}
	// One byte beyond the limit is enough to see an answer that is longer than it announced.
	if _, err := download.ReadFrom(io.LimitReader(response.Body, maxDownloadBytes+1)); err != nil {
		return transferError(op, err)
	}
	if download.Size() > maxDownloadBytes {
		return providerError(op, tooLargeMessage)
	}
	return nil
}

// downloadStatusError adds the download-specific statuses to statusError. A draft answers 409, and 406 when
// the voucher has no document; a 404 for an explicitly requested format may also mean the voucher has no
// such representation.
func downloadStatusError(op, resource string, status int, explicit, draftable bool) error {
	switch {
	case draftable && (status == http.StatusConflict || status == http.StatusNotAcceptable):
		return providerError(op, notFinalizedMessage)
	case status == http.StatusNotAcceptable && !draftable:
		return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: noFormatMessage}
	case status == http.StatusNotFound && explicit:
		return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: noFormatMessage}
	}
	return statusError(op, resource, status)
}

// transferError reports a failed transfer without any provider text. A local file problem keeps its own
// error, which never names the path.
func transferError(op string, err error) error {
	var integrity *localfile.IntegrityError
	var path *localfile.PathError
	if errors.As(err, &integrity) || errors.As(err, &path) {
		return err
	}
	return providerError(op, "the transfer ended before the file was complete")
}
