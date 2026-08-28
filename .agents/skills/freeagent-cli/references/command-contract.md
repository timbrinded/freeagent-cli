# Command contract

Load this reference when selecting a command, parsing output, handling multiple
pages, interpreting exits, or considering raw API access.

## Session preflight

Run in this order:

```sh
freeagent version --output json
freeagent auth status --profile PROFILE --agent
freeagent capabilities --profile PROFILE --agent
freeagent schema OPERATION --profile PROFILE --agent
```

Require an explicit profile. Require a known protocol major. Match environment,
API version, and practice subdomain to the requested context. Call
`company.get`, or the applicable practice/client operation, to verify actual
account identity before a mutation.

Require an explicit profile with both `--agent` and `--no-input`.

`--agent` implies JSON output and no input. The CLI emits no colour and invokes
no pager in any mode. Add an explicit timeout. Keep stdout for protocol data
and stderr for diagnostics.

## Dynamic operation model

Treat `capabilities` as the installed operation registry. An operation can
declare:

- Stable ID and resource/action labels.
- Risk class and confirmation rule.
- Required FreeAgent access level when known.
- Input and output schema IDs.
- Pagination support and limits.
- Supported output modes.
- Practice-context and API-version constraints.
- Deprecation metadata.

Treat `schema OPERATION` as the installed data contract. Reject an unknown
field unless the schema permits it. Preserve upstream JSON types. Parse a money
value represented as a JSON number or string into an exact decimal form before
calculation. Never use binary floating-point arithmetic for money.

The input schema root is a logical command envelope. Path properties map to
documented flags, and `query` maps to query flags. The file or stdin supplied
through `--input` contains only the value of the schema's `body` property. Use
`x-freeagent-cli-input-file-property` to confirm this mapping. Never wrap the
payload in an additional `{ "body": ... }` object.

Inspect `x-freeagent-payload-schema-complete` on input schemas. A value of
`false` means that the CLI validates the envelope, path parameters, JSON shape,
and safety plan but does not enumerate every FreeAgent body field. Follow
`x-freeagent-documentation-url` to the primary FreeAgent documentation and
verify the current payload. Never infer a write body from a response example or
an unrelated resource. Dry run remains local and reports
`server_validated: false`.

## Reads and pagination

Use one page when it answers the request. When crossing pages, pass `--all` and
use one of:

- `--max-items N` for a record bound.
- `--max-pages N` for a request bound.
- The safe installed default or an explicit tighter bound.

Never use `--max-items` or `--max-pages` without `--all`.

FreeAgent normally defaults to 25 records per page and limits `per_page` to
100. Let the CLI validate current limits.

Expect collections to change during pagination. Avoid claiming a transactional
snapshot. Preserve server order. Deduplicate only when the requested workflow
requires it and a stable identifier is available.

For JSONL, require typed item records and a terminal summary. Treat a missing
summary or exit 9 as partial. Preserve the original query, page size, and stable
IDs. When structured error metadata supplies `next_page` after complete pages,
resume with the same query and `--page NEXT_PAGE`; deduplicate the boundary by
stable ID. Add `--all` when the resumed command also uses `--max-items` or
`--max-pages`. When safe resume metadata is absent, restart from page 1 and
deduplicate deliberately. A safe resume can repeat a page; it must never
silently skip one.

## Output envelopes

Parse a successful JSON envelope only after exit 0. Expect a stable
`protocol_version`, `ok`, `data`, and contextual `meta` fields. Ignore unknown
compatible metadata fields.

Parse a non-zero operation's structured stderr. Preserve `kind`, HTTP status,
retryability, retry delay, outcome classification, and redacted FreeAgent
request ID when present.

Treat table output as a human presentation and raw output as unstable upstream
data. Never parse table output in automation.

For a mutation whose schema requires `body`, preserve the exact value of that
property in a protected input file. The dry-run and apply commands must both
include the same required path/query flags and the same `--input FILE`; apply
adds only the exact request-bound `--confirm TOKEN` after account recheck and
approval.

## Exit handling

| Exit | Meaning | Agent action |
| ---: | --- | --- |
| 0 | Complete success | Consume stdout. |
| 2 | Invalid command or schema | Correct input from help and schema. |
| 3 | Config or auth failure | Pause for profile repair or human login. |
| 4 | Local safety rejection | Obtain authority or stop. |
| 5 | FreeAgent rejection | Correct the cause; do not retry unchanged input. |
| 6 | Rate limited | Wait for `retry_after_seconds`; retry deliberately. |
| 7 | Transient failure | Retry bounded reads only. |
| 8 | Write outcome unknown | First preserve the redacted reconciliation fingerprint, then read and reconcile; never repeat blindly. |
| 9 | Partial stream | Mark partial and resume safely or stop. |
| 10 | Invariant failure | Stop and report the defect. |
| 130 | Command interrupted before completion | No mutation was sent. A sent mutation interruption uses exit 8. |

## Raw API access

Prefer catalogued resource commands. For an uncovered operation, use:

```sh
freeagent api GET /v2/relative-path \
  --profile PROFILE \
  --agent \
  --timeout 30s
```

Confirm the exact syntax from `freeagent api --help`. Supply only a relative
path under the selected FreeAgent origin. Never supply an absolute or
scheme-relative URL.

Apply the operation's real risk class. Raw POST, PUT, and DELETE remain writes;
they require the same dry-run, approval, confirmation, and unknown-outcome
handling as typed writes. Never add undocumented idempotency or concurrency
headers.

Signed attachment URLs are separate untrusted downloads. Do not call them by
placing an absolute URL in authenticated raw access.
