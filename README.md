# secrets-agent

Use an external secret store as the source of environment variables for ordinary VMs.

Cloudflare Secrets Store keeps secret values write-only: they cannot be read back
through the API at all, only bound into a Worker. That makes it a good store and a
useless one for a plain server. This project bridges the gap:

- **`worker/`** — a Worker that serves each host its own set of variables, guarded by
  Cloudflare Access with a per-host service token
- **`cmd/secrets-agent`** — an agent that runs on the VM, fetches those variables and
  applies them locally, on a systemd timer
- **`terraform/`** — a module that installs a pinned release of the agent

The agent itself is backend-agnostic: it fetches a JSON object of variables from an
authenticated HTTPS endpoint. Cloudflare is the reference backend, not a requirement.

## How it fits together

```
systemd timer
  └─ secrets-agent
       └─ GET https://secrets.example.com/v1/env/<host>
            with the host's service token headers
          └─ Cloudflare Access ── 403 for anything without that token
             └─ Worker ── merges a shared blob with the host's blob
                └─ Cloudflare Secrets Store
       ↓
       ├─ docker compose ── variables passed in its environment
       ├─ grafana alloy ─── EnvironmentFile via a systemd drop-in
       └─ /opt/secrets/files/<name> ── one file per variable, for images
                                       that read *_FILE
```

## What the agent guarantees

- **A failed apply is retried.** Each consumer's applied-state is recorded only after
  its command succeeds, so a failed `docker compose up` or `systemctl restart` is
  retried on the next tick instead of being reported as "no changes" forever.
- **An upstream outage does not take the host down.** The last payload that applied
  cleanly is cached, and the cache is promoted only after a clean pass — a payload
  that cannot be applied never becomes the fallback.
- **No dotenv rendering.** Variables reach `docker compose` through its process
  environment, so values containing quotes, `$`, `#` or spaces need no escaping and
  cannot break the whole file.
- **The package's own config is left alone.** Alloy's variables go into a drop-in with
  a second `EnvironmentFile=`, never over `/etc/default/alloy`.
- **It refuses to run insecurely.** The config holds the credential that unlocks every
  secret for the host, so the agent exits unless that file is root-owned and mode
  0600, and unless the endpoint is `https://`.
- **Writes are atomic.** Temp file, `fsync`, rename, plus an `fsync` of the directory,
  so a reader never sees a partial secret and a reboot cannot leave a truncated one.
- **Runs are serialised** with `flock`, because a manual run during a timer tick is the
  first thing anyone tries when debugging.

## Configuration

`/etc/secrets-agent.conf`, root-owned, mode 0600:

```sh
AGENT_URL=https://secrets.example.com/v1/env/web-1
AUTH_HEADERS={"CF-Access-Client-Id":"...","CF-Access-Client-Secret":"..."}
COMPOSE_FILE=/opt/configurations/docker-compose.yml
STATE_DIR=/opt/secrets
FILES_MODE=0644
```

`/etc/secrets-agent.files` routes individual variables to their own file, for images
that take `*_FILE` instead of a value:

```sh
postgres_password=POSTGRES_PASSWORD
```

## Why not …

**systemd credentials?** `LoadCredentialEncrypted=` and `ImportCredential=` solve
delivery *into a process* from a local store, not distribution *to a host*. They
compose with this rather than replace it — rendering into `/etc/credstore.encrypted/`
so that secrets are TPM-sealed at rest and never become environment variables is the
intended direction, and is not implemented yet.

**Vault Agent / Infisical Agent / consul-template?** Same shape, different backends,
and all of them need a store that answers reads. This exists because Cloudflare
Secrets Store deliberately does not.

## Status

Early. The store it targets is itself in open beta.

Known gap before this is fit for anyone else's production: the Worker checks that
Cloudflare Access put a JWT on the request, but does not verify its signature or bind
`aud` to the host being requested. Until it does, per-host isolation rests on Access
path-matching rather than on cryptography.
