package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	recoveryVersion  = 2
	maxRecoveryBytes = 64 << 10
)

type physicalIdentity struct {
	Device string `json:"device"`
	File   string `json:"file"`
}

type topologyNode struct {
	Position int              `json:"position"`
	Kind     string           `json:"kind"`
	Link     string           `json:"link,omitempty"`
	Identity physicalIdentity `json:"identity"`
}

type destinationBinding struct {
	Resolved string           `json:"resolved"`
	Nodes    []topologyNode   `json:"nodes"`
	Target   physicalIdentity `json:"target"`
	Mode     uint32           `json:"mode"`
}

type recoveryState struct {
	Version        int                `json:"version"`
	BackupSHA256   string             `json:"backup_sha256"`
	Backup         destinationBinding `json:"backup"`
	ExpectedSHA256 string             `json:"expected_sha256"`
	ExpectedMode   uint32             `json:"expected_mode"`
	OriginalMode   uint32             `json:"original_mode"`
	Destination    destinationBinding `json:"destination"`
}

type recoveryPlan struct {
	backup, state                 string
	backupResolved, stateResolved string
	backupParent, stateParent     parentBinding
	record                        recoveryState
	backupContents, stateContents []byte
	backupBinding, stateBinding   destinationBinding
	exists                        bool
	guard                         func() error
}

type parentBinding struct {
	Resolved string
	Identity physicalIdentity
}

func recoveryStatePath(c client) string { return backupPath(c) + ".json" }

func captureDestination(path string) (destinationBinding, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return destinationBinding{}, err
	}
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	remainder := strings.TrimPrefix(absolute[len(volume):], string(filepath.Separator))
	parts := strings.Split(remainder, string(filepath.Separator))
	current := volume + string(filepath.Separator)
	nodes := make([]topologyNode, 0, len(parts))
	for index, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return destinationBinding{}, fmt.Errorf("inspect destination topology: %w", err)
		}
		last := index == len(parts)-1
		if last && info.Mode().IsRegular() {
			continue
		}
		kind := "directory"
		link := ""
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
			link, err = os.Readlink(current)
			if err != nil {
				return destinationBinding{}, fmt.Errorf("read destination symlink: %w", err)
			}
		case !info.IsDir():
			return destinationBinding{}, errors.New("destination topology contains a non-directory")
		}
		identity, err := physicalIdentityAt(current, false)
		if err != nil {
			return destinationBinding{}, fmt.Errorf("identify destination topology: %w", err)
		}
		nodes = append(nodes, topologyNode{
			Position: index, Kind: kind, Link: link, Identity: identity,
		})
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return destinationBinding{}, fmt.Errorf("resolve destination: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return destinationBinding{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return destinationBinding{}, fmt.Errorf("inspect resolved destination: %w", err)
	}
	if !info.Mode().IsRegular() {
		return destinationBinding{}, errors.New("destination is not a regular file")
	}
	if !exactMode(info, info.Mode().Perm()) {
		return destinationBinding{}, errors.New("destination has unsupported special permission bits")
	}
	target, err := physicalIdentityAt(resolved, true)
	if err != nil {
		return destinationBinding{}, fmt.Errorf("identify resolved destination: %w", err)
	}
	return destinationBinding{
		Resolved: filepath.Clean(resolved), Nodes: nodes, Target: target,
		Mode: uint32(info.Mode().Perm()),
	}, nil
}

func readBoundFile(path string) ([]byte, destinationBinding, error) {
	return readBoundFileLimit(path, 0)
}

func readBoundFileLimit(
	path string,
	limit int64,
) ([]byte, destinationBinding, error) {
	before, err := captureDestination(path)
	if err != nil {
		return nil, destinationBinding{}, err
	}
	file, err := os.Open(before.Resolved)
	if err != nil {
		return nil, destinationBinding{}, err
	}
	if retainIfActiveLockAlias(file) {
		return nil, destinationBinding{}, errors.New("artifact aliases the active state lock")
	}
	reader := io.Reader(file)
	if limit > 0 {
		reader = io.LimitReader(file, limit)
	}
	contents, readErr := io.ReadAll(reader)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, destinationBinding{}, err
	}
	after, err := captureDestination(path)
	if err != nil {
		return nil, destinationBinding{}, err
	}
	if !sameBinding(before, after) {
		return nil, destinationBinding{}, errors.New("destination changed while it was read")
	}
	return contents, before, nil
}

