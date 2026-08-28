# Design rationale

`freeagent` is one CLI with two interfaces: a helpful terminal interface for a
person, and a strict process interface for scripts and software agents. This
document explains the main design choices. The exact installed contract remains
`freeagent capabilities` plus `freeagent schema OPERATION`.

This rationale is based on the public documentation and the repository source
and tests as reviewed on 2026-08-28. It does not claim live FreeAgent sandbox
validation.

## FreeAgent constraints shape the product

FreeAgent is not a uniform CRUD API. Its API uses OAuth 2.0, supports JSON and
XML, assigns resource access through user permission levels, paginates many
lists, and applies per-user rate limits. The normal list default is 25 items and
the documented general maximum is 100 items per page. A rate-limited request
returns HTTP 429 and a `Retry-After` value. These constraints require explicit
identity, bounded reads, typed failures, and deliberate retry policy rather than
a thin HTTP wrapper. See the [FreeAgent API introduction](https://dev.freeagent.com/docs/introduction).

The API can also change behavior according to the date in `X-Api-Version`.
FreeAgent falls back to an earlier version when no version is available for the
requested date. The CLI therefore stores and reports a fixed API date instead
of sending the current date. See [FreeAgent API versioning](https://dev.freeagent.com/docs/versioning).

Practice access is a different authorization context. It needs a practice-
enabled app and an account-manager authorization. A request on behalf of a
client adds that client's `X-Subdomain`. The profile, environment, API date,
and practice subdomain are therefore part of execution metadata and mutation
identity. See the [Accountancy Practice API](https://dev.freeagent.com/docs/accountancy_practice_api).

Some endpoint shapes also justify a conservative first release:

- Bank-statement upload accepts either a JSON `statement` array or a multipart
  file. A successful upload does not prove that the statement was imported
  correctly. The catalog exposes the JSON form only and requires a later read
  for reconciliation. See [Bank Transactions](https://dev.freeagent.com/docs/bank_transactions).
- Invoice, estimate, and credit-note PDF routes return base64 content inside a
  JSON `pdf.content` object. The CLI preserves that JSON payload. It does not
  claim that the HTTP body is a PDF file. See [Invoices](https://dev.freeagent.com/docs/invoices).

The catalog records each operation's method, path, query rules, body roots,
response key, output shape, access requirement, pagination, and risk. Commands,
schemas, capability discovery, and safety policy consume the same record. This
reduces drift between what the CLI describes and what it executes. The raw
`api METHOD PATH` command remains a restricted escape hatch for compatible
routes that are not yet safe to catalog.

## Why this Go design

Cobra supplies a conventional command tree, help, flags, and shell completion.
Its own design guidance treats a CLI as a user interface and emphasizes
discoverability and predictable command hierarchy. This supports the human
surface without requiring a second program. See the [Cobra philosophy](https://cobra.dev/docs/explanations/philosophy/).

The implementation keeps provider transport behind small interfaces and uses
standard Go request plumbing:

- A bounded `net/http` adapter owns origins, redirects, headers, response
  limits, and retry rules. Go recommends reusing HTTP clients because their
  transports cache connections, and it defines clients as safe for concurrent
  use. See [`net/http`](https://pkg.go.dev/net/http).
- `context.Context` carries timeouts and cancellation through command,
  credential, OAuth, and HTTP boundaries. The Go package defines context for
  deadlines, cancellation, and request-scoped values across API boundaries.
  See [`context`](https://pkg.go.dev/context).
- JSON decoding uses `Decoder.UseNumber`, which keeps an untyped JSON number as
  `json.Number` instead of converting it immediately to `float64`. This avoids
  an unnecessary loss of numeric representation at the protocol boundary. See
  [`encoding/json`](https://pkg.go.dev/encoding/json#Decoder.UseNumber).
- Input and output discovery uses JSON Schema Draft 2020-12. The schemas state
  when the generic FreeAgent body model is incomplete. See the
  [Draft 2020-12 specification](https://json-schema.org/draft/2020-12).

The package layout keeps Cobra, protocol rendering, catalog data, credentials,
and HTTP transport separate. This permits synthetic `httptest` coverage and
keeps provider behavior out of the stable process contract. See
[Architecture](architecture.md) for the package boundaries.

## Machine and agent contract

Automation must not scrape terminal help or depend on table layout. It gets a
versioned JSON envelope, stable exit classes, operation discovery, and schemas.
Normal data goes to stdout. Diagnostics and structured failures go to stderr.
`--agent` disables prompts, defaults to JSON, and requires an explicit profile
for FreeAgent account operations.
The first release does not emit terminal color or invoke a pager in any mode.
JSONL has a terminal summary; a bounded partial stream has a distinct exit code
and resume metadata. Raw output is explicit and is outside the stable protocol.

Writes use two steps. Dry run validates and canonicalizes the local request but
does not send it. Its output says `server_validated: false`. A short-lived token
binds the profile, environment, practice subdomain, API date, method, path,
query, and body to the later request. The token prevents accidental plan drift;
it is not proof of human approval. A transport failure after send is an unknown
outcome, so the CLI stops automatic write retries and returns reconciliation
data. See the [command contract](command-reference.md) and
[security model](security-model.md).

## What other CLIs teach us

| Tool or style | Useful lesson | What `freeagent` deliberately does differently |
| --- | --- | --- |
| [`gh api`](https://cli.github.com/manual/gh_api) | A raw API command, stdin body input, pagination, and machine formatting are useful escape hatches. | `gh api` can infer POST from added fields, substitute repository values from the current directory, and paginate until no page remains. `freeagent api` requires an explicit method and relative path. Catalogued commands never infer a mutation from supplied data, and `--all` stays within page, item, and byte bounds. |
| [AWS CLI pagination](https://docs.aws.amazon.com/cli/latest/userguide/cli-usage-pagination.html) | It clearly separates service page size, output item limits, and resume state. Its docs also warn that page and output limits can produce missing or duplicate items when a service changes order. | AWS CLI retrieves all available items by default for many commands. `freeagent` reads one page by default. Multi-page reads are explicit and bounded, and a partial result reports `next_page` rather than appearing complete. |
| [kubectl dry run](https://kubernetes.io/blog/2019/01/14/apiserver-dry-run-and-kubectl-diff/) | kubectl distinguishes a local dry run from a server dry run that reaches admission and validation. That distinction prevents a local check from being presented as server acceptance. | The reviewed FreeAgent public docs define no equivalent server-side dry-run contract for these operations. `freeagent` therefore labels its plan as locally validated only. It uses request-bound confirmation and post-write reconciliation instead of claiming server validation. |
| Human-first CLI defaults | Help, completion, tables, prompts, and pagers can improve interactive use. GitHub CLI, for example, enables terminal prompting by default and has a configurable pager. See [`gh config`](https://cli.github.com/manual/gh_config). | Human mode provides help, completion, tables, and guarded prompts. The first release deliberately omits color and pager behavior. Agent mode also rejects interaction, requires explicit context, and uses stable JSON. A mutable default profile is never enough for an agent account operation. |

These differences are intentional, not general criticisms of the other tools.
GitHub, AWS, and Kubernetes optimize for different APIs and failure costs. A
financial CLI needs tighter context binding and a smaller automatic action
surface.

## Alternatives not selected

- **Only expose a generic API command.** This is small, but it cannot provide
  reliable operation discovery, access metadata, output cardinality, or risk-
  specific safeguards.
- **Fetch every page automatically.** This is convenient, but it hides request
  volume and conflicts with FreeAgent rate limits. One page by default and
  bounded `--all` make cost visible.
- **Retry every HTTP method.** This can duplicate a financial effect when the
  request reached FreeAgent but the response did not reach the CLI. Reads can
  use bounded retries; writes cannot.
- **Call local dry run server validation.** Local schema validation cannot know
  permissions, lifecycle state, or provider-side rules. The protocol states the
  boundary instead.
- **Support XML, multipart upload, and PDF decoding in the first release.** A
  JSON-only process contract is smaller and easier to preserve. The raw command
  keeps upstream bytes available without pretending to transform them.
- **Optimize only for agents.** People still need readable help, completion,
  tables, and guided OAuth. The two modes share one command catalog but have
  different interaction defaults.

## Sources

- [FreeAgent API introduction](https://dev.freeagent.com/docs/introduction)
- [FreeAgent API versioning](https://dev.freeagent.com/docs/versioning)
- [FreeAgent Accountancy Practice API](https://dev.freeagent.com/docs/accountancy_practice_api)
- [FreeAgent Bank Transactions](https://dev.freeagent.com/docs/bank_transactions)
- [FreeAgent Invoices](https://dev.freeagent.com/docs/invoices)
- [Cobra philosophy](https://cobra.dev/docs/explanations/philosophy/)
- [Go `net/http`](https://pkg.go.dev/net/http)
- [Go `context`](https://pkg.go.dev/context)
- [Go `encoding/json.Decoder.UseNumber`](https://pkg.go.dev/encoding/json#Decoder.UseNumber)
- [JSON Schema Draft 2020-12](https://json-schema.org/draft/2020-12)
- [GitHub CLI `gh api`](https://cli.github.com/manual/gh_api)
- [GitHub CLI configuration](https://cli.github.com/manual/gh_config)
- [AWS CLI pagination](https://docs.aws.amazon.com/cli/latest/userguide/cli-usage-pagination.html)
- [Kubernetes API server dry run and `kubectl diff`](https://kubernetes.io/blog/2019/01/14/apiserver-dry-run-and-kubectl-diff/)
