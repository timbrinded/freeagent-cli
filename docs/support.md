# Support

This is an independent open-source project. FreeAgent does not endorse or
support it.

## Ask for help

Use a GitHub Discussion when available. Otherwise open a GitHub issue with:

- CLI version and protocol version.
- Operating system and architecture.
- The command shape with all sensitive values removed.
- Exit code and redacted structured error.
- Sandbox or production, without company-private data.
- Expected and actual behaviour.

Run `freeagent version --output json` and `freeagent auth status --profile NAME
--agent` for diagnostic context. The auth status response is designed not to
contain token values, but inspect all output before publication.

## Do not publish

- Client secrets.
- Access or refresh tokens.
- Full redirected OAuth URLs.
- Keyring contents.
- Real invoices, contacts, bank transactions, payroll, tax, or account data.
- A security exploit before coordinated disclosure.

Use synthetic sandbox data in reproductions.

## Security reports

Follow [SECURITY.md](../SECURITY.md). Do not use a public issue.

## FreeAgent questions

For FreeAgent account behaviour, tax or accounting guidance, API access
approval, or undocumented server behaviour, use the appropriate official
FreeAgent support channel. This project cannot provide accounting advice or
make FreeAgent service commitments.
