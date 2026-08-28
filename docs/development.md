# Development

## Requirements

- Go 1.26.7 or later.
- Git.
- An operating-system keyring service for auth tests that require it.
- Optional: a completed FreeAgent sandbox and dedicated OAuth application.
- Optional: GoReleaser v2, Syft, uv, and `skills-ref` for release and skill
  checks.

Read `go.mod` before selecting tool versions.

## Local checks

```sh
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/freeagent
```

Run `gofmt` on changed Go files. Run `govulncheck ./...` before a release.

Validate the Agent Skill:

```sh
uvx --from \
  'git+https://github.com/agentskills/agentskills@69ef37e9424c0a7ea9dd2293b559e43ec8176379#subdirectory=skills-ref' \
  skills-ref validate .agents/skills/freeagent-cli
```

The pinned validator revision makes local and CI results repeatable. Review and
update that revision deliberately.

## Test layers

### Unit tests

Cover config validation, profile isolation, URL canonicalization, schema
validation, confirmation hashes, redaction, exact decimals, errors, and exit
codes.

### HTTP contract tests

Use `httptest.Server` for OAuth and API behaviour:

- OAuth state success, mismatch, denial, timeout, and single use.
- Basic-auth and form-encoded token exchange.
- Rotating refresh tokens under concurrent processes.
- Required API headers and fixed API version.
- Same-origin redirects and pagination.
- 429 `Retry-After` handling.
- Bounded read retries and zero write retries.
- Mixed FreeAgent error representations.
- Response-size and JSONL partial-result limits.

Fuzz raw paths, redirect locations, pagination links, error bodies, redaction,
and mutation-plan canonicalization.

### Future sandbox integration tests

This release does not include or claim a live sandbox test run. When a live
suite is added, keep it behind an explicit build tag or environment gate. Use
only a dedicated sandbox company and synthetic records. Never run a production
write.

Prove questionable routes and OAuth details before enabling them. Record the
tested API version and result. Clean up synthetic records through ordinary
guarded operations when the API permits it.

## Documentation contract

Before release, compare examples with the same binary's:

```sh
freeagent help
freeagent capabilities --profile sandbox --agent
freeagent schema OPERATION --profile sandbox --agent
```

Do not claim live FreeAgent validation from mocked tests. Label sandbox tests
and their tested API version explicitly.

## Snapshot release

Install GoReleaser v2 and Syft, then run:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

Inspect archives, checksums, SBOM files, `freeagent version`, and shell
completion on at least Linux, macOS, and Windows before tagging a release.
