# Agent use

The CLI provides a machine contract, but it does not grant an agent authority
to make financial, accounting, banking, payroll, or tax decisions.

Use the repository Agent Skill at
`.agents/skills/freeagent-cli/SKILL.md` when the agent host supports Agent
Skills.

## Required invocation contract

For every account operation:

- Select an explicit profile.
- Pass `--agent` or both `--output json` and `--no-input`.
- Set a timeout.
- Bound multi-page reads.
- Parse stdout only after exit code 0.
- Parse diagnostics or the failure envelope from stderr after a non-zero exit.
- Keep secrets and complete redirected OAuth URLs out of prompts and arguments.

Do not send `--output raw` to a terminal. It preserves untrusted upstream bytes
and is outside the stable agent protocol.

Start each session with:

```sh
freeagent version --output json
freeagent auth status --profile sandbox --agent
freeagent capabilities --profile sandbox --agent
```

Check the protocol major and environment before doing further work.
Authentication status does not prove the company identity. Call catalogued
`company.get`, or the applicable practice/client operation, before a mutation.

## Discover before constructing

Do not infer a command, field name, status value, or date format. Select an
operation from `capabilities`, then request its schema:

```sh
freeagent schema invoices.list --profile sandbox --agent
```

Validate locally generated input against that schema. Use exact IDs for write
targets. A human-facing name can support discovery but cannot be the final
identity of a mutation target.

If `x-freeagent-payload-schema-complete` is `false`, follow the schema's primary
FreeAgent documentation URL and verify the request fields. Do not guess a write
payload or infer it from an unrelated response.

## Read workflow

1. Confirm version and protocol compatibility.
2. Confirm profile, environment, and authentication; use `company.get` when the
   requested company identity is material.
3. Discover the operation and schema.
4. Run a bounded list or get operation.
5. Follow only trusted pagination returned through the CLI.
6. Confirm a terminal summary for JSONL.
7. Report partial results as partial.

Use the smallest required page range. FreeAgent limits requests per user and
per hour. Honour `retry_after_seconds`; do not create an internal retry storm.

## Mutation workflow

1. Confirm that the user's instruction authorizes the specific effect.
2. Verify actual company identity with `company.get`, or verify practice/client
   context as applicable.
3. Resolve exact immutable target IDs with reads.
4. Report the target company, environment, records, amounts, dates, and effect.
5. Construct input from the installed schema.
6. Run the dry-run form. Confirm `server_validated` is `false`.
7. Present the exact canonical plan and complete effect to the human.
8. Request explicit human approval for that exact financial or high-risk plan.
9. Recheck company identity, then apply the approved plan using its
   request-bound confirmation token before expiry.
10. Read the resulting state and report it.

Never reuse a confirmation token after any target, profile, environment,
practice subdomain, API version, method, path, query, or body change. Company
identity is verified by a separate read and is not supplied by auth status.

Treat the token as a request-binding interlock, not proof that a human approved
the request. Obtain approval in the current workflow before applying it.

## Human boundaries

A human must complete initial OAuth approval. Require explicit human approval
before:

- Creating, changing, sending, or deleting an invoice, estimate, credit note,
  bill, expense, payment, or recurring instruction.
- Explaining, importing, reconciling, or transferring banking data.
- Creating or changing journals or financial records.
- Filing, marking, paying, or changing tax, payroll, or CIS data.
- Inviting a user or changing access and account locks.
- Sending an email or other external communication.
- Deleting an attachment or record.
- Running any production mutation whose effect was not already explicit in the
  current user instruction.

Ask for accounting or tax judgement instead of inventing it.

## Unknown outcomes

FreeAgent does not document an idempotency-key contract. A connection can fail
after a write reaches the server. The CLI reports this as exit code 8 with
`outcome: "unknown"` and an operation fingerprint.

After exit code 8:

1. Stop mutation retries.
2. Preserve the operation fingerprint and redacted error.
3. Read the target collection or resource.
4. Compare stable IDs and approved business fields.
5. Report whether the effect exists, does not exist, or remains ambiguous.
6. Ask for direction if a second write could duplicate the effect.

## Skill evaluation

Run the cases in `.agents/skills/freeagent-cli/evals/evals.json` with a fresh
agent and synthetic sandbox fixtures. Compare the skilled run with a baseline
run. Passing requires correct behaviour, not only command completion.
