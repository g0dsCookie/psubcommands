// Package psubcommands implements a simple way for a single command to have many
// subcommands, each of which takes arguments and so forth.
// Inspired by github.com/google/subcommands but extended for use with
// github.com/spf13/pflag (Posix compliant flags).
package psubcommands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"
)

// ExitStatus represents a Posix exit status that a subcommand
// expects to be returned to the shell.
type ExitStatus int

const (
	// ExitSuccess represents a successful execution.
	ExitSuccess ExitStatus = iota
	// ExitFailure represents a generic failure.
	ExitFailure
	// ExitUsageError represents a usage error on the command line.
	ExitUsageError
)

// Command represents a single subcommand.
type Command interface {
	// Name returns the name of the command.
	Name() string

	// Synopsis returns a short description of the command.
	// This should be less than one line.
	Synopsis() string

	// SetFlags adds the flags for this command to the specified set.
	SetFlags(*pflag.FlagSet)

	// Execute executes the command and returns the ExitStatus.
	Execute(ctx context.Context, f *pflag.FlagSet, args ...interface{}) ExitStatus
}

type commandGroup struct {
	name     string
	commands []Command
}

// Commander holds a set of commands.
type Commander struct {
	commands []*commandGroup
	topFlags *pflag.FlagSet
	name     string

	// Output specifies where a Commander should write its output.
	Output io.Writer
}

// NewCommander returns a new commander with specified name.
// Additionally this function accepts any arguments of the following type:
// map[string][]Command = Shortcut for multiple command registrations
// map[string]Command = Shortcut for single command registrations
// *pflag.FlagSet = Use your own *pflag.FlagSet for this Commander
// io.Writer = Use your own output instead of os.Stdout
func NewCommander(name string, args ...interface{}) *Commander {
	cdr := &Commander{
		commands: []*commandGroup{},
		topFlags: nil,
		name:     name,
		Output:   nil,
	}

	for _, arg := range args {
		switch v := arg.(type) {
		case map[string][]Command:
			for group, cmds := range v {
				cdr.Register(group, cmds...)
			}
		case map[string]Command:
			for group, cmd := range v {
				cdr.Register(group, cmd)
			}
		case *pflag.FlagSet:
			cdr.topFlags = v
		case io.Writer:
			cdr.Output = v
		}
	}

	if cdr.topFlags == nil {
		cdr.topFlags = pflag.NewFlagSet(name, pflag.ExitOnError)
	}

	if cdr.Output == nil {
		cdr.Output = os.Stdout
	}

	cdr.topFlags.Usage = func() { cdr.explain() }
	return cdr
}

// FlagSet returns the current *pflag.FlagSet used by this Commander.
func (c *Commander) FlagSet() *pflag.FlagSet { return c.topFlags }

// Register registers new Commands for the specified group.
func (c *Commander) Register(group string, cmds ...Command) {
	for _, g := range c.commands {
		if g.name == group {
			g.commands = append(g.commands, cmds...)
			return
		}
	}
	c.commands = append(c.commands, &commandGroup{
		name:     group,
		commands: cmds,
	})
}

// Execute finds the correct subcommand, executes it and returns it ExitStatus.
// If the FlagSet wasn't parsed by the user, this will call *pflag.FlagSet.Parse(os.Args[1:]).
// This will return ExitUsageError if something went wrong while parsing the command line,
// like subcommand missing.
func (c *Commander) Execute(ctx context.Context, args ...interface{}) ExitStatus {
	if !c.topFlags.Parsed() {
		c.topFlags.Parse(os.Args[1:])
	}

	if c.topFlags.NArg() < 1 {
		c.topFlags.Usage()
		return ExitUsageError
	}

	name := c.topFlags.Arg(0)

	for _, group := range c.commands {
		for _, cmd := range group.commands {
			if name != cmd.Name() {
				continue
			}

			f := pflag.NewFlagSet(name, pflag.ContinueOnError)
			f.SetOutput(c.Output)
			cmd.SetFlags(f)
			if f.Parse(c.topFlags.Args()[1:]) != nil {
				return ExitUsageError
			}
			return cmd.Execute(ctx, f, args...)
		}
	}

	c.topFlags.Usage()
	return ExitUsageError
}

