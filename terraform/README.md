# terraform modules

- `terraform/` — installs the agent on a host, documented below
- `terraform/worker` — hands the Cloudflare Worker source to whoever deploys it

Installs the agent on a host and keeps its configuration in step with your terraform.

Nothing here is specific to a cloud: the module reaches the host over SSH and declares
no providers at all, so it works on a droplet, a Hetzner server, an LXC guest or a
machine terraform never created — as long as it runs systemd and the user you give it
has passwordless sudo.

## Usage

```hcl
module "secrets_agent" {
  source = "github.com/obervinov/secrets-agent//terraform?ref=v1.2.0"

  host            = module.droplet.droplet_networks.external_v4
  ssh_user        = "terraform"
  ssh_private_key = base64decode(var.PROVISIONER_SSH_PRIVATE_KEY)

  # Pinned, because the binary is checked against the SHA256SUMS of this release.
  agent_version = "v1.1.0"

  url = "https://secrets.example.com/v1/env/web-1"
  auth_headers = {
    "CF-Access-Client-Id"     = var.ACCESS_CLIENT_ID
    "CF-Access-Client-Secret" = var.ACCESS_CLIENT_SECRET
  }

  compose_file  = "/opt/configurations/docker-compose.yml"
  systemd_units = [{ unit = "alloy.service", prefix = "ALLOY_", group = "alloy" }]

  # So a recreated host gets the agent again.
  redeploy_trigger = module.droplet.droplet_info.id
}
```

At least one of `compose_file` or `systemd_units` has to be set. A configuration that
consumes nothing fails at plan time rather than installing an agent that fetches
secrets and hands them to no one.

## Inputs

| Name | Type | Default | Description |
| ---- | ---- | ------- | ----------- |
| `host` | string | — | Address of the host. |
| `ssh_user` | string | — | User to connect as. Needs passwordless sudo. |
| `ssh_private_key` | string | — | Key in PEM form. Pass `base64decode(...)` if yours is stored encoded. |
| `ssh_port` | number | `22` | SSH port. |
| `agent_version` | string | — | Release tag to install, e.g. `v1.1.0`. |
| `url` | string | — | Endpoint serving this host's variables as a JSON object. Must be `https`. |
| `auth_headers` | map(string) | — | Headers authenticating the agent to that endpoint. |
| `compose_file` | string | `null` | Compose file on the host. Variables go into `docker compose`'s process environment, so `${VAR}` interpolation works with nothing written to disk. |
| `systemd_units` | list(object) | `[]` | Units taking a share of the variables — see below. |
| `routed_files` | map(string) | `{}` | `filename => VARIABLE`, for images that read `*_FILE` rather than a value. |
| `env` | map(string) | `{}` | Variables the caller owns rather than the store. Anything the endpoint serves wins over these. |
| `interval` | string | `*:0/15` | `OnCalendar` expression for the timer. |
| `state_dir` | string | `/opt/secrets` | Where rendered files and the cached payload live. |
| `files_mode` | string | `0644` | Mode of the per-variable files. |
| `redeploy_trigger` | string | `""` | Reinstall when this changes. |

### systemd_units

```hcl
systemd_units = [
  { unit = "alloy.service", prefix = "ALLOY_", group = "alloy" },
  { unit = "node-exporter.service", prefix = "NE_", env_file = "/etc/ne.env" },
]
```

Each entry takes the variables matching its `prefix`, renders them to `env_file`
(default `<state_dir>/<unit>.env`) and points the unit at that file through
`/etc/systemd/system/<unit>.d/10-secrets-agent.conf` — so whatever conffile the package
ships keeps its own settings. `group` owns the rendered file, which is what lets a unit
running as a non-root user read it. A unit that is not installed on the host is skipped
rather than treated as a failure.

## What the apply does

1. Downloads `secrets-agent_<agent_version>_linux_<arch>` on the host, `arch` from
   `uname -m`, and checks it against the `SHA256SUMS` published beside it. A tampered
   or truncated download fails the apply before anything is installed.
2. Installs the binary `root:root 0755`, the config `root:root 0600`, and the unit and
   timer.
3. Enables the timer and **runs the agent once synchronously** — so a wrong credential
   or endpoint fails the apply instead of surfacing on a tick nobody is watching.

## Notes

The config file travels as provisioner content rather than inside a `remote-exec`.
It holds the credential that unlocks every secret for the host, and a `remote-exec`
carrying it inline would be rendered into the plan — which is routinely kept as a CI
artifact.

Changing a secret needs none of this: edit it in the store and the agent picks it up on
its next tick. Terraform is only involved when the agent's version or its configuration
changes.

---

# terraform/worker

The Worker that serves the secrets is deployed by the operator's own terraform, against
their own Cloudflare account, Access applications and Secrets Store — none of which
this project can own. What it can own is the script, so it is not copied into every
consumer's repository where the two would drift apart.

```hcl
module "worker_source" {
  source = "github.com/obervinov/secrets-agent//terraform/worker?ref=v1.3.0"
}

resource "cloudflare_workers_script" "secrets_proxy" {
  account_id     = var.account_id
  script_name    = "secrets-proxy"
  content        = module.worker_source.script
  content_sha256 = module.worker_source.script_sha256
  main_module    = module.worker_source.main_module

  # bindings: EXPECTED_HOST, TEAM_DOMAIN, MANIFEST, and one
  # secrets_store_secret per blob
}
```

`?ref=` pins it, so a caller gets the script that shipped with that release rather than
whatever is on `main`. Bumping the ref is what deploys a new Worker.

| Output | Description |
| ------ | ----------- |
| `script` | Source, for `content` |
| `script_sha256` | Hash of it, for `content_sha256` |
| `main_module` | File name the entrypoint is expected under |
