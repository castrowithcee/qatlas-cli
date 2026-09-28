package vault

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"filippo.io/age"
)

// tokensFile holds the agent tokens, the vault's second kind of content beside secrets.age. It exists only
// while the vault is encrypted: an unencrypted vault has no passphrase, so nobody could manage a token.
const tokensFile = "tokens.age"

// tokenSchemaVersion is the version of the JSON document tokens.age holds.
const tokenSchemaVersion = 1

// tokenMACLabel is the HKDF context the key that authenticates tokens.age is derived with. tokens.age is
// encrypted to the public recipient like every vault file, so anyone who can write files could encrypt a
// document of their own to it; the check value, keyed from the vault's private key, is what only the vault
// itself can write.
const tokenMACLabel = "qatlas agent tokens v1"

// TokenPrefix begins every agent token, so a person, and a secret scanner, can tell one apart.
const TokenPrefix = "qat_"

// tokenBytes is the randomness of one agent token: 256 bits.
const tokenBytes = 32

// maxTokenName bounds the name of an agent token.
const maxTokenName = 64

// Token is one agent token: a random value that lets an agent approve an open connection change without the
// passphrase, as long as the change reaches no further than one of its vorbild connections, named in Models.
// Expires is nil for a token that never expires. Value is as secret as a credential: it is shown only to a
// person who entered the passphrase, never logged, and never put in an error.
type Token struct {
	Name    string     `json:"name"`
	Value   string     `json:"value"`
	Models  []string   `json:"models"`
	Expires *time.Time `json:"expires,omitempty"`
	Created time.Time  `json:"created"`
}

// Expired reports whether t no longer works at now.
func (t Token) Expired(now time.Time) bool { return t.Expires != nil && !now.Before(*t.Expires) }

// tokenDocument is the JSON shape tokens.age holds. MAC is the check value of Tokens; see tokenMAC.
type tokenDocument struct {
	Schema int     `json:"schema"`
	Tokens []Token `json:"tokens"`
	MAC    string  `json:"mac"`
}

var (
	// ErrTokenExists reports a token name that is taken already.
	ErrTokenExists = errors.New("an agent token of this name exists already")
	// ErrTokenNotFound reports a token name the vault holds no token for.
	ErrTokenNotFound = errors.New("the vault holds no agent token of this name")
	// ErrNoModels reports a token without a vorbild connection, which could approve nothing.
	ErrNoModels = errors.New("an agent token needs at least one vorbild connection")
	// ErrTokenUnknown reports a presented agent token the vault does not hold: never created, revoked, or
	// mistyped. It never names the value.
	ErrTokenUnknown = errors.New("the agent token is not one this vault holds; it may have been revoked")
	// ErrTokenExpired reports a presented agent token whose expiry has passed.
	ErrTokenExpired = errors.New("the agent token has expired")
	// ErrTokensTampered reports a tokens.age the vault did not write itself: its check value does not match.
	// No token of it is used.
	ErrTokensTampered = errors.New("tokens.age was not written by this vault; no agent token of it is used")
)

