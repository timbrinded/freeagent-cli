# Contributing

Contributions are welcome. Keep changes small enough to review and attach tests
to observable behaviour.

## Before starting

1. Search existing issues and pull requests.
2. Open an issue before a large command, protocol, OAuth, or safety change.
3. Never use real FreeAgent credentials or financial records in tests or issue
   reports.
4. Treat `internal/catalog` as the authority for commands, schemas, safety
   classes, and generated discovery output.

## Development setup

Install Go 1.26.7 or later, then run:

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/freeagent
```

Use a fully configured FreeAgent sandbox for opt-in integration tests. Never
run a production write from a test. See [docs/development.md](docs/development.md).

## Change requirements

- Preserve stdout for data and stderr for diagnostics in machine modes.
- Keep compatible JSON protocol additions deliberate and documented. Reserve
  incompatible protocol changes for a new protocol major and CLI major.
- Add or update the operation registry for each public command.
- Keep money exact; never introduce binary floating-point conversion.
- Add an `httptest` contract test for every transport behaviour.
- Do not automatically retry writes.
- Add sandbox evidence before enabling a route whose FreeAgent documentation is
  inconsistent.
- Update the Agent Skill only when its workflow or safety contract changes. Do
  not copy the complete command catalogue into the skill.

## Documentation and skill validation

Check links and examples against `freeagent help`, `freeagent capabilities`,
and `freeagent schema` from the same build.

Validate the repository skill with the official reference implementation:

```sh
uvx --from \
  'git+https://github.com/agentskills/agentskills@69ef37e9424c0a7ea9dd2293b559e43ec8176379#subdirectory=skills-ref' \
  skills-ref validate .agents/skills/freeagent-cli
```

Then run the behavioural cases in
`.agents/skills/freeagent-cli/evals/evals.json` with a fresh agent and synthetic
sandbox data.

## Pull requests

Explain the user-visible result, security effect, tests, and documentation
effect. Keep unrelated formatting and refactors out of the change.

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
