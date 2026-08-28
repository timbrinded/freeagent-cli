# OAuth application setup

FreeAgent uses an OAuth authorization-code flow. Initial authorization requires
a human FreeAgent user. The CLI cannot and must not bypass that approval.

## Bring an application credential

Create a separate OAuth application through the FreeAgent developer dashboard.
Public distributions of this CLI do not include a client ID or client secret.
This follows FreeAgent's rule against hardcoded access credentials in public
OAuth clients.

Use separate applications for sandbox and production where practical. At a
minimum, use separate CLI profiles and verify each profile's environment before
login.

Relevant FreeAgent documentation:

- [Quick start](https://dev.freeagent.com/docs/quick_start)
- [OAuth](https://dev.freeagent.com/docs/oauth)
- [API terms](https://dev.freeagent.com/docs/api_terms)
- [Client-secret rotation](https://dev.freeagent.com/docs/rotating_client_secrets)

## Redirect URI

Choose and register an exact IP-loopback redirect URI, such as
`http://127.0.0.1:8484/oauth/callback`. Pass that same value to `freeagent
profile add --redirect-uri`. FreeAgent requires exact matching.

Prefer a literal loopback address such as `127.0.0.1`, not a wildcard interface
and not a public callback. The callback listener accepts one login response,
validates the OAuth state, then closes.

If a browser cannot reach the loopback listener, use the login command's manual
mode. Copy the complete redirected URL into protected stdin. The state remains
mandatory. Never pass the URL as an argument because shells and process lists
can retain it.

## Credential placement

The versioned schema-v1 profile configuration is stored at
`os.UserConfigDir()/freeagent/config.json`. It is replaced atomically. POSIX
systems use owner-only file permissions. Windows uses the access-control list
inherited from the current user's `%AppData%` directory. It contains:

- Profile name.
- `sandbox` or `production` environment.
- Client ID.
- Exact redirect URI.
- Pinned API version.

The OS keyring service `freeagent-cli` contains:

- Client secret.
- Access token.
- Current rotating refresh token.
- Token expiry metadata needed for refresh.

The CLI does not silently fall back to a plaintext token file. Resolve keyring
failures before login or use an explicitly documented ephemeral credential
input intended for the current process.

Store the client secret with `freeagent auth set-client-secret --profile NAME`.
Use its hidden prompt, or add `--stdin` and pipe from a password manager. The
command never accepts the secret as an argument.

## Login lifecycle

1. Store the client secret through hidden input or a protected pipe.
2. Generate at least 32 random bytes for `state`.
3. Open or print the FreeAgent approval URL.
4. Let a human authenticate and approve the correct company.
5. Validate the returned state exactly and only once.
6. Exchange the short-lived code with HTTP Basic client authentication and a
   form-encoded request.
7. Store the token in the OS keyring.
8. Return status without exposing any secret value.

FreeAgent currently documents a 15-minute authorization-code lifetime and an
access-token lifetime supplied in `expires_in`. Do not hardcode an access-token
lifetime.

## Refresh rotation and concurrency

A refresh response can return a new access token and a new refresh token. The
CLI takes a cross-process profile lock, reloads the latest keyring value,
refreshes once, and writes the complete replacement token before releasing the
lock. This prevents two agent processes from using the same rotating refresh
token concurrently.

Do not copy token material between machines. Authorize each machine or isolated
automation identity separately.

## Logout and revocation

`freeagent auth logout --profile NAME` removes the locally stored OAuth token.
It does not remove the OAuth application's client secret. Removing the profile
cleans up profile credentials through the profile-removal workflow. FreeAgent
does not document an OAuth revocation endpoint. Local logout must not be
described as server-side revocation. Remove the application's access through
FreeAgent when server-side access must end.

## Secret rotation

FreeAgent can keep two client secrets active during rotation. Add the new
secret to the keyring, complete a login or refresh test, then retire the old
secret through FreeAgent. Never print either secret during the procedure.
