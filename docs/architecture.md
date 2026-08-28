# Architecture

The design keeps FreeAgent transport details behind one adapter and treats the
command registry as the product contract.

```text
cmd/freeagent
  -> internal/runtime
     -> internal/cli
        -> internal/catalog
        -> internal/output
        -> internal/protocol
     -> internal/auth
     -> internal/config
     -> internal/credential
     -> internal/freeagent
        -> FreeAgent API
```

## Boundaries

### `cmd/freeagent`

Build the root command and convert the final classified error into an exit
code. Keep business and transport logic out of `main`.

### `internal/cli`

Bind Cobra commands to operation specifications. Parse input, select the
profile, enforce interaction policy, and invoke provider operations.

### `internal/catalog`

Own every stable operation ID, input and output schema, risk class, access
requirement, pagination rule, and confirmation policy. Generate or drive Cobra
commands, `capabilities`, `schema`, safety checks, and command documentation
from the same records.

Avoid independent registries. Drift between execution, schema, and safety is a
correctness defect.

### `internal/runtime` and `internal/freeagent`

`internal/runtime` connects CLI service interfaces to profiles, credentials,
OAuth, and transport. `internal/freeagent` owns fixed API origins, headers,
request and response codecs, retry rules, body bounds, and transport error
classification. The project does not use a third-party FreeAgent SDK.

The adapter owns its HTTP policy directly. It does not expose a third-party SDK
or allow SDK URL handling, process-local token storage, or retry defaults to
become part of the public contract.

### `internal/auth` and `internal/config`

Keep schema-v1 non-secret profiles in the atomically replaced per-user config
file. POSIX systems use owner-only modes; Windows inherits the current user's
`%AppData%` access-control list. Keep client secrets and the whole token record
under the OS keyring service `freeagent-cli`. Serialize token refresh across
processes. Keep browser launching optional; make initial human approval
mandatory.

### `internal/output` and `internal/protocol`

Own stable envelopes, JSONL events, error kinds, exit classes, redaction, and
human renderers. Resource handlers return structured data and metadata; they do
not print directly.

## Client separation

Use separate HTTP policies:

- An authenticated read client with bounded retries.
- An authenticated mutation client with no automatic retries.

Reject an absolute raw path or cross-origin redirect before adding a bearer
token. Validate pagination links against the configured origin. Return external
attachment URLs as data; do not follow them.

## Mutation planning

Canonicalize the profile, environment, practice subdomain, API version, method,
path, query, and body. Hash that representation into a short-lived confirmation
token. Emit the canonical representation during dry run, without sending a
mutation request. Verify the actual company identity through `company.get`
before planning because authentication status does not persist company identity.

Classify a transport failure after send as unknown outcome. The CLI cannot
infer non-application from the absence of a response.

## Design constraints

- Keep exact decimal values out of `float64`.
- Keep server record order unless an operation contract states otherwise.
- Sort registries and schema properties for deterministic discovery.
- Emit JSONL only within the configured page, item, and byte bounds.
- Bound response bodies and page counts.
- Preserve redacted upstream details and request IDs for diagnosis.
- Pin the FreeAgent API version and direct dependency versions.
