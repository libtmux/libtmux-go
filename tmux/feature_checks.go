package tmux

import "strings"

var (
	namedBufferMinimumVersion   = Version{raw: "3.4", major: 3, minor: 4}
	capturedShellMinimumVersion = Version{raw: "3.5", major: 3, minor: 5}
	patchedBreakPaneVersion     = Version{raw: "3.7a", major: 3, minor: 7}
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
	for _, item := range directCommandItems(arguments, commandList) {
		if len(item) != 0 {
			namedCleanup := false
			switch {
			case matchesNativeCommand(item[0], "delete-buffer", "deleteb", 3):
				namedCleanup = directCommandOptions(item, "b")['b']
			case matchesNativeCommand(item[0], "set-buffer", "setb", 5):
				flags := directCommandOptions(item, "btn")
				namedCleanup = flags['b'] && flags['n']
			case matchesNativeCommand(item[0], "split-window", "splitw", 2):
				if current.AtLeast(namedBufferMinimumVersion) &&
					!current.AtLeast(capturedShellMinimumVersion) &&
					directCommandOptions(item, "ceFlpt")['p'] {
					return &VersionTooLowError{
						Current: current, Minimum: capturedShellMinimumVersion,
						Subcommand: item[0], Feature: "percentage",
					}
				}
			case matchesNativeCommand(item[0], "break-pane", "breakp", 2):
				if raw && current.String() == "3.7" {
					return &VersionTooLowError{Current: current, Minimum: patchedBreakPaneVersion, Subcommand: item[0], Feature: "raw_break_pane"}
				}
			}
			if namedCleanup && !current.meetsMinimum(namedBufferMinimumVersion) {
				return &VersionTooLowError{Current: current, Minimum: namedBufferMinimumVersion, Subcommand: item[0], Feature: "named_buffer_identity"}
			}
		}
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
