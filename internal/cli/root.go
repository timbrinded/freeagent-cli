// Package cli implements the freeagent process interface. It contains no
// package-global command or output state, which makes every invocation fully
// injectable and testable.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/timbrinded/freeagent-cli/internal/catalog"
	"github.com/timbrinded/freeagent-cli/internal/output"
	"github.com/timbrinded/freeagent-cli/internal/protocol"
	"golang.org/x/term"
)

type Dependencies struct {
	Profiles     ProfileService
	Auth         AuthService
	API          APIService
	Catalog      *catalog.Catalog
	Build        BuildInfo
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	Now          func() time.Time
	IsTerminal   func(int) bool
	ReadPassword func(int) ([]byte, error)
}

type App struct {
	deps Dependencies
}

type globalOptions struct {
	profile    string
	output     string
	agent      bool
	noInput    bool
	readOnly   bool
	subdomain  string
	apiVersion string
	timeout    time.Duration
	format     output.Format
	profileSet Profile
}

type executionState struct {
	app           *App
	global        *globalOptions
	meta          protocol.Meta
	command       *cobra.Command
	preRunReached bool
}

func New(deps Dependencies) *App {
	if deps.Catalog == nil {
		deps.Catalog = catalog.Default()
	}
	if deps.Stdin == nil {
		deps.Stdin = strings.NewReader("")
	}
	if deps.Stdout == nil {
		deps.Stdout = io.Discard
	}
	if deps.Stderr == nil {
		deps.Stderr = io.Discard
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.IsTerminal == nil {
		deps.IsTerminal = term.IsTerminal
	}
	if deps.ReadPassword == nil {
		deps.ReadPassword = term.ReadPassword
	}
	if deps.Build.Version == "" {
		deps.Build.Version = "dev"
	}
	if deps.Build.Go == "" {
		deps.Build.Go = runtime.Version()
	}
	return &App{deps: deps}
}

func (a *App) Execute(ctx context.Context, args []string) int {
	root, state := a.newRoot()
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return int(protocol.ExitSuccess)
	}
	var commandErr *protocol.CommandError
	if !errors.As(err, &commandErr) && !state.preRunReached {
		commandErr = protocol.UsageError("the command or flags are invalid; use --help")
	} else {
		commandErr = protocol.AsCommandError(err)
	}
	if errors.Is(ctx.Err(), context.Canceled) && commandErr.Outcome != "unknown" && commandErr.Outcome != "applied" {
		commandErr = &protocol.CommandError{Code: protocol.ExitInterrupted, Kind: "interrupted", Message: "the command was interrupted", Outcome: "not_applied", Retryable: true}
	}
	if !commandErr.Silent {
		format := state.global.format
		if format == "" {
			if state.global.agent || state.global.output == string(output.FormatJSON) || state.global.output == string(output.FormatJSONL) || state.global.output == string(output.FormatRaw) {
				format = output.FormatJSON
			} else {
				format = output.FormatTable
			}
		}
		_ = (output.Renderer{Writer: a.deps.Stderr}).Error(format, commandErr.Detail(), state.meta)
	}
	return int(commandErr.Code)
}