// CheckTokenName reports whether name may name an agent token: letters, digits, '-', '_' or '.', starting
// and ending with a letter or a digit, at most 64 bytes, the rule every configuration name follows too.
func CheckTokenName(name string) error {
	const rule = "an agent token name must consist of letters, digits, '-', '_' or '.', start and end with a " +
		"letter or a digit, and be at most 64 characters long"
	if name == "" || len(name) > maxTokenName {
		return errors.New(rule)
	}
	alnum := func(b byte) bool { return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
	for i := 0; i < len(name); i++ {
		if b := name[i]; !alnum(b) && b != '-' && b != '_' && b != '.' {
			return errors.New(rule)
		}
	}
	if !alnum(name[0]) || !alnum(name[len(name)-1]) {
		return errors.New(rule)
	}
	return nil
}

func (v *Vault) tokensPath() string { return filepath.Join(v.dir, tokensFile) }

// tokenKey derives the key that authenticates tokens.age from the vault's private key.
func tokenKey(id *identity) ([]byte, error) {
	secret := []byte(id.key.String())
	defer clear(secret)
	key, err := hkdf.Key(sha256.New, secret, nil, tokenMACLabel, sha256.Size)
	if err != nil {
		return nil, errors.New("cannot derive the agent token key")
	}
	return key, nil
}

// tokenMAC is the check value of tokens: the hex HMAC-SHA256, keyed with tokenKey, of their JSON encoding.
func tokenMAC(id *identity, tokens []Token) (string, error) {
	key, err := tokenKey(id)
	if err != nil {
		return "", err
	}
	defer clear(key)
	data, err := json.Marshal(tokens)
	if err != nil {
		return "", err
	}
	defer clear(data)
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// loadTokens reads tokens.age with the identity of this vault. A missing file holds no token.
func (v *Vault) loadTokens() ([]Token, error) {
	if v.identity == nil {
		return nil, ErrNotUnlocked
	}
	data, err := readFile(v.tokensPath())
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", v.tokensPath(), err)
	}
	plain, err := decryptFrom(data, v.identity.key)
	if err != nil {
		return nil, ErrTokensTampered
	}
	defer clear(plain)
	var doc tokenDocument
	if err := json.Unmarshal(plain, &doc); err != nil {
		return nil, fmt.Errorf("cannot read %s: not a valid token document", v.tokensPath())
	}
	if doc.Schema != tokenSchemaVersion {
		return nil, fmt.Errorf("cannot read %s: unsupported token schema version %d, want %d",
			v.tokensPath(), doc.Schema, tokenSchemaVersion)
	}
	want, err := tokenMAC(v.identity, doc.Tokens)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal([]byte(want), []byte(doc.MAC)) {
		return nil, ErrTokensTampered
	}
	return doc.Tokens, nil
}

// writeTokens replaces tokens.age with tokens, authenticated for this vault.
func (v *Vault) writeTokens(tokens []Token) error {
	if v.identity == nil {
		return ErrNotUnlocked
	}
	if tokens == nil {
		tokens = []Token{}
	}
	mac, err := tokenMAC(v.identity, tokens)
	if err != nil {
		return err
	}
	plain, err := json.MarshalIndent(tokenDocument{Schema: tokenSchemaVersion, Tokens: tokens, MAC: mac}, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode %s: %w", v.tokensPath(), err)
	}
	defer clear(plain)
	data, err := encryptTo(string(plain), v.identity.recipient)
	if err != nil {
		return err
	}
	return writeFile(v.tokensPath(), data)
}

// tokensOf returns the tokens of the vault unlocked in this process, which only an encrypted one holds.
func (v *Vault) tokensOf() ([]Token, error) {
	if _, err := v.unlockedDocument(); err != nil {
		return nil, err
	}
	return v.loadTokens()
}

// Tokens returns every agent token of the vault unlocked in this process, sorted by name, values included.
// It fails with ErrNotUnlocked or ErrNotEncrypted where there are none to read.
func (v *Vault) Tokens() ([]Token, error) {
	tokens, err := v.tokensOf()
	if err != nil {
		return nil, err
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].Name < tokens[j].Name })
	return tokens, nil
}

