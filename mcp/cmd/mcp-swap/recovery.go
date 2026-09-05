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
	recoveryVersion  = 1
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
}

type recoveryState struct {
	Version      int                `json:"version"`
	BackupSHA256 string             `json:"backup_sha256"`
	Destination  destinationBinding `json:"destination"`
}

type recoveryPlan struct {
	backup string
	state  string
	record recoveryState
	exists bool
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
	target, err := physicalIdentityAt(resolved, true)
	if err != nil {
		return destinationBinding{}, fmt.Errorf("identify resolved destination: %w", err)
	}
	return destinationBinding{
		Resolved: filepath.Clean(resolved), Nodes: nodes, Target: target,
	}, nil
}

func readBoundFile(path string) ([]byte, destinationBinding, error) {
	before, err := captureDestination(path)
	if err != nil {
		return nil, destinationBinding{}, err
	}
	contents, err := os.ReadFile(before.Resolved)
	if err != nil {
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
	return sameTopology(left, right) && left.Target == right.Target
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
		return recoveryPlan{
			backup: backup,
			state:  state,
			record: recoveryState{
				Version:      recoveryVersion,
				BackupSHA256: digest(contents),
				Destination:  binding,
			},
		}, nil
	}
	record, err := readRecoveryState(state)
	if err != nil {
		return recoveryPlan{}, err
	}
	backupContents, err := os.ReadFile(backup)
	if err != nil {
		return recoveryPlan{}, fmt.Errorf("read backup: %w", err)
	}
	if digest(backupContents) != record.BackupSHA256 {
		return recoveryPlan{}, errors.New("backup does not match recovery state")
	}
	if !sameBinding(record.Destination, binding) {
		return recoveryPlan{}, errors.New("configuration topology or physical target changed")
	}
	return recoveryPlan{backup: backup, state: state, record: record, exists: true}, nil
}

func readRecoveryState(path string) (recoveryState, error) {
	file, err := os.Open(path)
	if err != nil {
		return recoveryState{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxRecoveryBytes+1))
	decoder.DisallowUnknownFields()
	var record recoveryState
	if err := decoder.Decode(&record); err != nil {
		return recoveryState{}, fmt.Errorf("decode recovery state: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return recoveryState{}, errors.New("recovery state has trailing data")
	}
	if record.Version != recoveryVersion ||
		record.BackupSHA256 == "" || record.Destination.Resolved == "" {
		return recoveryState{}, errors.New("recovery state is incomplete or unsupported")
	}
	return record, nil
}

func writeRecoveryState(path string, record recoveryState) error {
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if len(encoded) > maxRecoveryBytes {
		return errors.New("recovery state exceeds its size limit")
	}
	return atomicWriteFile(path, encoded, 0o600)
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
