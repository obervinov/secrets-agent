locals {
  binary_path  = "/usr/local/bin/secrets-agent"
  config_path  = "/etc/secrets-agent.conf"
  routing_path = "/etc/secrets-agent.files"
  asset_base   = "https://github.com/obervinov/secrets-agent/releases/download/${var.agent_version}"

  unit = templatefile("${path.module}/templates/secrets-agent.service.tftpl", {
    binary_path = local.binary_path
    config_path = local.config_path
    state_dir   = var.state_dir
  })

  timer = templatefile("${path.module}/templates/secrets-agent.timer.tftpl", {
    interval = var.interval
  })

  config = templatefile("${path.module}/templates/secrets-agent.conf.tftpl", {
    url          = var.url
    auth_headers = jsonencode(var.auth_headers)
    compose_file = coalesce(var.compose_file, "")
    state_dir    = var.state_dir
    files_mode   = var.files_mode
    routing_path = local.routing_path

    # Optional fields are dropped rather than emitted as null, so the agent falls back
    # to its own defaults instead of reading a key that carries nothing.
    systemd_units = length(var.systemd_units) > 0 ? jsonencode([
      for unit in var.systemd_units : merge(
        { unit = unit.unit, prefix = unit.prefix },
        unit.env_file == null ? {} : { env_file = unit.env_file },
        unit.group == null ? {} : { group = unit.group },
      )
    ]) : ""
  })

  # filename=VARIABLE per line.
  routing = join("", [for name, variable in var.routed_files : "${name}=${variable}\n"])

  env = join("", [for key, value in var.env : "${key}=${value}\n"])
}

# A configuration that consumes nothing would install an agent that fetches secrets and
# hands them to no one. Caught here rather than on the host.
resource "terraform_data" "consumer_check" {
  input = "ok"

  lifecycle {
    precondition {
      condition     = var.compose_file != null || length(var.systemd_units) > 0
      error_message = "No consumer configured: set compose_file, systemd_units or both."
    }
  }
}

resource "terraform_data" "agent" {
  depends_on = [terraform_data.consumer_check]

  triggers_replace = {
    version = var.agent_version
    host    = var.redeploy_trigger
    config = sha256(join("\n---\n", [
      local.unit,
      local.timer,
      nonsensitive(local.config),
      local.routing,
      local.env,
    ]))
  }

  connection {
    host        = var.host
    port        = var.ssh_port
    user        = var.ssh_user
    type        = "ssh"
    agent       = false
    timeout     = "3m"
    private_key = var.ssh_private_key
  }

  provisioner "remote-exec" {
    inline = ["install -d -m 0700 \"$HOME/.secrets-agent\""]
  }

  # The config holds the credential that unlocks every secret for this host, so it
  # travels as provisioner content. A remote-exec carrying it inline would be rendered
  # into the plan, and a plan is routinely kept as a CI artifact.
  provisioner "file" {
    content     = local.config
    destination = "~/.secrets-agent/secrets-agent.conf"
  }

  provisioner "file" {
    content     = local.unit
    destination = "~/.secrets-agent/secrets-agent.service"
  }

  provisioner "file" {
    content     = local.timer
    destination = "~/.secrets-agent/secrets-agent.timer"
  }

  provisioner "file" {
    content     = local.routing
    destination = "~/.secrets-agent/secrets-agent.files"
  }

  provisioner "file" {
    content     = local.env
    destination = "~/.secrets-agent/terraform.env"
  }

  provisioner "remote-exec" {
    inline = [
      "set -eu",
      "cd \"$HOME/.secrets-agent\"",

      # Verify before installing: a tampered or truncated download has to fail the
      # apply, not leave a broken binary in place.
      "arch=$(uname -m); case \"$arch\" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo \"unsupported architecture $arch\" >&2; exit 1 ;; esac",
      "asset=secrets-agent_${var.agent_version}_linux_$arch",
      "curl -fsSL --retry 3 -o \"$asset\" \"${local.asset_base}/$asset\"",
      "curl -fsSL --retry 3 -o SHA256SUMS \"${local.asset_base}/SHA256SUMS\"",
      "grep \" $asset$\" SHA256SUMS | sha256sum -c -",

      # root:root on the binary and 0700 on the state directory on purpose: the unit
      # runs as root, so anything writable by another user is a root escalation at the
      # next timer tick.
      "sudo install -m 0755 -o root -g root \"$asset\" ${local.binary_path}",
      "sudo install -d -m 0700 -o root -g root ${var.state_dir}",
      "sudo install -m 0600 -o root -g root secrets-agent.conf ${local.config_path}",
      "sudo install -m 0600 -o root -g root terraform.env ${var.state_dir}/terraform.env",
      "sudo install -m 0644 -o root -g root secrets-agent.files ${local.routing_path}",
      "sudo install -m 0644 -o root -g root secrets-agent.service /etc/systemd/system/secrets-agent.service",
      "sudo install -m 0644 -o root -g root secrets-agent.timer /etc/systemd/system/secrets-agent.timer",
      "cd \"$HOME\" && rm -rf \"$HOME/.secrets-agent\"",

      "sudo systemctl daemon-reload",
      "sudo systemctl enable --now secrets-agent.timer",

      # Run once synchronously, so a wrong credential or endpoint fails the apply
      # rather than surfacing on a timer tick nobody is watching.
      "sudo systemctl start secrets-agent.service",
    ]
  }
}
