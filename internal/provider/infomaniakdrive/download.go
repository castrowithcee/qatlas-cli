package infomaniakdrive

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var filesDownload = capability.Descriptor{
	ID:      Provider + ".files.download",
	Version: 1,
	Title:   "Download an Infomaniak kDrive file to a local path",
	Description: "Write one file of a drive this connection may reach to local_path, in a directory the connection " +
		"releases for writing; a folder identifier writes Infomaniak's own zip archive of it. The content is never " +
		"returned, only its identifier, name, size, and SHA-256. An existing local file is replaced only with " +
		"confirmation, and an incomplete transfer leaves no file",
	Tags:       []string{"infomaniak", "kdrive", "files", "download", "local"},
	Risk:       filesReadRisk,
	Provider:   Provider,
	LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` + idSchema + `,` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["drive_id","file_id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":{"type":"integer"},` +
		`"file_id":{"type":"integer"},"name":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"}},` +
		`"required":["drive_id","file_id","name","size","sha256"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		driveIDArgument,
		{Name: "file_id", Description: "File or folder identifier below the same drive", Required: true},
		func() capability.Argument {
			a := localfile.DownloadPathArgument()
			a.Required = true
			return a
		}(),
	},
	Fields: []capability.Field{
		{Name: "drive_id", Description: "Drive the file was read from"},
		{Name: "file_id", Description: "File or folder that was written"},
		{Name: "name", Description: "Name Infomaniak reports for the file, untrusted data"},
		{Name: "size", Description: "Size of the written file in bytes"},
		{Name: "sha256", Description: "SHA-256 of the written content as hex"},
	},
	Examples: []capability.Example{{Description: "Write one file to a released local directory",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":2,"local_path":"~/downloads/report.pdf"}`)}},
}

// DownloadResult is what files.download reports: metadata of the written file, never its content.
type DownloadResult struct {
	DriveID int64  `json:"drive_id"`
	FileID  int64  `json:"file_id"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

type downloadArguments struct {
	DriveID   int64  `json:"drive_id"`
	FileID    int64  `json:"file_id"`
	LocalPath string `json:"local_path"`
}

func invokeFilesDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "download file"
	var input downloadArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectDrive(resolved, input.DriveID); err != nil {
		return nil, err
	}
	if input.FileID <= 0 {
		return nil, invalidRequest("file_id must be a positive integer")
	}
	// The local target is prepared before the credential is resolved, so a path outside the release is
	// refused without secret access or provider I/O.
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
	if err := client.verifyDriveAccount(ctx, op, input.DriveID); err != nil {
		return nil, err
	}
	entry, err := client.StatFile(ctx, input.DriveID, input.FileID)
	if err != nil {
		return nil, err
	}
	response, err := client.openDownload(ctx, op, input.DriveID, input.FileID)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 {
		if err := download.ExpectSize(response.ContentLength); err != nil {
			return nil, err
		}
	}
	if _, err := download.ReadFrom(response.Body); err != nil {
		return nil, transferError(op, err)
	}
	done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	return &DownloadResult{DriveID: input.DriveID, FileID: input.FileID, Name: entry.Name,
		Size: download.Size(), SHA256: sum}, nil
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
