package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/timbrinded/freeagent-cli/internal/catalog"
	"github.com/timbrinded/freeagent-cli/internal/config"
	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

func (a *App) newVersionCommand(state *executionState) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show build and protocol versions",
		Args:  noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			data := struct {
				BuildInfo
				ProtocolVersion string `json:"protocol_version"`
			}{BuildInfo: a.deps.Build, ProtocolVersion: protocol.Version}
			return state.emit(data, protocol.Meta{Command: "version"})
		},
	}
}

func (a *App) newProfileCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{Use: "profile", Short: "Manage local FreeAgent profiles", Args: noArgs, RunE: showHelpOrUsage(state, "a profile command is required")}
	command.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.deps.Profiles == nil {
				return unavailable("profile")
			}
			profiles, err := a.deps.Profiles.List(cmd.Context())
			if err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "configuration_error", Message: "the profile configuration could not be read", Cause: err}
			}
			sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
			return state.emit(profiles, protocol.Meta{Command: "profile.list", Items: len(profiles)})
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show one profile without secrets",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile, err := state.resolveProfile(cmd.Context())
			if err != nil {
				return err
			}
			return state.emit(profile, profileMeta(profile, "profile.show", state.global.subdomain))
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "use NAME",
		Short: "Select the default profile",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.deps.Profiles == nil {
				return unavailable("profile")
			}
			if err := a.deps.Profiles.SetDefault(cmd.Context(), args[0]); err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "configuration_error", Message: "the default profile could not be changed", Cause: err}
			}
			profile, err := a.deps.Profiles.Resolve(cmd.Context(), args[0])
			if err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "profile_not_found", Message: "the requested FreeAgent profile is not configured", Cause: err}
			}
			return state.emit(profile, profileMeta(profile, "profile.use", ""))
		},
	})

	var input ProfileInput
	add := &cobra.Command{
		Use:   "add NAME",
		Short: "Add or update a profile without storing a client secret",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.deps.Profiles == nil {
				return unavailable("profile")
			}
			input.Name = args[0]
			if input.Environment != "sandbox" && input.Environment != "production" {
				return protocol.UsageError("--environment must be sandbox or production")
			}
			if strings.TrimSpace(input.ClientID) == "" || strings.TrimSpace(input.RedirectURI) == "" || strings.TrimSpace(input.APIVersion) == "" {
				return protocol.UsageError("--client-id, --redirect-uri, and --api-version are required")
			}
			if !apiVersionPattern.MatchString(input.APIVersion) {
				return protocol.UsageError("--api-version must use YYYY-MM-DD")
			}
			if _, err := time.Parse(time.DateOnly, input.APIVersion); err != nil {
				return protocol.UsageError("--api-version must be a valid calendar date")
			}
			profile, err := a.deps.Profiles.Upsert(cmd.Context(), input)
			if err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "configuration_error", Message: "the profile could not be saved", Cause: err}
			}
			return state.emit(profile, profileMeta(profile, "profile.add", ""))
		},
	}
	add.Flags().StringVar(&input.Environment, "environment", "sandbox", "FreeAgent environment: sandbox or production")
	add.Flags().StringVar(&input.ClientID, "client-id", "", "OAuth application client ID")
	add.Flags().StringVar(&input.RedirectURI, "redirect-uri", "", "Exact registered OAuth redirect URI")
	add.Flags().StringVar(&input.APIVersion, "api-version", config.DefaultAPIVersion, "Pinned FreeAgent API version in YYYY-MM-DD format")
	add.Flags().BoolVar(&input.ReadOnly, "read-only", false, "Persistently reject FreeAgent mutations for this profile")
	add.Flags().BoolVar(&input.Replace, "replace", false, "Explicitly replace an existing profile with these settings")
	command.AddCommand(add)

	var removeForce bool
	remove := &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a profile and its stored credentials",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.deps.Profiles == nil {
				return unavailable("secure profile removal")
			}
			name := args[0]
			profile, err := a.deps.Profiles.Resolve(cmd.Context(), name)
			if err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "configuration_error", Message: "the profile does not exist or is invalid", Cause: err}
			}
			name = profile.Name
			if !removeForce {
				if state.global.noInput {
					return protocol.SafetyError("profile remove in non-interactive mode requires --force")
				}
				confirmed, err := confirmExact(a.deps.Stdin, a.deps.Stderr, "Type the profile name to remove it: ", name)
				if err != nil {
					return err
				}
				if !confirmed {
					return protocol.SafetyError("profile removal was not confirmed")
				}
			}
			if err := a.deps.Profiles.Remove(cmd.Context(), name); err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "profile_removal_error", Message: "the profile and its stored credentials could not be removed", Cause: err}
			}
			return state.emit(map[string]any{"name": name, "removed": true}, protocol.Meta{Command: "profile.remove"})
		},
	}
	remove.Flags().BoolVar(&removeForce, "force", false, "Confirm profile and credential removal")
	command.AddCommand(remove)
	return command
}

