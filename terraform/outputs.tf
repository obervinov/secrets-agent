output "binary_path" {
  description = "Where the agent was installed."
  value       = local.binary_path
}

output "config_path" {
  description = "Configuration file the agent reads. Root-owned, mode 0600."
  value       = local.config_path
}

output "state_dir" {
  description = "Directory holding the rendered files and the cached payload."
  value       = var.state_dir
}

output "unit" {
  description = "Systemd unit installed. `systemctl start` it to apply changed secrets immediately."
  value       = "secrets-agent.service"
}
