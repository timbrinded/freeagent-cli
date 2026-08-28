# Examples

These examples show the agent-safe command pattern. Run them only after adding
and authorizing a sandbox profile.

Always compare examples with the installed `capabilities`, `schema`, and
command help. The installed version is authoritative.

## Discover and list

```sh
PROFILE=sandbox

freeagent version --output json
freeagent auth status --profile "$PROFILE" --agent
freeagent capabilities --profile "$PROFILE" --agent
freeagent schema invoices.list --profile "$PROFILE" --agent
freeagent invoices list \
  --profile "$PROFILE" \
  --agent \
  --all \
  --max-items 100 \
  --timeout 30s
```

Use a task-specific variable such as `PROFILE`; do not repurpose `HOME` or
other system variables.

## Stream a bounded collection

```sh
freeagent invoices list \
  --profile sandbox \
  --output jsonl \
  --no-input \
  --all \
  --max-pages 4 \
  --timeout 30s
```

Consume the stream only when a terminal summary is present and the process
returns zero. Treat exit code 9 as a partial result.

## Plan a mutation

Inspect the exact mutation command and schema reported by the installed CLI.
The schema root is a logical command envelope. Supply only its `body` property
value through the documented file or stdin mechanism, then run the command's
`--dry-run` form. Confirm that the plan reports the intended profile,
environment, target IDs, exact decimal amounts, dates, and
`server_validated: false`.

The plan does not prove company identity. Call catalogued `company.get` before
the dry run and again immediately before apply, then compare the actual company
with the approved target.

Do not paste a generic mutation example into production. A human must approve
the complete plan. Apply it only with the request-bound confirmation mechanism
reported by the command.

## Handle an unknown write outcome

When a write exits with code 8:

```text
stop writes
record the redacted operation fingerprint
read the target state by stable identifier or approved business fields
classify the effect as present, absent, or still ambiguous
ask for direction before any possible duplicate
```

Never put OAuth secrets or token values in example files.