func (a *App) newAuthCommand(state *executionState) *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Authorize FreeAgent profiles", Args: noArgs, RunE: showHelpOrUsage(state, "an auth command is required")}
	command.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show authentication status without token values",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile, err := state.resolveProfile(cmd.Context())
			if err != nil {
				return err
			}
			if a.deps.Auth == nil {
				return unavailable("authentication")
			}
			requestContext, cancel := state.requestContext(cmd.Context())
			defer cancel()
			status, err := a.deps.Auth.Status(requestContext, profile.Name)
			if err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "authentication_required", Message: "the profile is not authenticated", Cause: err}
			}
			return state.emit(status, profileMeta(profile, "auth.status", state.global.subdomain))
		},
	})

	var noBrowser, manual bool
	var loginTimeout time.Duration
	login := &cobra.Command{
		Use:   "login",
		Short: "Authorize a profile with human consent",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile, err := state.resolveProfile(cmd.Context())
			if err != nil {
				return err
			}
			if state.global.noInput {
				return protocol.SafetyError("auth login needs human input; bootstrap the profile outside agent mode")
			}
			if a.deps.Auth == nil {
				return unavailable("authentication")
			}
			if loginTimeout <= 0 {
				return protocol.UsageError("--login-timeout must be greater than zero")
			}
			loginInput := a.deps.Stdin
			var readManualCallback func() ([]byte, error)
			if manual {
				if inputFile, ok := a.deps.Stdin.(*os.File); ok && a.deps.IsTerminal(int(inputFile.Fd())) {
					readManualCallback = func() ([]byte, error) {
						if _, writeErr := io.WriteString(a.deps.Stderr, "Redirected callback URL: "); writeErr != nil {
							return nil, writeErr
						}
						callback, readErr := a.deps.ReadPassword(int(inputFile.Fd()))
						_, _ = io.WriteString(a.deps.Stderr, "\n")
						return callback, readErr
					}
				}
			}
			loginContext, cancel := context.WithTimeout(cmd.Context(), loginTimeout)
			defer cancel()
			status, err := a.deps.Auth.Login(loginContext, profile.Name, LoginOptions{NoBrowser: noBrowser, Manual: manual, Input: loginInput, Messages: a.deps.Stderr, ReadManualCallback: readManualCallback})
			if err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "authorization_failed", Message: "FreeAgent authorization did not complete", Cause: err}
			}
			return state.emit(status, profileMeta(profile, "auth.login", state.global.subdomain))
		},
	}
	login.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the authorization URL instead of opening a browser")
	login.Flags().BoolVar(&manual, "manual", false, "Read the full redirected callback URL from stdin")
	login.Flags().DurationVar(&loginTimeout, "login-timeout", 5*time.Minute, "Maximum time to complete browser authorization")
	command.AddCommand(login)

	var secretFromStdin bool
	setSecret := &cobra.Command{
		Use:   "set-client-secret",
		Short: "Store the OAuth client secret without putting it on the command line",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile, err := state.resolveProfile(cmd.Context())
			if err != nil {
				return err
			}
			var secret []byte
			if secretFromStdin {
				if inputFile, ok := a.deps.Stdin.(*os.File); ok {
					info, statErr := inputFile.Stat()
					if statErr != nil {
						return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "credential_input_error", Message: "the client secret input could not be inspected", Cause: statErr}
					}
					if info.Mode()&os.ModeCharDevice != 0 {
						return protocol.SafetyError("--stdin refuses an interactive terminal because it would echo the client secret; pipe the value instead")
					}
				}
				secret, err = readSecret(a.deps.Stdin)
			} else {
				if state.global.noInput {
					return protocol.SafetyError("hidden client-secret entry requires an interactive terminal; use --stdin with a protected pipe")
				}
				inputFile, ok := a.deps.Stdin.(*os.File)
				if !ok || !a.deps.IsTerminal(int(inputFile.Fd())) {
					return protocol.SafetyError("client-secret input is not a terminal; use --stdin with a protected pipe")
				}
				if _, writeErr := io.WriteString(a.deps.Stderr, "FreeAgent client secret: "); writeErr != nil {
					return protocol.InvariantError(writeErr)
				}
				secret, err = a.deps.ReadPassword(int(inputFile.Fd()))
				_, _ = io.WriteString(a.deps.Stderr, "\n")
			}
			if err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "credential_input_error", Message: "the client secret could not be read", Cause: err}
			}
			secret = bytes.TrimSpace(secret)
			if len(secret) == 0 {
				return protocol.UsageError("the client secret is empty")
			}
			defer zeroBytes(secret)
			if err := a.deps.Auth.SetClientSecret(cmd.Context(), profile.Name, secret); err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "credential_storage_error", Message: "the client secret could not be stored", Cause: err}
			}
			return state.emit(map[string]any{"profile": profile.Name, "client_secret_stored": true}, profileMeta(profile, "auth.set-client-secret", ""))
		},
	}
	setSecret.Flags().BoolVar(&secretFromStdin, "stdin", false, "Read the client secret from piped stdin")
	command.AddCommand(setSecret)

	var force bool
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored OAuth token",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profile, err := state.resolveProfile(cmd.Context())
			if err != nil {
				return err
			}
			if !force {
				if state.global.noInput {
					return protocol.SafetyError("auth logout in non-interactive mode requires --force")
				}
				confirmed, confirmErr := confirmExact(a.deps.Stdin, a.deps.Stderr, "Type the profile name to remove its OAuth token: ", profile.Name)
				if confirmErr != nil {
					return confirmErr
				}
				if !confirmed {
					return protocol.SafetyError("auth logout was not confirmed")
				}
			}
			if a.deps.Auth == nil {
				return unavailable("authentication")
			}
			if err := a.deps.Auth.Logout(cmd.Context(), profile.Name); err != nil {
				return &protocol.CommandError{Code: protocol.ExitAuth, Kind: "credential_storage_error", Message: "the stored OAuth token could not be removed", Cause: err}
			}
			return state.emit(map[string]any{"profile": profile.Name, "authenticated": false}, profileMeta(profile, "auth.logout", ""))
		},
	}
	logout.Flags().BoolVar(&force, "force", false, "Confirm removal without a prompt")
	command.AddCommand(logout)
	return command
}

