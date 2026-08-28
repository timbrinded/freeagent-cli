# Security policy

## Supported versions

Security fixes are supplied for the latest published release. Before CLI
version 1.0, minor releases can add commands and compatible protocol fields.
Incompatible protocol changes require a CLI major release.

## Report a vulnerability

Use GitHub's private **Report a vulnerability** form in the repository's
Security tab. If private vulnerability reporting is not available, contact the
repository owner privately through the contact method on the owner's GitHub
profile.

Do not open a public issue. Do not include a real FreeAgent client secret,
access token, refresh token, redirected OAuth URL, or private financial record.
Use a synthetic sandbox reproduction where possible.

Include:

- The affected release and operating system.
- The required attacker access.
- A minimal reproduction using synthetic data.
- The expected and actual security boundary.
- Any known disclosure deadline.

Receipt will be acknowledged when the report is read. A remediation and
disclosure plan depends on severity and reproducibility. No fixed response-time
guarantee is made by this volunteer-maintained project.

## Security boundaries

The CLI is designed to:

- Keep OAuth secrets and tokens out of its JSON configuration.
- Store secrets in the operating-system keyring.
- Serialize refresh-token rotation across processes.
- Send bearer credentials only to the configured FreeAgent API origin.
- Reject cross-origin authenticated redirects and absolute raw API paths.
- Avoid automatic write retries.
- Redact known secret values from diagnostics.

The CLI cannot protect credentials from a compromised user account, operating
system, keyring, shell, or process with equivalent access. It cannot confirm
the accounting or tax correctness of an operator's instruction.

Read [docs/security-model.md](docs/security-model.md) for the complete model.
