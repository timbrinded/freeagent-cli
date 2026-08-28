# Command and protocol reference

The installed CLI is the authority for its exact command surface:

```sh
freeagent help
freeagent capabilities --profile NAME --agent
freeagent schema OPERATION --profile NAME --agent
```

This document defines the stable model shared by commands. Protocol version 1
is stable from CLI version 0.1.0. Incompatible machine-output, exit-code, or
retry-semantics changes require a new protocol major and CLI major release.

## Global flags

| Flag | Purpose |
| --- | --- |
| `--profile NAME` | Select one named sandbox or production context. |
| `--output table\|json\|jsonl\|raw` | Select the renderer. |
| `--agent` | Select non-interactive JSON protocol mode. |
| `--no-input` | Reject any operation that would prompt. |
| `--no-color` | Compatibility flag. Output is always uncoloured in this release. |
| `--read-only` | Reject every catalogued and raw mutation. |
| `--subdomain NAME` | Select one Accountancy Practice client context. |
| `--timeout DURATION` | Bound each API request. Pagination remains bounded separately. |
| `--api-version DATE` | Override the pinned API date for an explicit compatibility test. |

An explicit `--profile` is required with `--agent` or `--no-input` for FreeAgent
operations. Do not rely on a mutable default profile in automation. This
release does not invoke a pager or emit terminal colour in any output mode. This
release uses the documented `2024-10-01` API version as its configured default.
FreeAgent selects the newest version available on or before the requested date,
so the current date or a future date is not a stable pin. See
[FreeAgent API versioning](https://dev.freeagent.com/docs/versioning). Check
release notes for sandbox-validation evidence.

## Top-level commands

### `version`

Report the binary version, commit, build date when present, and machine protocol
version. This command does not require a profile or network access.

### `profile add|list|show|remove`

Manage versioned, non-secret profile configuration. Profiles contain an
environment, client ID, redirect URI, and pinned API version. Secret values are
stored separately in the OS keyring. `profile add NAME` refuses to overwrite an
existing profile unless `--replace` is explicit.

Removing a profile is a local operation. It removes the OAuth token and client
secret from the keyring before it removes profile configuration. Interactive
removal requires the exact profile name; non-interactive removal requires
`--force`.

`profile use NAME` selects a human-facing default. Agents must still pass an
explicit profile.

### `auth set-client-secret|login|status|logout`

Store a client secret through hidden input or a protected stdin pipe, authorize
a profile, report redacted authentication state, or delete its local OAuth
token. No command accepts the client secret as an argument. Initial login
always crosses a human approval boundary. Logout does not delete the client
secret and is not server-side OAuth revocation.

### `capabilities`

Return the operation registry for the installed binary. Each operation can
declare:

- Stable operation ID.
- Resource and action.
- Risk class.
- Required FreeAgent access level when known.
- Input and output schema identifiers.
- Pagination and output modes.
- Confirmation policy.
- Practice-context support.
- Deprecation metadata.

Agents must use this command instead of guessing installed features.

### `schema OPERATION`

Return the machine-readable input or output schema for one operation. Use the
operation ID from `capabilities`. Reject unknown fields unless the operation
schema explicitly permits them.

Check `x-freeagent-payload-schema-complete`. When it is `false`, the schema
covers the CLI request envelope but not every FreeAgent body field. Use its
`x-freeagent-documentation-url` to verify the current upstream payload. A local
dry run does not turn an incomplete payload schema into server validation.

An input schema is a logical command envelope. Path properties map to their
documented command flags, and the `query` object maps to named query flags or
repeatable `--query NAME=VALUE`. A mutation file or stdin stream must contain
only the JSON value of the schema's `body` property. The extension
`x-freeagent-cli-input-file-property: "body"` makes this mapping explicit. Do
not put an extra `{ "body": ... }` wrapper in `--input`.

For example, a structural `invoices.create` schema value can contain:

```json
{"body":{"invoice":{"contact":"CONTACT_URL"}}}
```

The corresponding input file contains only:

```json
{"invoice":{"contact":"CONTACT_URL"}}
```

The payload schema is incomplete, so verify all real invoice fields through
the schema's official documentation URL before use.

### Catalogued resource commands

Catalogued commands use a resource noun and operation verb, for example:

```text
freeagent invoices list
freeagent invoices get --id ID
freeagent invoices create
freeagent invoices update --id ID
freeagent invoices delete --id ID
```

Not every resource supports CRUD, and not every installed version implements
every documented FreeAgent operation. Use `capabilities` for the exact set.

Common read controls include `--page`, `--per-page`, `--all`, `--max-pages`,
and `--max-items`. A request for all pages remains bounded by an explicit or
safe default limit.

Supply mutation JSON with `--input FILE` or `--input -` for stdin. Supply query
parameters with repeatable `--query NAME=VALUE`. Avoid sensitive values in
arguments. A mutation dry run validates locally and emits a canonical plan
without sending the write. Every mutation, including an interactive sandbox
mutation, requires the plan's current `--confirm` token. The token binds two
steps to one exact request; it does not attest human approval.

When a mutation schema requires `body`, use the same unchanged `--input FILE`
for dry run and apply. Repeat every required path and query flag. After account
recheck and approval, add only the plan's exact `--confirm TOKEN`.

### `api METHOD PATH`

Call a FreeAgent API route that does not yet have a catalogued resource
command. `PATH` must be relative to the selected profile's API origin. Absolute
and scheme-relative URLs are rejected. Authenticated cross-origin redirects and
cross-origin pagination links are rejected.

Raw writes are subject to the same read-only, dry-run, confirmation, retry, and
unknown-outcome rules as catalogued writes. `--output raw` exposes an upstream
body and is not part of the stable JSON protocol.

### `completion SHELL`

Generate completion for a supported shell. Follow the installed command's
instructions for `bash`, `zsh`, `fish`, or PowerShell.

## Output modes

### Table

Table output is for a terminal. Column selection and presentation can change
without a protocol-major change.

### JSON

JSON output contains one versioned envelope. Successful data goes to stdout.
Diagnostics and a structured failure object go to stderr.

Representative success shape:

```json
{
  "protocol_version": "1",
  "ok": true,
  "data": [],
  "meta": {
    "command": "invoices.list",
    "profile": "sandbox",
    "environment": "sandbox"
  }
}
```

The CLI preserves FreeAgent JSON value types. It does not promise that every
money value is a string or that every timestamp uses one format. Consumers must
accept the documented upstream representation and use exact decimal arithmetic
for calculations. Resource identifiers and URLs remain upstream JSON values.

### JSONL

JSONL emits one event object per line. Item records are followed by a terminal
summary on complete success. A partial stream has no successful completion
summary and returns exit code 9. A resumed page sequence can repeat an item; it
must not silently skip a page.

### Raw

Raw output returns the exact upstream HTTP body bytes. It is an escape hatch,
not a stable agent contract. Raw mode preserves untrusted bytes, including
terminal control sequences. Redirect it to a file or a byte-safe consumer;
never render it in a terminal. FreeAgent PDF routes return a JSON envelope with
base64 content, so raw mode does not decode those routes into a PDF file.

## Exit codes

| Code | Class | Required response |
| ---: | --- | --- |
| 0 | Success | Consume stdout. |
| 2 | Usage or schema error | Correct the command or input. |
| 3 | Configuration or authentication | Repair the profile or perform human login. |
| 4 | Local safety policy | Obtain approval or select a permitted operation. |
| 5 | FreeAgent rejection | Inspect the structured API error; do not retry unchanged input. |
| 6 | Rate limited | Honour `retry_after_seconds` before a deliberate retry. |
| 7 | Transient read or server failure | Retry only when the operation is a read and policy permits it. |
| 8 | Mutation outcome unknown | Reconcile current state; never repeat blindly. |
| 9 | Partial JSONL result | Keep or discard partial items deliberately, then resume safely. |
| 10 | Internal invariant failure | Stop and report a reproducible defect. |
| 130 | Command interrupted before completion | No mutation was sent. A sent mutation interruption returns exit 8 instead. |

Stable error `kind` values provide finer classification than the numeric code.
Preserve the upstream request ID and redacted response details when supplied.
