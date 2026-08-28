# Quick start

This guide creates a sandbox connection and performs a bounded read. It does
not change FreeAgent data.

`freeagent` is an unofficial client. FreeAgent does not support this project.

## Prerequisites

- A FreeAgent developer account.
- A completed FreeAgent sandbox company.
- A registered OAuth application for that sandbox.
- The `freeagent` binary on `PATH`.
- An available operating-system keyring.

FreeAgent requires a sandbox company to complete its setup before normal API
operations work. Follow the [FreeAgent quick start](https://dev.freeagent.com/docs/quick_start)
first.

## 1. Verify the binary

```sh
freeagent version --output json
```

Record the CLI version and `protocol_version` when an automation system depends
on the output contract.

## 2. Register an OAuth application

Create an application in the FreeAgent developer dashboard. Choose an exact
IP-loopback redirect URI, such as
`http://127.0.0.1:8484/oauth/callback`, and register it with FreeAgent. The CLI
listens on the URI that you later put in the profile. Redirect URI matching is
exact; a different port, host, path, or trailing slash can fail.

Do not publish or commit the client secret. Public copies of this CLI do not
contain a shared FreeAgent application credential. See
[OAuth application setup](oauth-setup.md).

## 3. Add a sandbox profile

Inspect the installed command before entering profile fields:

```sh
freeagent profile add --help
```

Add a profile named `sandbox`. Select the `sandbox` environment and provide the
client ID, exact redirect URI, and the documented API version date:

```sh
freeagent profile add sandbox \
  --environment sandbox \
  --client-id CLIENT_ID \
  --redirect-uri EXACT_REGISTERED_LOOPBACK_URI \
  --api-version 2024-10-01 \
  --read-only \
  --output json
```

Replace each uppercase placeholder. Version `2024-10-01` is the documented
version configured for this CLI release; it is not evidence of live API
validation. FreeAgent selects the newest API version available on or before the
header date. Do not use the current date or a future date as an automatic pin.
See [FreeAgent API versioning](https://dev.freeagent.com/docs/versioning).
The initial profile is read-only. Update it deliberately only when guarded
writes are required; replacing an existing profile requires `--replace`. The
JSON profile stores no secret.

Confirm the non-secret context:

```sh
freeagent profile show --profile sandbox --output json
```

Check that the environment is `sandbox`, the redirect URI is exact, and the API
version is a fixed documented date.

## 4. Store the client secret

Use the hidden terminal prompt:

```sh
freeagent auth set-client-secret --profile sandbox
```

Alternatively, pipe the secret from a password manager and add `--stdin`. The
stdin form rejects an interactive terminal because normal piped input cannot
hide terminal echo. Never supply a client secret as an argument or exported
environment variable.

## 5. Complete human OAuth approval

```sh
freeagent auth login --profile sandbox
```

The command opens the FreeAgent approval page. A human must authenticate,
inspect the company and permissions, and approve or deny access. For a headless
shell, use the login command's documented manual mode. Pass the full redirected
URL through protected stdin when prompted. Do not place it on the command line.

The CLI validates a random, single-use OAuth state value. It exchanges the code
using HTTP Basic authentication and stores the resulting token in the OS
keyring.

## 6. Verify the connection

```sh
freeagent auth status --profile sandbox --agent
```

The response identifies the profile, environment, API version, and
authentication state. It never returns a client secret or token.

## 7. Discover supported operations

```sh
freeagent capabilities --profile sandbox --agent
freeagent schema invoices.list --profile sandbox --agent
```

Treat these responses as the authority for the installed version. Static docs
describe the model, but they do not replace runtime discovery.

## 8. Perform a bounded read

```sh
freeagent invoices list \
  --profile sandbox \
  --agent \
  --per-page 25
```

Use `--output jsonl` for a long stream. Set `--max-items` or `--max-pages` when
requesting multiple pages.

## 9. Add production only after sandbox validation

Create a distinct profile and OAuth application for production. Confirm the
company and environment before every production mutation. Do not copy sandbox
tokens or configuration into the production profile.

Read [security model](security-model.md), [command reference](command-reference.md),
and [agent use](agent-use.md) before enabling writes.
