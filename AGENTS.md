# Repository agent instructions

## Scope

These instructions apply to all files in this repository.

## FreeAgent operations

- Read `.agents/skills/freeagent-cli/SKILL.md` before using `freeagent` to read
  or change FreeAgent data.
- Select an explicit profile and use `--agent` or `--no-input` for every agent
  invocation.
- Discover installed operations with `capabilities` and `schema`; do not infer
  flags or payload fields from static documentation.
- Bound all pagination.
- Keep secrets out of command arguments, prompts, logs, patches, and fixtures.
- Require human OAuth approval and explicit human approval for financial,
  payroll, tax, banking, send, transition, or delete operations.
- Run a dry run before each mutation and bind confirmation to the exact plan.
- Reconcile current state after exit code 8. Never repeat an unknown-outcome
  mutation blindly.

## Development

- Read `go.mod` before selecting Go tools or dependency versions.
- Keep command, schema, and safety metadata in the central operation registry.
- Preserve stable machine output and exit-code behaviour.
- Keep OAuth and API credentials in the OS keyring, never configuration files.
- Never retry writes automatically.
- Use `httptest` and synthetic fixtures. Restrict live tests to the sandbox.
- Run `go test ./...`, `go test -race ./...`, `go vet ./...`, and the relevant
  format or generation checks before completion.
- Update user documentation and skill evaluations for public contract changes.
