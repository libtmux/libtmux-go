package tmux

import "strings"

var (
	bufferDeleteNamedVersion = capabilitySince("buffer-delete-named")
	bufferRenameVersion      = capabilitySince("buffer-rename")
	capturedShellVersion     = workaroundFixedIn("run-shell-output-in-copy-mode")
	percentSizeFixedVersion  = workaroundFixedIn("split-window-percent-size")
	breakPaneFixedVersion    = workaroundFixedIn("break-pane-misnames-windows")
	nameSeparatorsFixed      = workaroundFixedIn("name-separators-rejected")
	selectLayoutFixed        = workaroundFixedIn("select-layout-invalid-string")
)

// checkCommandFeatures examines direct argv items. Nested tmux scripts and
// configured command aliases retain native semantics.
func (s Server) checkCommandFeatures(arguments []string, commandList, raw bool) error {
	current := Version{}
	if s.daemon != nil {
		current = s.daemon.version
	}
	if s.startupVersion != nil {
		current = *s.startupVersion
	}
	if s.connection != nil && current.raw == "" {
		// OpenControl checks the version before attaching, so a connection carries its
		// daemon. A fixture-built handle without one has no version to check;
		// it must not be refused with an empty Current.
		return nil
	}
	for _, item := range directCommandItems(arguments, commandList) {
		if len(item) != 0 {
			namedCleanup := false
			namedMinimum := bufferDeleteNamedVersion
			switch {
			case matchesNativeCommand(item[0], "delete-buffer", "deleteb", 3):
				namedCleanup = directCommandOptions(item, "b")['b']
			case matchesNativeCommand(item[0], "set-buffer", "setb", 5):
				namedMinimum = bufferRenameVersion
				flags := directCommandOptions(item, "btn")
				namedCleanup = flags['b'] && flags['n']
			case matchesNativeCommand(item[0], "split-window", "splitw", 2):
				if workaroundApplies("split-window-percent-size", current) &&
					directCommandOptions(item, "ceFlpt")['p'] {
					return &VersionTooLowError{
						Current: current, Minimum: percentSizeFixedVersion,
						Subcommand: item[0], Feature: "percentage",
					}
				}
			case matchesNativeCommand(item[0], "new-window", "neww", 5):
				if err := checkExact37Name(current, item, "cFnet", "n"); err != nil {
					return err
				}
			case matchesNativeCommand(item[0], "new-session", "new", 5):
				if err := checkExact37Name(current, item, "cEFfnsttxy", "ns"); err != nil {
					return err
				}
			case matchesNativeCommand(item[0], "rename-window", "renamew", 8):
				if err := checkExact37Positional(current, item, "t"); err != nil {
					return err
				}
			case matchesNativeCommand(item[0], "rename-session", "rename", 8):
				if err := checkExact37Positional(current, item, "t"); err != nil {
					return err
				}
			case matchesNativeCommand(item[0], "select-layout", "selectl", 8):
				if err := checkSelectLayout(current, item); err != nil {
					return err
				}
			case matchesNativeCommand(item[0], "refresh-client", "refresh", 3):
				// tmux 3.3a exits the server on -l when the client has a
				// terminal. -l is a plain flag; the valued ones are skipped so
				// that a value spelled "l" is not mistaken for it.
				if !current.meetsMinimum(serverClientsClipboardVersion) &&
					directCommandOptions(item, "ABCfFrt")['l'] {
					return &VersionTooLowError{
						Current: current, Minimum: serverClientsClipboardVersion,
						Subcommand: item[0], Feature: "clipboard_query",
					}
				}
			case matchesNativeCommand(item[0], "break-pane", "breakp", 2):
				if raw && workaroundApplies("break-pane-misnames-windows", current) {
					return &VersionTooLowError{Current: current, Minimum: breakPaneFixedVersion, Subcommand: item[0], Feature: "raw_break_pane"}
				}
			}
			if namedCleanup && !current.meetsMinimum(namedMinimum) {
				return &VersionTooLowError{Current: current, Minimum: namedMinimum, Subcommand: item[0], Feature: "named_buffer_identity"}
			}
		}
	}
	return nil
}