// CreateToken creates an agent token named name with the vorbild connections models and an optional expiry,
// and writes it to the vault unlocked in this process. The value is 256 random bits. Whether each model
// names a connection is the caller's to check; the vault knows no configuration.
func (v *Vault) CreateToken(name string, models []string, expires *time.Time) (Token, error) {
	if err := CheckTokenName(name); err != nil {
		return Token{}, err
	}
	var unique []string
	for _, model := range models {
		if model != "" && !slices.Contains(unique, model) {
			unique = append(unique, model)
		}
	}
	if len(unique) == 0 {
		return Token{}, ErrNoModels
	}
	sort.Strings(unique)
	tokens, err := v.tokensOf()
	if err != nil {
		return Token{}, err
	}
	for _, token := range tokens {
		if token.Name == name {
			return Token{}, ErrTokenExists
		}
	}
	value, err := newTokenValue()
	if err != nil {
		return Token{}, err
	}
	token := Token{Name: name, Value: value, Models: unique, Created: time.Now().UTC()}
	if expires != nil {
		at := expires.UTC()
		token.Expires = &at
	}
	if err := v.writeTokens(append(tokens, token)); err != nil {
		return Token{}, err
	}
	return token, nil
}

// RevokeToken removes the agent token named name from the vault unlocked in this process. It fails with
// ErrTokenNotFound for a name the vault holds no token for.
func (v *Vault) RevokeToken(name string) error {
	tokens, err := v.tokensOf()
	if err != nil {
		return err
	}
	kept := make([]Token, 0, len(tokens))
	for _, token := range tokens {
		if token.Name != name {
			kept = append(kept, token)
		}
	}
	if len(kept) == len(tokens) {
		return ErrTokenNotFound
	}
	return v.writeTokens(kept)
}

// newTokenValue returns a fresh agent token: TokenPrefix and 256 bits from crypto/rand, base64url encoded.
func newTokenValue() (string, error) {
	b := make([]byte, tokenBytes)
	defer clear(b)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cannot read random bytes for an agent token: %w", err)
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// matchToken returns the token whose value is value, comparing every stored value in constant time and
// without stopping at a match, so the time taken tells nothing about which token, or how much of one,
// matched.
func matchToken(tokens []Token, value string) (Token, bool) {
	if value == "" {
		return Token{}, false
	}
	given := sha256.Sum256([]byte(value))
	match := -1
	for i := range tokens {
		stored := sha256.Sum256([]byte(tokens[i].Value))
		if subtle.ConstantTimeCompare(given[:], stored[:]) == 1 {
			match = i
		}
	}
	if match < 0 {
		return Token{}, false
	}
	return tokens[match], true
}

// OpenWithKey returns the encrypted vault in dir, the vault directory itself as Dir reports it, unlocked with
// key, its own private key, the way a vault process holds it, without the passphrase. It reads secrets.age as
// it is on disk now and leaves every pending entry where it is: the next unlock with the passphrase merges
// them, and a write through the returned vault keeps them pending.
func OpenWithKey(dir string, key *age.X25519Identity) (*Vault, error) {
	if key == nil {
		return nil, ErrNotUnlocked
	}
	v := &Vault{dir: dir}
	recipientPEM, err := readFile(v.recipientPath())
	if err != nil {
		if isNotExist(err) {
			return nil, ErrNotEncrypted
		}
		return nil, fmt.Errorf("cannot read %s: %w", v.recipientPath(), err)
	}
	recipient, err := age.ParseX25519Recipient(string(bytes.TrimSpace(recipientPEM)))
	if err != nil {
		return nil, fmt.Errorf("cannot read the vault recipient: %w", err)
	}
	if key.Recipient().String() != recipient.String() {
		return nil, errors.New("the vault key and the vault recipient do not match")
	}
	id := &identity{key: key, recipient: recipient}
	doc := newDocument()
	if data, err := readFile(v.secretsPath()); err == nil {
		plain, err := decryptFrom(data, key)
		if err != nil {
			return nil, fmt.Errorf("cannot decrypt %s: %w", v.secretsPath(), err)
		}
		decoded, err := decodeDocument(v.secretsPath(), plain)
		clear(plain)
		if err != nil {
			return nil, err
		}
		doc = decoded
	} else if !isNotExist(err) {
		return nil, fmt.Errorf("cannot read %s: %w", v.secretsPath(), err)
	}
	v.identity, v.doc, v.unlocked = id, doc, true
	return v, nil
}