func (a *App) newRoot() (*cobra.Command, *executionState) {
	global := &globalOptions{output: string(output.FormatTable)}
	state := &executionState{app: a, global: global}
	root := &cobra.Command{
		Use:           "freeagent",
		Short:         "Use the FreeAgent API safely from a terminal or an agent",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          noArgs,
		RunE:          showHelpOrUsage(state, "a command is required"),
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			state.preRunReached = true
			format, err := output.ParseFormat(global.output)
			if err != nil {
				return protocol.UsageError("%s", err)
			}
			if global.agent {
				global.noInput = true
				if flag := cmd.Flags().Lookup("output"); flag != nil && flag.Changed && format == output.FormatTable {
					return protocol.UsageError("--agent requires --output json, jsonl, or raw")
				}
				if format == output.FormatTable {
					format = output.FormatJSON
					global.output = string(format)
				}
			}
			global.format = format
			if global.timeout <= 0 {
				return protocol.UsageError("--timeout must be greater than zero")
			}
			if global.apiVersion != "" && !apiVersionPattern.MatchString(global.apiVersion) {
				return protocol.UsageError("--api-version must use YYYY-MM-DD")
			}
			if global.apiVersion != "" {
				if _, err := time.Parse(time.DateOnly, global.apiVersion); err != nil {
					return protocol.UsageError("--api-version must be a valid calendar date")
				}
			}
			if err := protocol.ValidatePracticeSubdomain(global.subdomain); err != nil {
				return protocol.UsageError("%s", err)
			}
			state.command = cmd
			return nil
		},
	}
	state.command = root
	root.SetIn(a.deps.Stdin)
	root.SetOut(a.deps.Stdout)
	root.SetErr(a.deps.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return protocol.UsageError("%s", err)
	})

	flags := root.PersistentFlags()
	flags.StringVarP(&global.profile, "profile", "p", "", "Profile name (the configured default is used when omitted)")
	flags.StringVarP(&global.output, "output", "o", string(output.FormatTable), "Output format: table, json, jsonl, or raw")
	flags.BoolVar(&global.agent, "agent", false, "Use the stable non-interactive agent protocol")
	flags.BoolVar(&global.noInput, "no-input", false, "Never prompt for input")
	flags.Bool("no-color", false, "Compatibility flag; output is always uncoloured")
	flags.BoolVar(&global.readOnly, "read-only", false, "Reject every FreeAgent mutation, including raw API requests")
	flags.StringVar(&global.subdomain, "subdomain", "", "Explicit Accountancy Practice client subdomain")
	flags.StringVar(&global.apiVersion, "api-version", "", "Override the profile's pinned API version in YYYY-MM-DD format")
	flags.DurationVar(&global.timeout, "timeout", 30*time.Second, "Maximum time for one API request")

	root.AddCommand(a.newVersionCommand(state))
	root.AddCommand(a.newProfileCommand(state))
	root.AddCommand(a.newAuthCommand(state))
	root.AddCommand(a.newCapabilitiesCommand(state))
	root.AddCommand(a.newSchemaCommand(state))
	root.AddCommand(a.newAPICommand(state))
	a.addCatalogCommands(root, state)
	root.InitDefaultCompletionCmd()
	return root, state
}

func (s *executionState) emit(data any, meta protocol.Meta) error {
	if meta.Command == "" && s.command != nil {
		meta.Command = strings.ReplaceAll(strings.TrimSpace(s.command.CommandPath()), " ", ".")
	}
	s.meta = meta
	if err := (output.Renderer{Writer: s.app.deps.Stdout}).Success(s.global.format, data, meta); err != nil {
		return protocol.InvariantError(fmt.Errorf("render output: %w", err))
	}
	return nil
}

func (s *executionState) resolveProfile(ctx context.Context) (Profile, error) {
	if s.global.noInput && s.global.profile == "" {
		return Profile{}, protocol.UsageError("--no-input and --agent require an explicit --profile for FreeAgent operations")
	}
	if s.app.deps.Profiles == nil {
		return Profile{}, &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "service_unavailable", Message: "profile support is not configured"}
	}
	profile, err := s.app.deps.Profiles.Resolve(ctx, s.global.profile)
	if err != nil {
		return Profile{}, &protocol.CommandError{Code: protocol.ExitAuth, Kind: "profile_not_found", Message: "the requested FreeAgent profile is not configured", Cause: err}
	}
	s.global.profileSet = profile
	if s.global.apiVersion != "" {
		profile.APIVersion = s.global.apiVersion
		s.global.profileSet = profile
	}
	s.meta.Profile = profile.Name
	s.meta.Environment = profile.Environment
	s.meta.APIVersion = profile.APIVersion
	return profile, nil
}

func profileMeta(profile Profile, command, subdomain string) protocol.Meta {
	meta := protocol.Meta{Command: command, Profile: profile.Name, Environment: profile.Environment, PracticeSubdomain: subdomain, APIVersion: profile.APIVersion}
	return meta
}

func (s *executionState) requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, s.global.timeout)
}

var apiVersionPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func noArgs(_ *cobra.Command, args []string) error {
	if len(args) != 0 {
		return protocol.UsageError("this command accepts no positional arguments")
	}
	return nil
}

func exactArgs(count int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != count {
			return protocol.UsageError("this command requires exactly %d positional argument(s)", count)
		}
		return nil
	}
}

func showHelpOrUsage(state *executionState, message string) func(*cobra.Command, []string) error {
	return func(command *cobra.Command, _ []string) error {
		if state.global.noInput || state.global.agent {
			return protocol.UsageError("%s; use --help to list available commands", message)
		}
		return command.Help()
	}
}