func (a *App) newCapabilitiesCommand(state *executionState) *cobra.Command {
	var operationID string
	command := &cobra.Command{
		Use:   "capabilities",
		Short: "Describe the installed command and safety contract",
		Args:  noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if operationID != "" {
				operation, ok := a.deps.Catalog.Lookup(operationID)
				if !ok {
					return protocol.UsageError("unknown operation %q", operationID)
				}
				return state.emit(operation.Capability(), protocol.Meta{Command: "capabilities", Items: 1})
			}
			capabilities := a.deps.Catalog.Capabilities()
			return state.emit(capabilities, protocol.Meta{Command: "capabilities", Items: len(capabilities)})
		},
	}
	command.Flags().StringVar(&operationID, "command", "", "Return one stable operation ID")
	return command
}

func (a *App) newSchemaCommand(state *executionState) *cobra.Command {
	var direction string
	command := &cobra.Command{
		Use:   "schema OPERATION",
		Short: "Return a command JSON Schema",
		Args:  exactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			operation, ok := a.deps.Catalog.Lookup(args[0])
			if !ok {
				return protocol.UsageError("unknown operation %q", args[0])
			}
			schema, err := catalog.Schema(operation, catalog.SchemaDirection(direction))
			if err != nil {
				return protocol.UsageError("%s", err)
			}
			return state.emit(schema, protocol.Meta{Command: "schema", Items: 1})
		},
	}
	command.Flags().StringVar(&direction, "direction", string(catalog.SchemaInput), "Schema direction: input or output")
	return command
}

func unavailable(name string) *protocol.CommandError {
	return &protocol.CommandError{Code: protocol.ExitInvariant, Kind: "service_unavailable", Message: fmt.Sprintf("%s support is not configured", name)}
}

func readSecret(reader io.Reader) ([]byte, error) {
	var buffer bytes.Buffer
	if _, err := io.CopyN(&buffer, reader, 64*1024+1); err != nil && err != io.EOF {
		return nil, &protocol.CommandError{Code: protocol.ExitAuth, Kind: "credential_input_error", Message: "the client secret could not be read", Cause: err}
	}
	if buffer.Len() > 64*1024 {
		return nil, protocol.UsageError("the client secret input is too large")
	}
	secret := []byte(strings.TrimSpace(buffer.String()))
	if len(secret) == 0 {
		return nil, protocol.UsageError("the client secret is empty")
	}
	return secret, nil
}

func confirmExact(reader io.Reader, messages io.Writer, prompt, expected string) (bool, error) {
	if _, err := io.WriteString(messages, prompt); err != nil {
		return false, protocol.InvariantError(err)
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, &protocol.CommandError{Code: protocol.ExitUsage, Kind: "input_error", Message: "confirmation input could not be read", Cause: err}
	}
	return strings.TrimSpace(line) == expected, nil
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
