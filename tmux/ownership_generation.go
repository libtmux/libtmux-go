package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

const ownerGenerationOption = "@libtmux_owner_generation"

// ErrInvalidOwnerGeneration identifies malformed reserved daemon metadata.
// Ownership accepts exactly 32 ASCII hexadecimal characters. Callers must not
// set, remove, or shadow @libtmux_owner_generation in any tmux option scope.
var ErrInvalidOwnerGeneration = errors.New("tmux: invalid reserved owner generation")

func ownershipIdentityFields() []formatField {
	return append(snapshotIdentityFields(), formatField{name: ownerGenerationOption})
}

func decodeOwnershipIdentity(values formatValues) (snapshotServerIdentity, error) {
	identity, err := decodeSnapshotIdentity("server", 0, values)
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	generation, err := requiredSnapshotValue("server", 0, values, ownerGenerationOption)
	if err != nil {
		return snapshotServerIdentity{}, ErrInvalidOwnerGeneration
	}
	decoded, err := hex.DecodeString(generation)
	if err != nil || len(decoded) != 16 {
		return snapshotServerIdentity{}, ErrInvalidOwnerGeneration
	}
	identity.ownerGeneration = generation
	return identity, nil
}

// Initialize, validate and use the token on one tmux connection. A creation ID
// cannot acquire a generation from a later connection to a replacement daemon.
func (s Server) ownershipCommand(ctx context.Context, start bool, arguments []string) (CommandResult, []byte, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return CommandResult{}, nil, err
	}
	token := hex.EncodeToString(nonce[:])
	invalid := &daemonCommandGuard{failure: "__libtmux_owner_generation_invalid_" + rand.Text() + "__"}
	operation, err := encodeControlCommand(arguments, false)
	if err != nil {
		return CommandResult{}, nil, err
	}
	// Repeating the class avoids braces that tmux would parse as format delimiters.
	valid := "#{m/r:^" + strings.Repeat("[0-9a-fA-F]", 32) + "$,#{" + ownerGenerationOption + "}}"
	commands := []string{
		"set-option", "-soq", ownerGenerationOption, token,
		";", "if-shell", "-F", valid, operation, invalid.failure,
	}
	if start {
		commands = append([]string{"start-server", ";"}, commands...)
	}
	result, raw, err := s.dispatch(ctx, true, commands...)
	if err == nil && invalid.rejected(result.ExitCode, result.Stderr) {
		return result, raw, ErrInvalidOwnerGeneration
	}
	return result, raw, err
}

func (s Server) acceptOwnership(ctx context.Context) (Server, error) {
	if s.daemon != nil && s.daemon.ownerGeneration != "" {
		if err := s.CheckAlive(ctx); err != nil {
			return Server{}, err
		}
		return s, nil
	}
	fields := ownershipIdentityFields()
	result, raw, err := s.ownershipCommand(ctx, false, []string{"display-message", "-p", formatTemplate(fields)})
	if err != nil {
		return Server{}, err
	}
	if result.ExitCode != 0 {
		return Server{}, newCommandError("accept ownership", result)
	}
	rows, err := decodeFormatRecords(raw, Version{}, fields)
	if err != nil {
		return Server{}, err
	}
	if len(rows) != 1 {
		return Server{}, ErrInvalidCommandOutput
	}
	identity, err := decodeOwnershipIdentity(rows[0])
	if err != nil {
		return Server{}, err
	}
	identity, err = s.normalizeSnapshotIdentityVersion(ctx, identity)
	if err != nil {
		return Server{}, err
	}
	return s.withDaemon(identity), nil
}
