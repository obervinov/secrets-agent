# Change Log
All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](http://keepachangelog.com/) and this project adheres to [Semantic Versioning](http://semver.org/).


## v1.4.0 - 2026-09-13
### What's Changed
#### 🐛 Bug Fixes
* Re-apply compose when the compose file itself changes. The digest deciding whether `docker compose up -d` has to run covered the variables and the file's *path*, so an image tag bumped in the compose file while the variables stayed the same was reported as `compose unchanged` and the old containers kept running indefinitely. Found on a host still running an image two releases behind what its compose file asked for, six weeks after the bump. The digest now covers the file's contents, and a missing compose file fails loudly rather than hashing nothing and calling it unchanged.

## v1.3.2 - 2026-09-11
### What's Changed
#### 🐛 Bug Fixes
* `terraform/`: stage under a path unique to each apply. The fixed `/tmp/secrets-agent-install` collided with a leftover directory owned by another user, and the install failed with `install: cannot change permissions of '/tmp/secrets-agent-install': Operation not permitted` — `/tmp` is shared, so a fixed name is only ever as safe as whatever ran there before. The directory is now named after the resource id, which is regenerated on every replacement.

## v1.3.1 - 2026-09-11
### What's Changed
#### 🐛 Bug Fixes
* `terraform/`: stage under an absolute path. A `file` provisioner runs no shell, so `~/.secrets-agent` reached scp literally and every install failed with `Upload failed: scp: ~/.secrets-agent: No such file or directory` — the preceding `remote-exec` had created the directory under the real `$HOME`, where the upload never looked. Staging moves to `/tmp/secrets-agent-install`, mode 0700 so the config is not readable by other users for the seconds it exists.
* `terraform/`: never upload empty content. A caller passing neither `routed_files` nor `env` produced two zero-byte provisioner uploads; both files now carry a header naming what wrote them, which the agent skips as a comment.

## v1.3.0 - 2026-09-11
### What's Changed
#### 🚀 Features
* `terraform/worker`: a module handing the worker source to whoever deploys it. The worker is deployed by the operator's own terraform, against their own Cloudflare account, Access applications and Secrets Store — so this project cannot own the deployment, but it can own the script. Without somewhere to fetch it from, every consumer keeps a copy in their own repository and the two drift apart. `?ref=` pins it, so a caller gets the script that shipped with that release rather than whatever is on `main`.

## v1.2.0 - 2026-09-11
### What's Changed
#### 🚀 Features
* `terraform/`: a module that installs a pinned release on a host. It declares no providers and reaches the host over SSH, so it works on a droplet, a Hetzner server, an LXC guest or a machine terraform never created — anything running systemd where the given user has passwordless sudo. The binary is downloaded on the host and verified against the `SHA256SUMS` of that release before anything is installed, the config travels as provisioner content rather than inside a `remote-exec` that a plan would capture, and the agent is run once synchronously so a wrong credential fails the apply instead of a later timer tick. A configuration that consumes nothing — neither `compose_file` nor `systemd_units` — fails at plan time.

## v1.1.0 - 2026-09-03
### What's Changed
#### 🚀 Features
* `worker/`: verify the Cloudflare Access JWT instead of only checking that the header is present. The signature is checked against the team's published keys, and `aud` is bound to the Access application guarding the requested host — so a token issued for one host cannot be replayed against another's path, regardless of how Access matches paths. The previous check could not survive the case it existed for: a deleted or misconfigured Access application leaves requests unfiltered, and the header is client-settable. `alg` is pinned to RS256 rather than taken from the token, the key set is cached per isolate with a refetch on an unknown `kid`, and an unreachable key endpoint fails closed. The manifest now carries `{secret, aud}` per host and the Worker needs a `TEAM_DOMAIN` binding.

## v1.0.0 - 2026-09-03
### What's Changed
#### 🚀 Features
* `worker/`: a Cloudflare Worker serving each host its own merged set of variables from the Secrets Store, behind Cloudflare Access with a per-host service token. Secrets Store values are write-only and readable only from a Worker binding, which is what makes them unusable as a secret source for a plain VM and why this exists.
* `cmd/secrets-agent`: the agent for the host side. Applies the fetched set to docker compose (variables passed in the process environment, so nothing is rendered as dotenv text), to grafana alloy (a systemd drop-in with a second `EnvironmentFile=`, leaving the package conffile alone), and to per-variable files for images that read `*_FILE`. Each consumer's applied-state is recorded only after its command succeeds, so a failed restart is retried rather than reported as no-change. Caches the last payload that applied cleanly, refuses to run unless its config is root-owned `0600` and the endpoint is `https`, writes atomically with `fsync`, and serialises runs with `flock`.
* `packaging/`: systemd unit and timer, verified on a live host.
