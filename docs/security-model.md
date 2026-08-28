# Security model

## Goals

The CLI aims to protect FreeAgent credentials from accidental disclosure,
prevent authenticated cross-origin requests, make automation context explicit,
and prevent automatic duplicate writes.

It does not act as a multi-user authorization service. The operating-system
user and keyring remain the local trust boundary.

## Assets

- FreeAgent client secret.
- OAuth access and refresh tokens.
- Company and practice context.
- Financial, banking, payroll, and tax records.
- Mutation plans and confirmation tokens.
- API responses, logs, and exported files.

## Credential storage

Profiles use schema v1 at `os.UserConfigDir()/freeagent/config.json`. The CLI
replaces the file atomically with owner-only permissions. A profile contains
only its environment, client ID, exact redirect URI, pinned API version, and
read-only policy. Sandbox and production select fixed API origins. Profiles
contain no secret.

Client secrets and the complete access/refresh token record are stored under
the OS keyring service `freeagent-cli`. Keyring failure is fatal by default.
The CLI does not silently create a plaintext token file.

Shell arguments, environment variables, redirected OAuth URLs, crash reports,
and debug logs can leak secrets. Use protected prompts or stdin for sensitive
input. Redaction is defence in depth, not permission to print credentials.
Structured error redaction is best effort and key based: JSON fields whose
names contain `token`, `secret`, `password`, or `authorization` are replaced.
It does not find every credential embedded in another string, and it does not
rewrite raw output.

## OAuth threats

- **Login CSRF or callback confusion:** generate a random state of at least 32
  bytes, compare it exactly, accept it once, and expire it quickly.
- **Callback exposure:** listen on loopback only and close after the response.
- **Code leakage:** accept a manual redirected URL through stdin, not argv.
- **Refresh races:** take a cross-process profile lock, reload the token after
  locking, and atomically store the complete rotated token.
- **False revocation claims:** describe local logout as local deletion because
  FreeAgent does not document a revocation endpoint.

FreeAgent does not document PKCE or device authorization. The CLI must not
claim either protection without a verified server contract.

## HTTP threats

Authenticated requests are restricted to the selected FreeAgent production or
sandbox origin. Raw paths must be relative. Scheme-relative and absolute paths
are rejected. Cross-origin redirects and pagination links are rejected before
authorization can be forwarded. The CLI returns upstream attachment metadata
and URLs but does not follow an external attachment URL.

All requests send a useful `User-Agent`, JSON accept headers, and a fixed
`X-Api-Version`. The release default is the documented `2024-10-01` version.
FreeAgent selects the newest API version available on or before the header
date, so a future date is not a stable pin. See
[FreeAgent API versioning](https://dev.freeagent.com/docs/versioning). An
API-version override is explicit and visible in output. An Accountancy Practice
`X-Subdomain` value must be one DNS label and is attached per request.

## Retry and mutation threats

Reads can use bounded retries for network errors, selected server errors, and
rate limits. Writes are never automatically retried, including PUT and DELETE.
HTTP method idempotence does not prove business-level idempotence.

A failure after a write is sent returns an unknown outcome. The operator must
reconcile state before another attempt. The CLI does not invent or rely on an
`Idempotency-Key`; FreeAgent does not document one.

Dry run performs local validation and makes no mutation request. Its output
must state `server_validated: false`. A short-lived confirmation token is bound
to the complete canonical request and context. Every mutation requires it,
including an interactive sandbox mutation. The token is a two-step
anti-accident binding, not cryptographic proof or attestation of human approval.
The calling workflow must enforce the human approval boundary.

## Output threats

- Send machine data to stdout and diagnostics to stderr.
- Omit generated ANSI sequences, progress displays, prompts, and pagers in
  structured agent output. Treat explicit raw output as untrusted bytes and
  never send it to a terminal.
- Preserve upstream JSON value types; use exact decimal arithmetic when
  calculating money.
- Redact known credential values from errors.
- Limit response size and pagination.
- Treat raw output and downloaded attachments as untrusted data.
- Avoid exposing local paths or keyring identifiers without need.

## Residual risks

- A compromised account or machine can use credentials available to that user.
- A FreeAgent permission level can grant more API access than one CLI command
  needs because granular OAuth scopes are not documented.
- FreeAgent does not document optimistic concurrency, idempotency, webhooks, or
  deletion feeds.
- Static API documentation contains apparent route and method inconsistencies.
- A syntactically valid operation can still be financially or legally wrong.

Use least-privilege FreeAgent users, separate environments, bounded operations,
and human review to reduce these risks.
