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

## What it is for

The gap it fills is narrow and specific. Cloudflare Secrets Store is a good place to
keep secrets — values are write-only, they cannot be read back through the API at all,
only bound into a Worker. That last property is exactly what makes it useless as a
secret source for a server that is not a Worker.

So the store solves storage and stops there. This carries the last mile.

### Fits

**A small fleet of VMs outside Kubernetes.** Two droplets, a handful of LXC guests, a
box at home. Running Vault for that means running — and bootstrapping, and unsealing,
and backing up — one more stateful service whose own secrets have to come from
somewhere. If Cloudflare is already in the picture, this needs no new server at all.

**A host that must not depend on what sits behind it.** The clearest case, and the one
this was built for: a gateway that fronts a cluster cannot take its secrets from a
store that is only reachable through that same gateway. The agent talks to Cloudflare
over the public internet, so recovering the host needs nothing from inside the estate
it serves. If your secret store lives behind the machine you are trying to boot, that
circularity is the problem this removes.

**Getting secrets out of `/etc/environment`.** The usual shortcut is a global env file
at mode 0644 that `pam_env` loads into every login session, which puts every secret in
the environment of every process on the box. Here each consumer gets only its own
variables, and `docker compose` gets them through its process environment with no file
at all.

**Rotating without a deploy.** Edit the value in the store; the agent picks it up on
its next tick and restarts only what actually changed. No pipeline run, no
`terraform apply`, no image rebuild.

**Mixed consumers on one host.** Containers that read environment variables, systemd
units that want an `EnvironmentFile`, and images that insist on `*_FILE` — all fed
from the same payload, each in the shape it expects.

### Does not fit

**Kubernetes.** Use the External Secrets Operator. This exists for hosts that have no
operator to run.

**Anything needing per-secret audit, leases, or dynamic credentials.** That is Vault's
job and this does not attempt it. There is no record of which secret was read when,
because the Worker hands over the whole set for a host at once.

**Secrets that must never be plaintext on disk.** Rendered files are plaintext, mode
0600. Rendering into `/etc/credstore.encrypted/` so systemd can hold them TPM-sealed is
the intended direction; it is not implemented.

**Untrusted operators or multi-tenant hosts.** Root on the host reads everything, and
the host's own credential unlocks that host's entire set.

**Air-gapped estates.** The whole design assumes the host can reach the store over the
internet.

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
       ├─ systemd units ─── EnvironmentFile via a drop-in, one per configured unit
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
- **The package's own config is left alone.** A unit's variables go into a drop-in with
  a second `EnvironmentFile=`, never over a conffile the package ships.
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
SYSTEMD_UNITS=[{"unit":"alloy.service","prefix":"ALLOY_","group":"alloy"}]
STATE_DIR=/opt/secrets
FILES_MODE=0644
```

`COMPOSE_FILE` and `SYSTEMD_UNITS` are both optional on their own — at least one has
to be set, since otherwise nothing consumes the variables.

Each entry in `SYSTEMD_UNITS` takes the variables matching its `prefix`, renders them
to `env_file` (defaulting to `<STATE_DIR>/<unit>.env`), points the unit at that file
through `/etc/systemd/system/<unit>.d/10-secrets-agent.conf`, and restarts the unit
when the content changed. `group` sets the group owning the rendered file. A unit that
is not installed on the host is skipped rather than treated as a failure.

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
