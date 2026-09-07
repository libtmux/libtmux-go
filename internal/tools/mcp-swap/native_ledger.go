package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	nativeLedgerVersion        = 1
	nativeLedgerImplementation = "go"
	nativeLedgerLimit          = 1 << 20
	nativeConfigLimit          = 16 << 20
)

type nativeLedger struct {
	NextSequence uint64                       `json:"next_sequence"`
	Entries      map[string]nativeLedgerEntry `json:"entries"`
}

type nativeLedgerEntry struct {
	Client       string             `json:"client"`
	Scope        configScope        `json:"scope"`
	Sequence     uint64             `json:"sequence"`
	Server       string             `json:"server"`
	Repository   string             `json:"repository"`
	ConfigPath   string             `json:"config_path"`
	BackupPath   string             `json:"backup_path"`
	OriginalMode uint32             `json:"original_mode"`
	Route        destinationBinding `json:"route"`
	Backup       nativeFileIdentity `json:"backup"`
	Expected     nativeFileIdentity `json:"expected_config"`
	Spec         nativeStoredSpec   `json:"spec"`
}

type nativeFileIdentity struct {
	Device string `json:"device"`
	File   string `json:"file"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type nativeStoredSpec struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type nativeLedgerEnvelope struct {
	Version        int          `json:"version"`
	Implementation string       `json:"implementation"`
	Checksum       string       `json:"checksum"`
	Payload        nativeLedger `json:"payload"`
}

type nativeStateSnapshot struct {
	Contents []byte
	Binding  destinationBinding
}

func nativeStateDirectory() string {
	root := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(root) {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Clean(filepath.Join(root, "libtmux-mcp-dev", "swap", "go"))
}

func nativeStatePath() string { return filepath.Join(nativeStateDirectory(), "state.json") }

func nativeStateKey(client string, scope configScope) string {
	return client + ":" + string(scope)
}

func loadNativeLedger() (nativeLedger, *nativeStateSnapshot, error) {
	path := nativeStatePath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nativeLedger{Entries: map[string]nativeLedgerEntry{}}, nil, nil
	}
	if err != nil {
		return nativeLedger{}, nil, err
	}
	if err := validateNativeStateDirectory(filepath.Dir(path)); err != nil {
		return nativeLedger{}, nil, err
	}
	links, linksErr := fileLinkCount(info)
	if !info.Mode().IsRegular() || !exactMode(info, 0o600) ||
		linksErr != nil || links != 1 || !fileOwnedByCurrentUser(info) {
		return nativeLedger{}, nil,
			errors.New("go recovery state must be an owned 0600 regular file with one link")
	}
	contents, binding, err := readBoundFileLimit(path, nativeLedgerLimit+1)
	if err != nil {
		return nativeLedger{}, nil, err
	}
	if len(contents) > nativeLedgerLimit {
		return nativeLedger{}, nil, errors.New("go recovery state exceeds its size limit")
	}
	ledger, err := decodeNativeLedger(contents)
	if err != nil {
		return nativeLedger{}, nil, err
	}
	return ledger, &nativeStateSnapshot{Contents: contents, Binding: binding}, nil
}

func decodeNativeLedger(contents []byte) (nativeLedger, error) {
	if !utf8.Valid(contents) {
		return nativeLedger{}, errors.New("go recovery state is not valid UTF-8")
	}
	if err := rejectDuplicateJSONNames(contents); err != nil {
		return nativeLedger{}, fmt.Errorf("decode Go recovery state: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var envelope nativeLedgerEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return nativeLedger{}, fmt.Errorf("decode Go recovery state: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nativeLedger{}, err
	}
	if envelope.Version != nativeLedgerVersion {
		return nativeLedger{}, fmt.Errorf("unsupported Go recovery state version %d", envelope.Version)
	}
	if envelope.Implementation != nativeLedgerImplementation {
		return nativeLedger{}, errors.New("go recovery state belongs to another implementation")
	}
	if err := validateNativeLedger(envelope.Payload); err != nil {
		return nativeLedger{}, err
	}
	payload, err := json.Marshal(envelope.Payload)
	if err != nil {
		return nativeLedger{}, err
	}
	if envelope.Checksum != digest(payload) {
		return nativeLedger{}, errors.New("go recovery state checksum mismatch")
	}
	return envelope.Payload, nil
}

func encodeNativeLedger(ledger nativeLedger) ([]byte, error) {
	if err := validateNativeLedger(ledger); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(ledger)
	if err != nil {
		return nil, err
	}
	envelope := nativeLedgerEnvelope{
		Version: nativeLedgerVersion, Implementation: nativeLedgerImplementation,
		Checksum: digest(payload), Payload: ledger,
	}
	contents, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, err
	}
	contents = append(contents, '\n')
	if len(contents) > nativeLedgerLimit {
		return nil, errors.New("go recovery state exceeds its size limit")
	}
	return contents, nil
}

func validateNativeLedger(ledger nativeLedger) error {
	if ledger.Entries == nil {
		return errors.New("go recovery ledger has no entries object")
	}
	if len(ledger.Entries) == 0 {
		return errors.New("go recovery ledger is unexpectedly empty")
	}
	known := map[string]bool{}
	for _, target := range knownClients(string(filepath.Separator)) {
		known[target.name] = true
	}
	sequences := map[uint64]bool{}
	for key, entry := range ledger.Entries {
		if !known[entry.Client] || nativeStateKey(entry.Client, entry.Scope) != key {
			return fmt.Errorf("go recovery entry %q has a noncanonical client or scope", key)
		}
		if entry.Client != "claude" && entry.Scope != scopeUser {
			return fmt.Errorf("go recovery entry %q has a non-user global scope", key)
		}
		if entry.Scope != scopeUser && entry.Scope != scopeProject {
			return fmt.Errorf("go recovery entry %q has an unknown scope", key)
		}
		if entry.Sequence >= ledger.NextSequence || sequences[entry.Sequence] {
			return fmt.Errorf("go recovery entry %q has an invalid sequence", key)
		}
		sequences[entry.Sequence] = true
		if entry.Server != serverName || !canonicalAbsolutePath(entry.Repository) ||
			!canonicalAbsolutePath(entry.ConfigPath) ||
			!canonicalAbsolutePath(entry.BackupPath) ||
			!canonicalAbsolutePath(entry.Route.Resolved) ||
			entry.BackupPath != nativeBackupPath(entry.Route.Resolved, entry.Sequence) {
			return fmt.Errorf("go recovery entry %q has invalid paths", key)
		}
		if entry.Route.Resolved == "" || entry.Route.Target == (physicalIdentity{}) ||
			entry.Backup.Device == "" || entry.Backup.File == "" ||
			entry.Expected.Device == "" || entry.Expected.File == "" ||
			entry.Backup.Size < 0 || entry.Expected.Size < 0 ||
			!validDigest(entry.Backup.SHA256) || !validDigest(entry.Expected.SHA256) ||
			entry.OriginalMode > 0o777 || entry.Route.Mode != entry.OriginalMode ||
			entry.Backup.Mode != 0o600 || entry.Expected.Mode != entry.OriginalMode ||
			strings.TrimSpace(entry.Spec.Command) == "" ||
			entry.Spec.Args == nil || entry.Spec.Env == nil ||
			!validNativeTopology(entry.Route.Nodes) {
			return fmt.Errorf("go recovery entry %q is incomplete", key)
		}
	}
	return nil
}

func canonicalAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && path == filepath.Clean(path) && !strings.ContainsRune(path, '\x00')
}

func validNativeTopology(nodes []topologyNode) bool {
	previous := -1
	for _, node := range nodes {
		if node.Position <= previous || node.Identity == (physicalIdentity{}) {
			return false
		}
		previous = node.Position
		switch node.Kind {
		case "directory":
			if node.Link != "" {
				return false
			}
		case "symlink":
			if node.Link == "" || strings.ContainsRune(node.Link, '\x00') {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func nativeIdentity(contents []byte, binding destinationBinding) nativeFileIdentity {
	return nativeFileIdentity{
		Device: binding.Target.Device, File: binding.Target.File, Mode: binding.Mode,
		Size: int64(len(contents)), SHA256: digest(contents),
	}
}

func (identity nativeFileIdentity) matches(contents []byte, binding destinationBinding) bool {
	return identity.Device == binding.Target.Device && identity.File == binding.Target.File &&
		identity.Mode == binding.Mode && identity.Size == int64(len(contents)) &&
		identity.SHA256 == digest(contents)
}

func (identity nativeFileIdentity) matchesContent(contents []byte, mode uint32) bool {
	return identity.Mode == mode && identity.Size == int64(len(contents)) &&
		identity.SHA256 == digest(contents)
}

func storedNativeSpec(spec processSpec) nativeStoredSpec {
	return nativeStoredSpec{
		Command: spec.command, Args: append([]string{}, spec.args...),
		Env: cloneStringMap(spec.env),
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	return maps.Clone(source)
}

func rejectDuplicateJSONNames(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if err := consumeUniqueJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func consumeUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if seen[name] {
				return fmt.Errorf("duplicate JSON member %q", name)
			}
			seen[name] = true
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim(map[json.Delim]rune{'{': '}', '[': ']'}[delimiter]) {
		return errors.New("mismatched JSON delimiter")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON has trailing data")
		}
		return err
	}
	return nil
}
