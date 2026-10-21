package manage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	yaml "go.yaml.in/yaml/v3"
)

// The format of a file ExportConfig writes and PlanImport reads.
const (
	// TransferFormat identifies the file as a configuration export.
	TransferFormat = "qatlas-config-export"
	// TransferFormatVersion is the only version this build writes and reads. A later optional section
	// (such as one for secrets) raises it, so an older build refuses the file instead of dropping the section.
	TransferFormatVersion = 1
)

// maxTransferSize bounds what an import reads, far above any real configuration and far below memory
// trouble.
const maxTransferSize = 8 << 20

// transferFile is the file form: the sections of config.Config that are portable, behind a format marker.
// It is decoded strictly, so a section this build does not know stops the import.
type transferFile struct {
	Format        string                       `yaml:"format"`
	FormatVersion int                          `yaml:"format_version"`
	Services      map[string]config.Service    `yaml:"services,omitempty"`
	Credentials   map[string]config.Credential `yaml:"credentials,omitempty"`
	Connections   map[string]config.Connection `yaml:"connections,omitempty"`
	Defaults      *config.Defaults             `yaml:"defaults,omitempty"`
	ProviderNotes map[string]string            `yaml:"provider_notes,omitempty"`
}

// ImportRefusedError reports a file or a plan that was refused before anything was written. Its text has
// passed the service's redaction.
type ImportRefusedError struct {
	msg string
	Err error
}

func (e *ImportRefusedError) Error() string { return e.msg }

func (e *ImportRefusedError) Unwrap() error { return e.Err }

// refuse returns the redacted refusal with cause as its text.
func (s *Service) refuse(cause error) error {
	return &ImportRefusedError{msg: s.redact(cause.Error()), Err: cause}
}

// toConfig assembles the sections into a configuration of the service's providers, so the file is checked
// by the rules of a configuration. Entries are set through the Set methods, which normalize free text.
func (s *Service) toConfig(f *transferFile) (*config.Config, error) {
	cfg := s.NewConfig()
	for name, v := range f.Services {
		if err := cfg.SetService(name, v); err != nil {
			return nil, err
		}
	}
	for name, v := range f.Credentials {
		if err := cfg.SetCredential(name, v); err != nil {
			return nil, err
		}
	}
	for name, v := range f.Connections {
		if err := cfg.SetConnection(name, v); err != nil {
			return nil, err
		}
	}
	for provider, note := range f.ProviderNotes {
		if err := cfg.SetProviderNote(provider, note); err != nil {
			return nil, err
		}
	}
	if f.Defaults != nil {
		for domain, conn := range f.Defaults.Connections {
			if err := cfg.SetDefault(domain, conn); err != nil {
				return nil, err
			}
		}
		cfg.Defaults.SecretStore = f.Defaults.SecretStore
	}
	return cfg, nil
}

// readTransfer reads, decodes and validates the export file at path. The sections are validated as a
// configuration of their own, so every reference inside the file must resolve inside the file.
func (s *Service) readTransfer(path string) (*transferFile, *config.Config, error) {
	data, err := readLimited(path)
	if err != nil {
		return nil, nil, s.refuse(err)
	}
	// The marker is read before the strict decoding, so a file of another format or a later version says
	// so instead of naming an unknown key.
	var head struct {
		Format        string `yaml:"format"`
		FormatVersion int    `yaml:"format_version"`
	}
	if yaml.Unmarshal(data, &head) == nil {
		if head.Format != TransferFormat {
			return nil, nil, s.refuse(fmt.Errorf("%s is not a qatlas configuration export (format must be %s)", path, TransferFormat))
		}
		if head.FormatVersion != TransferFormatVersion {
			return nil, nil, s.refuse(fmt.Errorf("%s has format version %d, this build reads version %d", path, head.FormatVersion, TransferFormatVersion))
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f transferFile
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, s.refuse(fmt.Errorf("%s is empty", path))
		}
		return nil, nil, s.refuse(fmt.Errorf("%s cannot be read: %w", path, config.RedactDecodeError(err)))
	}
	if err := dec.Decode(new(transferFile)); !errors.Is(err, io.EOF) {
		return nil, nil, s.refuse(fmt.Errorf("%s must contain exactly one document", path))
	}
	if f.Format != TransferFormat || f.FormatVersion != TransferFormatVersion {
		return nil, nil, s.refuse(fmt.Errorf("%s is not a qatlas configuration export of format version %d", path, TransferFormatVersion))
	}
	// The former name of effects is a schema-migration aid of the configuration file, not part of this format.
	for _, name := range sortedNames(f.Connections) {
		if f.Connections[name].LegacyPermissions != nil {
			return nil, nil, s.refuse(fmt.Errorf("%s: connections.%s: unknown key permissions, use effects", path, name))
		}
	}
	cfg, err := s.toConfig(&f)
	if err != nil {
		return nil, nil, s.refuse(err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, s.refuse(&config.InvalidError{Path: path, Err: err})
	}
	return &f, cfg, nil
}

// readLimited reads a regular file of at most maxTransferSize bytes.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTransferSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTransferSize {
		return nil, fmt.Errorf("%s is larger than %d MiB", path, maxTransferSize>>20)
	}
	return data, nil
}

func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func addUnique(list []string, name string) []string {
	if slices.Contains(list, name) {
		return list
	}
	return append(list, name)
}
