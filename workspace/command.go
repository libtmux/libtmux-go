package workspace

import (
	"fmt"
	"math"
	"time"

	"gopkg.in/yaml.v3"
)

// Command is one command to run in a pane. YAML accepts either a bare string or
// a mapping, so "echo hi" and {cmd: echo hi, sleep_before: 2} are both commands.
type Command struct {
	// Command is the text typed into the pane.
	Command string `yaml:"cmd"`
	// SleepBefore delays this command without delaying the ones before it.
	// YAML writes it as a number of seconds, matching tmuxp, so 0.5 is half a
	// second; it is not a duration string, which is why it is decoded rather
	// than taken from this field.
	SleepBefore time.Duration `yaml:"-"`
	// SleepAfter delays the commands that follow this one, in the same units.
	SleepAfter time.Duration `yaml:"-"`
	// Enter reports whether to press Enter after the command. Nil presses it,
	// matching tmuxp, where omitting enter runs the command.
	Enter *Bool `yaml:"enter"`
}

// sends reports whether the command should be followed by Enter.
func (c Command) sends() bool { return c.Enter == nil || bool(*c.Enter) }

// UnmarshalYAML decodes a command written as a bare string or as a mapping.
func (c *Command) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var text string
		if err := node.Decode(&text); err != nil {
			return fmt.Errorf("%w: line %d: command is neither text nor a mapping",
				ErrInvalidWorkspace, node.Line)
		}
		c.Command = text
		return nil
	}

	if err := checkKnownFields(node, "cmd", "sleep_before", "sleep_after", "enter"); err != nil {
		return err
	}
	// Durations are written as seconds, so they are decoded as numbers rather
	// than through time.ParseDuration.
	type commandFields struct {
		Command     string  `yaml:"cmd"`
		SleepBefore float64 `yaml:"sleep_before"`
		SleepAfter  float64 `yaml:"sleep_after"`
		Enter       *Bool   `yaml:"enter"`
	}
	var fields commandFields
	if err := node.Decode(&fields); err != nil {
		return nested(node.Line, err)
	}
	before, err := seconds(fields.SleepBefore, node.Line, "sleep_before")
	if err != nil {
		return err
	}
	after, err := seconds(fields.SleepAfter, node.Line, "sleep_after")
	if err != nil {
		return err
	}
	c.Command = fields.Command
	c.SleepBefore = before
	c.SleepAfter = after
	c.Enter = fields.Enter
	return nil
}

// seconds converts a YAML sleep value to a duration, rejecting values tmux
// could never wait for.
func seconds(value float64, line int, field string) (time.Duration, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("%w: line %d: %s must be a nonnegative number of seconds",
			ErrInvalidWorkspace, line, field)
	}
	return time.Duration(value * float64(time.Second)), nil
}

// commandList decodes a field that accepts one command or a list of them.
func commandList(node yaml.Node, field string) ([]Command, error) {
	switch node.Kind {
	case 0:
		return nil, nil
	case yaml.ScalarNode, yaml.MappingNode:
		var command Command
		if err := command.UnmarshalYAML(&node); err != nil {
			return nil, err
		}
		if command.Command == "" && command.SleepBefore == 0 && command.SleepAfter == 0 {
			return nil, nil
		}
		return []Command{command}, nil
	case yaml.SequenceNode:
		var commands []Command
		if err := node.Decode(&commands); err != nil {
			return nil, nested(node.Line, err)
		}
		return commands, nil
	case yaml.DocumentNode, yaml.AliasNode:
		return nil, fmt.Errorf("%w: line %d: %s must be a command or a list",
			ErrInvalidWorkspace, node.Line, field)
	default:
		return nil, fmt.Errorf("%w: line %d: %s has an unknown YAML kind",
			ErrInvalidWorkspace, node.Line, field)
	}
}
