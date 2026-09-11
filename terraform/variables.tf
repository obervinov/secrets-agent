variable "host" {
  description = "Address of the host to install the agent on."
  type        = string
}

variable "ssh_user" {
  description = "User to connect as. Needs passwordless sudo."
  type        = string
}

variable "ssh_private_key" {
  description = "Private key in PEM form. Pass base64decode(...) if yours is stored encoded."
  type        = string
  sensitive   = true
}

variable "ssh_port" {
  description = "SSH port of the host."
  type        = number
  default     = 22
}

variable "agent_version" {
  description = "Release tag of the agent to install, for example v1.1.0. Pinned rather than latest, because the binary is verified against the SHA256SUMS of this exact release."
  type        = string

  validation {
    condition     = can(regex("^v[0-9]+\\.[0-9]+\\.[0-9]+$", var.agent_version))
    error_message = "agent_version must be a release tag such as v1.1.0."
  }
}

variable "url" {
  description = "Endpoint serving this host's variables as a JSON object."
  type        = string

  validation {
    condition     = startswith(var.url, "https://")
    error_message = "url must be https: the agent refuses to send its credential in cleartext."
  }
}

variable "auth_headers" {
  description = "Headers authenticating the agent to that endpoint, for example the CF-Access-Client-Id and CF-Access-Client-Secret of a Cloudflare Access service token."
  type        = map(string)
  sensitive   = true

  validation {
    condition     = length(var.auth_headers) > 0
    error_message = "auth_headers cannot be empty: the endpoint has to authenticate the host somehow."
  }
}

variable "compose_file" {
  description = "Path to a docker compose file on the host. Variables are passed to `docker compose` in its process environment, so $${VAR} interpolation works with nothing written to disk."
  type        = string
  default     = null
}

variable "systemd_units" {
  description = "Units taking a share of the variables through an EnvironmentFile. Each entry gets the variables matching its prefix, rendered to env_file (default <state_dir>/<unit>.env) and wired in through a drop-in, leaving any conffile the package ships untouched. A unit not installed on the host is skipped rather than failed."

  type = list(object({
    unit     = string
    prefix   = string
    env_file = optional(string)
    group    = optional(string)
  }))

  default = []

  validation {
    condition     = alltrue([for unit in var.systemd_units : endswith(unit.unit, ".service")])
    error_message = "each systemd_units entry must name a unit, for example alloy.service."
  }
}

variable "routed_files" {
  description = "Variables that also get a file of their own, as filename => variable, for images that read *_FILE rather than a value."
  type        = map(string)
  default     = {}
}

variable "env" {
  description = "Variables the caller owns rather than the secret store: values derived from resources terraform manages, plus non-secret ones a systemd unit cannot read from /etc/environment. Anything the endpoint serves wins over these."
  type        = map(string)
  default     = {}
}

variable "interval" {
  description = "OnCalendar expression for the timer."
  type        = string
  default     = "*:0/15"
}

variable "state_dir" {
  description = "Directory the agent keeps its rendered files and cache in."
  type        = string
  default     = "/opt/secrets"
}

variable "files_mode" {
  description = "Mode of the per-variable files. 0644 by default, because a container running as a non-root user has to read the file it was pointed at — outside swarm, compose bind-mounts these with the host's permissions."
  type        = string
  default     = "0644"
}

variable "redeploy_trigger" {
  description = "Reinstall when this value changes. Pass the host's id so a recreated host gets the agent again."
  type        = string
  default     = ""
}