func (c *Commander) explain() {
	fmt.Fprintf(c.Output, "Usage: %s <flags> <subcommand> <subcommand args>\n\n", c.name)

	flags := c.topFlags.FlagUsages()
	if len(flags) > 0 {
		fmt.Fprintf(c.Output, "Arguments:\n%s\n", flags)
	}

	for _, v := range c.commands {
		if len(v.commands) == 0 {
			continue
		}

		buf := bytes.Buffer{}
		if len(v.name) == 0 {
			buf.WriteString("Subcommands:\n")
		} else {
			buf.WriteString(fmt.Sprintf("%s:\n", v.name))
		}

		for _, vv := range v.commands {
			buf.WriteString(fmt.Sprintf("\t%-15s    %s\n", vv.Name(), vv.Synopsis()))
		}
		buf.WriteRune('\n')
		c.Output.Write(buf.Bytes())
	}
}

func (c *Commander) explainCmd(cmd Command) {
	fmt.Fprintf(c.Output, "Usage: %s <flags> %s <subcommand flags>\n\n%s\n\n", c.name, cmd.Name(), cmd.Synopsis())

	f := pflag.NewFlagSet(cmd.Name(), pflag.ExitOnError)
	cmd.SetFlags(f)
	flags := f.FlagUsages()

	if len(flags) > 0 {
		fmt.Fprintf(c.Output, "Arguments:\n%s", flags)
	}
}

type helpCommand Commander

// Name of this command.
func (*helpCommand) Name() string { return "help" }

// Synopsis returns a short description of this command.
func (*helpCommand) Synopsis() string { return "describe subcommands and their syntax" }

// SetFlags adds the flags to the FlagSet.
func (*helpCommand) SetFlags(*pflag.FlagSet) {}

// Execute executs this command and returns it's ExitStatus.
func (h *helpCommand) Execute(_ context.Context, f *pflag.FlagSet, _ ...interface{}) ExitStatus {
	switch f.NArg() {
	case 0:
		(*Commander)(h).explain()
		return ExitSuccess

	case 1:
		arg := f.Arg(0)
		for _, group := range h.commands {
			for _, cmd := range group.commands {
				if arg != cmd.Name() {
					continue
				}
				(*Commander)(h).explainCmd(cmd)
				return ExitSuccess
			}
		}
		fmt.Fprintf(h.Output, "Subcommand %s not understood\n", arg)
	}

	f.Usage()
	return ExitUsageError
}

// RegisterHelpCommand registers the default help command to the specified group.
func (c *Commander) RegisterHelpCommand(group string) { c.Register(group, (*helpCommand)(c)) }

// DefaultCommander holds the packages default commander which is ready to use.
var DefaultCommander = NewCommander(os.Args[0])

// FlagSet returns the current *pflag.FlagSet of the DefaultCommander.
func FlagSet() *pflag.FlagSet { return DefaultCommander.topFlags }

// Register registers the given commands on the DefaultCommander.
func Register(group string, cmd ...Command) { DefaultCommander.Register(group, cmd...) }

// Execute finds the correct subcommand, executes it and returns it ExitStatus.
// If the FlagSet wasn't parsed by the user, this will call *pflag.FlagSet.Parse(os.Args[1:]).
// This will return ExitUsageError if something went wrong while parsing the command line,
// like subcommand missing.
func Execute(ctx context.Context, args ...interface{}) ExitStatus {
	return DefaultCommander.Execute(ctx, args...)
}

// RegisterHelpCommand registers the default help command to the specified group
// on the DefaultCommander.
func RegisterHelpCommand(group string) { DefaultCommander.RegisterHelpCommand(group) }
