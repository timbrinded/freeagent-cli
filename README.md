# freeagent

`freeagent` is an unofficial command-line client for the
[FreeAgent API](https://dev.freeagent.com/). It is designed for people, shell
scripts, and software agents. It provides explicit account profiles,
machine-readable discovery, stable JSON output, and guarded financial writes.

This project is independent of FreeAgent. It is not endorsed, sponsored, or
supported by FreeAgent.

> [!CAUTION]
> FreeAgent records can affect accounts, banking, payroll, and tax. Start with a
> sandbox profile. Review every production mutation. The CLI cannot make an
> accounting or tax decision for an operator.

## Status

Version 0.1.0 is the first public preview. Machine envelopes, error classes,
and exit semantics use stable protocol version 1. The CLI release version
remains pre-1.0 while its documented command catalog can expand.

## What it provides

- Separate sandbox and production profiles.
- Bring-your-own FreeAgent OAuth application credentials.
- OAuth secrets and rotating refresh tokens in the operating-system keyring.
- Human and agent output modes with separate stdout and stderr contracts.
- Runtime operation discovery through `capabilities` and `schema`.
- Bounded pagination for agent and batch use.
- Read-only policy controls and guarded production mutations.
- A relative-path raw API command for API features that do not yet have a
  catalogued resource command.
- A repository Agent Skill in `.agents/skills/freeagent-cli`.

## Install

Download an archive and `checksums.txt` from
[GitHub Releases](https://github.com/timbrinded/freeagent-cli/releases), then
put `freeagent` on `PATH`.

Go 1.26.7 or later can also install the current source release:

```sh
go install github.com/timbrinded/freeagent-cli/cmd/freeagent@latest
```

Verify the installation:

```sh
freeagent version --output json
```

See [release and installation guidance](docs/releases.md) for checksum, SBOM,
and source-build instructions.

## Quick start

1. Create and finish setting up a
   [FreeAgent sandbox company](https://dev.freeagent.com/docs/quick_start).
2. Register a FreeAgent OAuth application. Choose an exact IP-loopback redirect
   URI, such as `http://127.0.0.1:8484/oauth/callback`.
3. Add a named sandbox profile. Supply the environment, client ID, the same
   registered redirect URI, and a fixed API date.
4. Run `freeagent auth set-client-secret --profile sandbox`. Enter the client
   secret through the hidden prompt, or use `--stdin` with a protected password-
   manager pipe.
5. Run `freeagent auth login --profile sandbox`. Complete the approval in a
   browser.
6. Confirm the context before reading data:

```sh
freeagent profile show --profile sandbox --output json
freeagent auth status --profile sandbox --agent
freeagent capabilities --profile sandbox --agent
freeagent invoices list --profile sandbox --output json --no-input --per-page 25
```

Never put the client secret, access token, refresh token, or redirected OAuth
URL in a command-line argument, a repository, a prompt, or a log.

Read the full [quick start](docs/quick-start.md) and
[OAuth application setup](docs/oauth-setup.md) before using production.

## Agent use

Agent calls must select a profile and disable interaction:

```sh
freeagent capabilities --profile sandbox --agent
freeagent schema invoices.list --profile sandbox --agent
freeagent invoices list \
  --profile sandbox \
  --agent \
  --all \
  --max-items 100
```

`--agent` defaults to JSON, disables prompts, and keeps stdout for protocol
data. The CLI does not use terminal colour or a pager in any mode. Diagnostics
and structured errors go to stderr. Use JSONL for long result streams. An
explicit `--output raw` preserves untrusted upstream bytes and must not write
to a terminal.

Before any write, discover the operation schema, build exact input, and run the
operation's dry-run form. Every write requires the short-lived confirmation
bound to that exact request. This is an anti-accident binding, not proof of
human approval. If the CLI returns exit code `8`, the outcome is unknown: read
the target state and reconcile it before considering another write.

The repository skill supplies the complete agent workflow:
[`.agents/skills/freeagent-cli/SKILL.md`](.agents/skills/freeagent-cli/SKILL.md).
See [agent use](docs/agent-use.md) and [examples](examples/README.md).

## Documentation

- [Quick start](docs/quick-start.md)
- [OAuth application setup](docs/oauth-setup.md)
- [Command and protocol reference](docs/command-reference.md)
- [Agent use and approval boundaries](docs/agent-use.md)
- [Security model](docs/security-model.md)
- [API coverage and known limitations](docs/api-coverage.md)
- [Architecture](docs/architecture.md)
- [Design rationale](docs/design-rationale.md)
- [Agent Skill evaluation](docs/skill-evaluation.md)
- [Development](docs/development.md)
- [Releases and installation](docs/releases.md)
- [Support](docs/support.md)

## Contributing and security

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a change. Report a
security problem privately as described in [SECURITY.md](SECURITY.md). Do not
put credentials, private financial records, or a vulnerability exploit in a
public issue.

## Licence

MIT. See [LICENSE](LICENSE).
