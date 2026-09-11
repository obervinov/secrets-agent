output "script" {
  description = "Source of the worker, to pass as `content` on cloudflare_workers_script."
  value       = file(local.script_path)
}

output "script_sha256" {
  description = "Hash of that source, for `content_sha256`."
  value       = filesha256(local.script_path)
}

output "main_module" {
  description = "File name the worker's entrypoint is expected under."
  value       = "secrets-proxy.js"
}
