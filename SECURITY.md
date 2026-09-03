# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.x.x   | :white_check_mark: |

## Reporting a Vulnerability

Please open an issue with the label "security" and I will try to fix it as soon as
possible.

## Trust boundaries

What an attacker gets at each compromise point, so you can decide whether that is
acceptable for your deployment:

| Compromise point | What it yields |
| --- | --- |
| The Cloudflare account, or an API token with Workers deploy rights | Everything. A Worker can be deployed that binds the same secrets and dumps them, which bypasses Access entirely |
| Terraform state of the stack that creates the Access service tokens | Every host's token, and through them every host's secrets, from anywhere |
| Cloudflare Access misconfigured or its application deleted | See the known gap below |
| One host's service token | That host's set, plus anything shared with it, from anywhere, until the token is revoked |
| Root on a host | That host's set, plus its own token |
| A container on a host | The variables that host handed it |

## Known gap

The Worker checks that Cloudflare Access placed a JWT on the request, but does **not**
verify its signature, and does not bind `aud` to the host being requested. The header
is client-settable, so that check catches a missing or deleted Access application only
by accident. Until it verifies the signature, per-host isolation rests on Access
path-matching rather than on cryptography.

Do not run this in front of secrets you cannot afford to lose without reading that
paragraph twice.

## Non-goals

State plainly what this does not attempt, so it is not adopted for the wrong job:

- **Not a Vault replacement.** No per-secret audit trail, no leases, no dynamic
  credentials. The Worker hands over a whole host's set at once, so there is no record
  of which individual secret was read when.
- **Revocation does not reach running hosts.** The agent caches the last payload that
  applied cleanly and falls back to it whenever a fetch fails, deliberately, so an
  upstream outage cannot stop containers from starting. Revoking a token therefore
  stops updates, not the secrets already in use.
- **Secrets are plaintext on disk**, mode 0600, and in the environment of the
  containers that receive them — visible through `docker inspect` and
  `/proc/<pid>/environ`. Rendering into `/etc/credstore.encrypted/` for systemd to hold
  TPM-sealed is the intended direction and is not implemented.
- **No protection from root** on a consumer host, or from the Cloudflare account owner.
- **Cloudflare sees every plaintext value.** This is not end-to-end encrypted.
- **Shared variables multiply blast radius.** A value shared between hosts is
  compromised on every host as soon as any one of them is.
- **Bearer-token authentication** with no IP or mTLS binding by default. Pinning the
  Access policy to the host's address is worth doing and is not done for you.

## What it does enforce

- The agent refuses to start unless its configuration file is owned by root with mode
  0600, and unless the endpoint is `https://`
- Secrets Store values are write-only: they cannot be read back through the Cloudflare
  API at all, only through a Worker binding
- The Worker refuses any request whose hostname is not the expected one, which is what
  makes a `workers.dev` route or a preview URL useless
- Each host's path is guarded by its own Access application and service token, so one
  host's token does not reach another's set
- Error paths never interpolate secret material into a response body or a log line