func sameTopology(left, right destinationBinding) bool {
	if left.Resolved != right.Resolved || len(left.Nodes) != len(right.Nodes) {
		return false
	}
	for index := range left.Nodes {
		if left.Nodes[index] != right.Nodes[index] {
			return false
		}
	}
	return true
}

func sameBinding(left, right destinationBinding) bool {
	return sameTopology(left, right) && left.Target == right.Target && left.Mode == right.Mode
}

func recoveryFor(c client, contents []byte, binding destinationBinding) (recoveryPlan, error) {
	backup := backupPath(c)
	state := recoveryStatePath(c)
	backupExists, err := regularFileExists(backup)
	if err != nil {
		return recoveryPlan{}, fmt.Errorf("inspect backup: %w", err)
	}
	stateExists, err := regularFileExists(state)
	if err != nil {
		return recoveryPlan{}, fmt.Errorf("inspect recovery state: %w", err)
	}
	if backupExists != stateExists {
		return recoveryPlan{}, errors.New("backup and recovery state are incomplete")
	}
	if !backupExists {
		if err := preflightBackupDestination(backup); err != nil {
			return recoveryPlan{}, fmt.Errorf("preflight backup: %w", err)
		}
		if err := preflightBackupDestination(state); err != nil {
			return recoveryPlan{}, fmt.Errorf("preflight recovery state: %w", err)
		}
		backupResolved, backupParent, err := missingDestinationPath(backup)
		if err != nil {
			return recoveryPlan{}, fmt.Errorf("resolve backup destination: %w", err)
		}
		stateResolved, stateParent, err := missingDestinationPath(state)
		if err != nil {
			return recoveryPlan{}, fmt.Errorf("resolve recovery state destination: %w", err)
		}
		return recoveryPlan{
			backup: backup, state: state,
			backupResolved: backupResolved, stateResolved: stateResolved,
			backupParent: backupParent, stateParent: stateParent,
			record: recoveryState{
				Version:        recoveryVersion,
				BackupSHA256:   digest(contents),
				ExpectedSHA256: digest(contents),
				ExpectedMode:   binding.Mode,
				OriginalMode:   binding.Mode,
				Destination:    binding,
			},
			backupContents: append([]byte{}, contents...),
		}, nil
	}
	record, stateContents, stateBinding, err := readRecoveryState(state)
	if err != nil {
		return recoveryPlan{}, err
	}
	if stateBinding.Mode != 0o600 {
		return recoveryPlan{}, errors.New("recovery state mode changed")
	}
	backupContents, backupBinding, err := readBoundFile(backup)
	if err != nil {
		return recoveryPlan{}, fmt.Errorf("read backup: %w", err)
	}
	if !sameBinding(backupBinding, record.Backup) ||
		digest(backupContents) != record.BackupSHA256 {
		return recoveryPlan{}, errors.New("backup does not match recovery state")
	}
	if !sameBinding(record.Destination, binding) {
		return recoveryPlan{}, errors.New("configuration topology or physical target changed")
	}
	if digest(contents) != record.ExpectedSHA256 || binding.Mode != record.ExpectedMode {
		return recoveryPlan{}, errors.New("configuration content or mode changed after the swap")
	}
	return recoveryPlan{
		backup: backup, state: state,
		backupResolved: backupBinding.Resolved, stateResolved: stateBinding.Resolved,
		record:         record,
		backupContents: backupContents, stateContents: stateContents,
		backupBinding: backupBinding, stateBinding: stateBinding,
		exists: true,
	}, nil
}