// checkSelectLayout refuses the select-layout forms that kill a tmux 3.3a
// server: an operand that is neither a preset (or unique prefix of one) nor a
// classic layout string, an empty operand, and -o. tmux frees an uninitialised
// pointer when its layout parser rejects an operand, and -o sends even a preset
// down that parser. tmux ignores the operand when -E, -n or -p is given, so
// those forms pass. A preset or unique prefix resolves the way tmux's
// layout_set_lookup does and never reaches the parser.
func checkSelectLayout(current Version, item []string) error {
	if !workaroundApplies("select-layout-invalid-string", current) {
		return nil
	}
	flags := directCommandOptions(item, "t")
	refuse := func(feature string) error {
		return &VersionTooLowError{
			Current: current, Minimum: selectLayoutFixed,
			Subcommand: item[0], Feature: feature,
		}
	}
	if flags['E'] || flags['n'] || flags['p'] {
		return nil
	}
	if flags['o'] {
		return refuse("select_layout_undo")
	}
	_, operands := directCommandValues(item, "t")
	if len(operands) == 0 {
		return nil
	}
	if _, err := validateLayout(operands[0], current); err != nil || operands[0] == "" {
		return refuse("layout_string")
	}
	return nil
}

// directCommandItems mirrors tmux's argv delimiter handling. It does not
// interpret shell text, nested command strings, or configured aliases.
func directCommandItems(arguments []string, commandList bool) [][]string {
	if !commandList {
		return [][]string{arguments}
	}
	var items [][]string
	var item []string
	for _, argument := range arguments {
		separator := false
		if strings.HasSuffix(argument, ";") {
			argument = argument[:len(argument)-1]
			if strings.HasSuffix(argument, "\\") {
				argument = argument[:len(argument)-1] + ";"
			} else {
				separator = true
			}
		}
		if !separator || argument != "" {
			item = append(item, argument)
		}
		if separator {
			items = append(items, item)
			item = nil
		}
	}
	return append(items, item)
}

func directCommandOptions(arguments []string, valued string) map[byte]bool {
	flags := make(map[byte]bool)
	for index := 1; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" || argument == "-" || !strings.HasPrefix(argument, "-") {
			break
		}
		for offset := 1; offset < len(argument); offset++ {
			flag := argument[offset]
			flags[flag] = true
			if strings.ContainsRune(valued, rune(flag)) {
				if offset == len(argument)-1 {
					index++
				}
				break
			}
		}
	}
	return flags
}

func matchesNativeCommand(verb, canonical, alias string, prefix int) bool {
	return verb == alias || (len(verb) >= prefix && len(verb) <= len(canonical) && canonical[:len(verb)] == verb)
}

// checkExact37Name refuses a window or session name tmux 3.7 mishandles: only
// that release rejects a colon or period in a name, so it is the only one
// checked, and 3.7a and newer are left to tmux.
func checkExact37Name(current Version, item []string, valued, names string) error {
	if !isExact37(current) {
		return nil
	}
	values, _ := directCommandValues(item, valued)
	for _, flag := range names {
		if name, ok := values[byte(flag)]; ok && strings.ContainsAny(name, ":.") {
			return &VersionTooLowError{
				Current: current, Minimum: nameSeparatorsFixed,
				Subcommand: item[0], Feature: "name_colon_or_period",
			}
		}
	}
	return nil
}

// checkExact37Positional applies the same refusal to the new name that
// rename-window and rename-session take as their last operand.
func checkExact37Positional(current Version, item []string, valued string) error {
	if !isExact37(current) {
		return nil
	}
	_, operands := directCommandValues(item, valued)
	if len(operands) > 0 && strings.ContainsAny(operands[len(operands)-1], ":.") {
		return &VersionTooLowError{
			Current: current, Minimum: nameSeparatorsFixed,
			Subcommand: item[0], Feature: "name_colon_or_period",
		}
	}
	return nil
}

func isExact37(current Version) bool {
	return workaroundApplies("name-separators-rejected", current)
}

// directCommandValues returns the value of each valued flag in one command
// item and the operands after its flags.
func directCommandValues(arguments []string, valued string) (map[byte]string, []string) {
	values := make(map[byte]string)
	index := 1
	for ; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			index++
			break
		}
		if argument == "-" || !strings.HasPrefix(argument, "-") {
			break
		}
		for offset := 1; offset < len(argument); offset++ {
			flag := argument[offset]
			if !strings.ContainsRune(valued, rune(flag)) {
				continue
			}
			if offset < len(argument)-1 {
				values[flag] = argument[offset+1:]
			} else if index+1 < len(arguments) {
				index++
				values[flag] = arguments[index]
			}
			break
		}
	}
	if index > len(arguments) {
		index = len(arguments)
	}
	return values, arguments[index:]
}
