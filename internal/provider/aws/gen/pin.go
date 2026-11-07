package gen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ModelDir is the directory of the extracts, the pin file, and the upstream LICENSE and NOTICE, relative to
// this package.
const ModelDir = "model"

// PinFile is the name of the pin file in ModelDir.
const PinFile = "pin.json"

// removedTraits are the traits the extractor drops. The generator needs neither: descriptions come from the
// curation tables, and generated tests derive their values from the schema. Both carry AWS example keys that
// must not enter the repository.
var removedTraits = []string{"smithy.api#documentation", "smithy.api#examples"}

// Pin records where the extracts come from and what they hash to.
type Pin struct {
	Source string `json:"source"`
	Commit string `json:"commit"`
	// RetrievedOn is the day the upstream files were downloaded. It is not the commit date, which a raw file
	// download does not carry.
	RetrievedOn  string         `json:"retrieved_on"`
	License      string         `json:"license"`
	Modification string         `json:"modification"`
	Files        []PinFileEntry `json:"files"`
}

// PinFileEntry is one pinned model: the upstream file and the extract derived from it.
type PinFileEntry struct {
	Service        string `json:"service"`
	UpstreamPath   string `json:"upstream_path"`
	UpstreamSHA256 string `json:"upstream_sha256"`
	Extract        string `json:"extract"`
	ExtractSHA256  string `json:"extract_sha256"`
}

var (
	commitForm  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestForm  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	dateForm    = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	extractForm = regexp.MustCompile(`^[a-z0-9-]+\.json$`)
)

// LoadPin reads and checks the pin file of dir.
func LoadPin(dir string) (Pin, error) {
	data, err := os.ReadFile(filepath.Join(dir, PinFile))
	if err != nil {
		return Pin{}, err
	}
	var p Pin
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return Pin{}, fmt.Errorf("pin file: %w", err)
	}
	if !commitForm.MatchString(p.Commit) || !dateForm.MatchString(p.RetrievedOn) || p.Source == "" ||
		p.License == "" || p.Modification == "" || len(p.Files) == 0 {
		return Pin{}, errors.New("pin file: source, commit, retrieved_on, license, modification and files are required")
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		if seen[f.Service] || f.Service == "" || f.UpstreamPath == "" || !extractForm.MatchString(f.Extract) ||
			!digestForm.MatchString(f.UpstreamSHA256) || !digestForm.MatchString(f.ExtractSHA256) {
			return Pin{}, fmt.Errorf("pin file: entry %q is incomplete or repeated", f.Service)
		}
		seen[f.Service] = true
	}
	return p, nil
}

// Model returns the pin entry of a service model.
func (p Pin) Model(service string) (PinFileEntry, bool) {
	for _, f := range p.Files {
		if f.Service == service {
			return f, true
		}
	}
	return PinFileEntry{}, false
}

// encodePin writes the pin file in its one canonical form.
func encodePin(p Pin) ([]byte, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ReadExtract returns the extract of a service after checking it against its pinned hash.
func ReadExtract(dir string, p Pin, service string) ([]byte, error) {
	entry, ok := p.Model(service)
	if !ok {
		return nil, fmt.Errorf("model %q is not pinned", service)
	}
	data, err := os.ReadFile(filepath.Join(dir, entry.Extract))
	if err != nil {
		return nil, err
	}
	if got := digest(data); got != entry.ExtractSHA256 {
		return nil, fmt.Errorf("extract %s has SHA-256 %s, the pin file says %s", entry.Extract, got, entry.ExtractSHA256)
	}
	return data, nil
}

// Extract derives the extract from an upstream model file: it checks the upstream hash, drops the removed
// traits wherever they appear, and re-encodes the rest in member order with fixed indentation.
func Extract(upstream []byte, wantSHA256 string) ([]byte, error) {
	if got := digest(upstream); got != wantSHA256 {
		return nil, fmt.Errorf("upstream file has SHA-256 %s, the pin file says %s", got, wantSHA256)
	}
	root, err := decodeOrdered(upstream)
	if err != nil {
		return nil, fmt.Errorf("upstream file is not JSON: %w", err)
	}
	if _, ok := root.(*object); !ok {
		return nil, errors.New("upstream file is not a Smithy JSON AST")
	}
	dropTraits(root)
	return encodeOrdered(root)
}

// dropTraits removes the removed traits from every "traits" object in the tree.
func dropTraits(v any) {
	switch t := v.(type) {
	case *object:
		if traits := t.obj("traits"); traits != nil {
			for _, name := range removedTraits {
				traits.remove(name)
			}
		}
		for _, k := range t.keys {
			dropTraits(t.vals[k])
		}
	case []any:
		for _, item := range t {
			dropTraits(item)
		}
	}
}

// UpdateExtracts is the model update: for every pinned model it reads the upstream file from upstreamDir
// (laid out as in api-models-aws), writes the extract into dir, and records the extract hash in the pin
// file. The upstream hashes and the commit in the pin file are set by hand first, so an update is a
// deliberate change.
func UpdateExtracts(dir, upstreamDir string) error {
	p, err := loadPinForUpdate(dir)
	if err != nil {
		return err
	}
	for i, f := range p.Files {
		upstream, err := os.ReadFile(filepath.Join(upstreamDir, filepath.FromSlash(f.UpstreamPath)))
		if err != nil {
			return err
		}
		extract, err := Extract(upstream, f.UpstreamSHA256)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Service, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f.Extract), extract, 0o644); err != nil {
			return err
		}
		p.Files[i].ExtractSHA256 = digest(extract)
	}
	data, err := encodePin(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, PinFile), data, 0o644)
}

// loadPinForUpdate reads the pin file without requiring extract hashes, which the update writes.
func loadPinForUpdate(dir string) (Pin, error) {
	data, err := os.ReadFile(filepath.Join(dir, PinFile))
	if err != nil {
		return Pin{}, err
	}
	var p Pin
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return Pin{}, fmt.Errorf("pin file: %w", err)
	}
	for _, f := range p.Files {
		if !digestForm.MatchString(f.UpstreamSHA256) || !extractForm.MatchString(f.Extract) {
			return Pin{}, fmt.Errorf("pin file: entry %q needs upstream_sha256 and extract", f.Service)
		}
	}
	return p, nil
}