func missingDestinationPath(path string) (string, parentBinding, error) {
	if _, err := os.Lstat(path); err == nil {
		return "", parentBinding{}, errors.New("destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", parentBinding{}, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", parentBinding{}, err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", parentBinding{}, err
	}
	identity, err := physicalIdentityAt(parent, true)
	if err != nil {
		return "", parentBinding{}, err
	}
	parent = filepath.Clean(parent)
	return filepath.Clean(filepath.Join(parent, filepath.Base(path))), parentBinding{
		Resolved: parent, Identity: identity,
	}, nil
}

func validateMissingDestination(
	path, resolved string,
	parent parentBinding,
) error {
	if _, err := os.Lstat(path); err == nil {
		return errors.New("destination appeared after it was planned")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	current, err = filepath.Abs(current)
	if err != nil {
		return err
	}
	current = filepath.Clean(current)
	identity, err := physicalIdentityAt(current, true)
	if err != nil {
		return err
	}
	if current != parent.Resolved || identity != parent.Identity ||
		filepath.Join(current, filepath.Base(path)) != resolved {
		return errors.New("destination parent changed after it was planned")
	}
	return nil
}

func readRecoveryState(path string) (
	recoveryState,
	[]byte,
	destinationBinding,
	error,
) {
	contents, binding, err := readBoundFileLimit(path, maxRecoveryBytes+1)
	if err != nil {
		return recoveryState{}, nil, destinationBinding{}, err
	}
	if len(contents) > maxRecoveryBytes {
		return recoveryState{}, nil, destinationBinding{},
			errors.New("recovery state exceeds its size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(contents), maxRecoveryBytes+1))
	decoder.DisallowUnknownFields()
	var record recoveryState
	if err := decoder.Decode(&record); err != nil {
		return recoveryState{}, nil, destinationBinding{},
			fmt.Errorf("decode recovery state: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return recoveryState{}, nil, destinationBinding{},
			errors.New("recovery state has trailing data")
	}
	if record.Version != recoveryVersion ||
		record.BackupSHA256 == "" || record.ExpectedSHA256 == "" ||
		record.Backup.Resolved == "" || record.Destination.Resolved == "" ||
		record.Backup.Target == (physicalIdentity{}) ||
		record.Destination.Target == (physicalIdentity{}) ||
		record.ExpectedMode != record.Destination.Mode {
		return recoveryState{}, nil, destinationBinding{},
			errors.New("recovery state is incomplete or unsupported")
	}
	return record, contents, binding, nil
}

func encodeRecoveryState(record recoveryState) ([]byte, error) {
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxRecoveryBytes {
		return nil, errors.New("recovery state exceeds its size limit")
	}
	return encoded, nil
}

func writeRecoveryState(
	path string,
	record recoveryState,
	options ...stageOptions,
) ([]byte, destinationBinding, error) {
	encoded, err := encodeRecoveryState(record)
	if err != nil {
		return nil, destinationBinding{}, err
	}
	contents, binding, err := writeBoundFileExact(path, encoded, 0o600, options...)
	if !bytes.Equal(contents, encoded) || binding.Resolved != "" && binding.Mode != 0o600 {
		return contents, binding, errors.Join(
			err, errors.New("recovery state changed while it was written"),
		)
	}
	return contents, binding, err
}

func writeBoundFileExact(
	path string,
	contents []byte,
	mode os.FileMode,
	options ...stageOptions,
) ([]byte, destinationBinding, error) {
	staged, err := stageAtomicFile(path, contents, mode, options...)
	if err != nil {
		return nil, destinationBinding{}, err
	}
	defer staged.cleanup()
	published, publishErr := staged.publish()
	if !published {
		return nil, destinationBinding{}, publishErr
	}
	current, binding, err := readBoundFile(path)
	if err != nil {
		return nil, destinationBinding{}, errors.Join(publishErr, err)
	}
	if !bytes.Equal(current, contents) || binding.Mode != uint32(mode.Perm()) {
		return current, binding, errors.Join(
			publishErr, errors.New("file changed while it was written"),
		)
	}
	return current, binding, publishErr
}

func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func validateBoundContents(path string, expected []byte, binding destinationBinding) error {
	contents, current, err := readBoundFile(path)
	if err != nil {
		return err
	}
	if !sameBinding(current, binding) {
		return errors.New("configuration topology or physical target changed")
	}
	if !bytes.Equal(contents, expected) {
		return errors.New("configuration changed after it was planned")
	}
	return nil
}

func (r recoveryPlan) validateArtifacts() error {
	if !r.exists {
		return nil
	}
	if err := validateBoundContents(r.backup, r.backupContents, r.backupBinding); err != nil {
		return fmt.Errorf("backup changed after it was planned: %w", err)
	}
	if err := validateBoundContents(r.state, r.stateContents, r.stateBinding); err != nil {
		return fmt.Errorf("recovery state changed after it was planned: %w", err)
	}
	return nil
}

func (r recoveryPlan) replaceRecord(record recoveryState) (recoveryPlan, error) {
	if r.exists {
		if err := validateBoundContents(r.state, r.stateContents, r.stateBinding); err != nil {
			return r, fmt.Errorf("recovery state changed before update: %w", err)
		}
	}
	option := stageOptions{guard: r.guard}
	if r.exists {
		option.expectedContents = r.stateContents
		option.expectedBinding = &r.stateBinding
	}
	contents, binding, err := writeRecoveryState(r.state, record, option)
	if binding.Resolved != "" {
		r.record = record
		r.stateContents = contents
		r.stateBinding = binding
		r.exists = true
	}
	return r, err
}

func recoveryStateForConfiguration(
	record recoveryState,
	contents []byte,
	binding destinationBinding,
) recoveryState {
	record.ExpectedSHA256 = digest(contents)
	record.ExpectedMode = binding.Mode
	record.Destination = binding
	return record
}

func publishBoundConfiguration(
	path string,
	current []byte,
	binding destinationBinding,
	updated []byte,
	updatedMode uint32,
	recovery recoveryPlan,
	beforePublish func(recoveryState) error,
) (destinationBinding, recoveryPlan, bool, error) {
	if err := validateBoundContents(path, current, binding); err != nil {
		return binding, recovery, false, err
	}
	if err := recovery.validateArtifacts(); err != nil {
		return binding, recovery, false, err
	}
	staged, err := stageAtomicFile(
		binding.Resolved, updated, os.FileMode(updatedMode), stageOptions{
			guard: recovery.guard, expectedContents: current,
			expectedBinding: &binding,
		},
	)
	if err != nil {
		return binding, recovery, false, err
	}
	defer staged.cleanup()
	expected := binding
	expected.Target = staged.sourceBinding.Target
	expected.Mode = staged.sourceBinding.Mode
	previous := recovery.record
	next := recoveryStateForConfiguration(previous, updated, expected)
	recovery, err = recovery.replaceRecord(next)
	if err != nil {
		rollback := recovery
		rollback.guard = nil
		rolledBack, rollbackErr := rollback.replaceRecord(previous)
		return binding, rolledBack, false, errors.Join(err, rollbackErr)
	}
	rollbackState := func(cause error) (destinationBinding, recoveryPlan, bool, error) {
		rollback := recovery
		rollback.guard = nil
		rolledBack, rollbackErr := rollback.replaceRecord(previous)
		return binding, rolledBack, false, errors.Join(cause, rollbackErr)
	}
	if beforePublish != nil {
		if err := beforePublish(next); err != nil {
			return rollbackState(err)
		}
	}
	if err := validateBoundContents(path, current, binding); err != nil {
		return rollbackState(err)
	}
	published, err := staged.publish()
	if err != nil {
		if !published {
			return rollbackState(err)
		}
		return expected, recovery, true, err
	}
	after, err := captureDestination(path)
	if err == nil && !sameBinding(after, expected) {
		err = errors.New("configuration binding changed during publish")
	}
	if err == nil {
		err = validateBoundContents(path, updated, expected)
	}
	return expected, recovery, true, err
}
