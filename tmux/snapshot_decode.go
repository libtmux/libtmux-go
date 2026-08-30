package tmux

import "strconv"

func decodeClientAttachment(record int, values formatValues) (clientAttachment, error) {
	var attachment clientAttachment
	sessionID, hasSession := nonemptySnapshotValue(values, "session_id")
	if !hasSession {
		for _, field := range []string{"window_id", "window_index", "pane_id"} {
			if value, ok := nonemptySnapshotValue(values, field); ok {
				return clientAttachment{}, newSnapshotDecodeError(
					"client",
					record,
					field,
					value,
					"attachment field requires an attached session",
				)
			}
		}
		return attachment, nil
	}
	attachment.sessionID = SessionID(sessionID)
	attachment.hasSession = true
	if err := validateSnapshotIdentifier("client", record, "session_id", sessionID, '$'); err != nil {
		return clientAttachment{}, err
	}

	windowID, hasWindow := nonemptySnapshotValue(values, "window_id")
	windowIndexText, hasWindowIndex := nonemptySnapshotValue(values, "window_index")
	if hasWindow != hasWindowIndex {
		return clientAttachment{}, newSnapshotDecodeError(
			"client",
			record,
			"window_id/window_index",
			windowID+"/"+windowIndexText,
			"both attached-window fields must be present",
		)
	}
	if hasWindow {
		if err := validateSnapshotIdentifier("client", record, "window_id", windowID, '@'); err != nil {
			return clientAttachment{}, err
		}
		windowIndex, err := parseSnapshotIndex("client", record, "window_index", windowIndexText)
		if err != nil {
			return clientAttachment{}, err
		}
		attachment.windowID = WindowID(windowID)
		attachment.windowIndex = windowIndex
		attachment.hasWindow = true
	}

	paneID, hasPane := nonemptySnapshotValue(values, "pane_id")
	if hasPane && !attachment.hasWindow {
		return clientAttachment{}, newSnapshotDecodeError(
			"client",
			record,
			"pane_id",
			paneID,
			"attached pane requires an attached window",
		)
	}
	if hasPane {
		if err := validateSnapshotIdentifier("client", record, "pane_id", paneID, '%'); err != nil {
			return clientAttachment{}, err
		}
		attachment.paneID = PaneID(paneID)
		attachment.hasPane = true
	}
	return attachment, nil
}

type snapshotIdentityValidator struct {
	version  Version
	expected *snapshotServerIdentity
	observed *snapshotServerIdentity
}

func (v *snapshotIdentityValidator) validate(object string, record int, values formatValues) error {
	if values.tmuxVersion().String() != v.version.String() {
		return newSnapshotDecodeError(
			object,
			record,
			"version",
			values.tmuxVersion().String(),
			"decoder version differs from snapshot version "+v.version.String(),
		)
	}
	identity, err := decodeSnapshotIdentity(object, record, values)
	if err != nil {
		return err
	}
	if identity.version.String() != v.version.String() {
		return newSnapshotDecodeError(
			object,
			record,
			"version",
			identity.version.String(),
			"row version differs from snapshot version "+v.version.String(),
		)
	}
	baseline := v.expected
	if baseline == nil {
		baseline = v.observed
	}
	if baseline != nil && !sameSnapshotIdentity(*baseline, identity) {
		return newSnapshotDecodeError(
			object,
			record,
			"server_identity",
			formatSnapshotIdentity(identity),
			"row was produced by a different tmux server",
		)
	}
	if v.observed == nil {
		v.observed = &identity
	}
	return nil
}

func decodeSnapshotIdentity(
	object string,
	record int,
	values formatValues,
) (snapshotServerIdentity, error) {
	rawVersion, err := requiredSnapshotValue(object, record, values, "version")
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	version, err := ParseVersion(rawVersion)
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	pid, err := requiredSnapshotDecimal(object, record, values, "pid")
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	startTime, err := requiredSnapshotDecimal(object, record, values, "start_time")
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	socketPath, err := requiredSnapshotValue(object, record, values, "socket_path")
	if err != nil {
		return snapshotServerIdentity{}, err
	}
	return snapshotServerIdentity{
		version:    version,
		pid:        pid,
		startTime:  startTime,
		socketPath: socketPath,
	}, nil
}

func requiredSnapshotDecimal(
	object string,
	record int,
	values formatValues,
	field string,
) (string, error) {
	value, err := requiredSnapshotValue(object, record, values, field)
	if err != nil {
		return "", err
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return "", newSnapshotDecodeError(
				object,
				record,
				field,
				value,
				"expected a nonnegative decimal value",
			)
		}
	}
	return value, nil
}

func requiredSnapshotValue(
	object string,
	record int,
	values formatValues,
	field string,
) (string, error) {
	value, ok := nonemptySnapshotValue(values, field)
	if ok {
		return value, nil
	}
	return "", newSnapshotDecodeError(object, record, field, value, "required nonempty field is absent")
}

func requiredSnapshotIdentifier(
	object string,
	record int,
	values formatValues,
	field string,
	sigil byte,
) (string, error) {
	value, err := requiredSnapshotValue(object, record, values, field)
	if err != nil {
		return "", err
	}
	if err := validateSnapshotIdentifier(object, record, field, value, sigil); err != nil {
		return "", err
	}
	return value, nil
}

func validateSnapshotIdentifier(object string, record int, field, value string, sigil byte) error {
	if len(value) < 2 || value[0] != sigil {
		return newSnapshotDecodeError(object, record, field, value, "invalid tmux identifier sigil")
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return newSnapshotDecodeError(object, record, field, value, "identifier body is not decimal")
		}
	}
	return nil
}

func requiredSnapshotIndex(
	object string,
	record int,
	values formatValues,
	field string,
) (int, error) {
	value, err := requiredSnapshotValue(object, record, values, field)
	if err != nil {
		return 0, err
	}
	return parseSnapshotIndex(object, record, field, value)
}

func parseSnapshotIndex(object string, record int, field, value string) (int, error) {
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 {
		return 0, newSnapshotDecodeError(object, record, field, value, "expected a nonnegative integer")
	}
	return index, nil
}

func nonemptySnapshotValue(values formatValues, field string) (string, bool) {
	value, ok := values.get(field)
	return value, ok && value != ""
}

func newSnapshotDecodeError(object string, record int, field, _ string, reason string) *SnapshotDecodeError {
	return &SnapshotDecodeError{
		Object: object,
		Record: record + 1,
		Field:  field,
		Value:  "[redacted]",
		Reason: reason,
	}
}
