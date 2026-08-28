# Change Log
All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](http://keepachangelog.com/) and this project adheres to [Semantic Versioning](http://semver.org/).


## v1.0.0 - 2026-08-28
### What's Changed
#### 🚀 Features
* `worker/`: a Cloudflare Worker serving each host its own merged set of variables from the Secrets Store, behind Cloudflare Access with a per-host service token. Secrets Store values are write-only and readable only from a Worker binding, which is what makes them unusable as a secret source for a plain VM and why this exists.
* `cmd/secrets-agent`: the agent for the host side. Applies the fetched set to docker compose (variables passed in the process environment, so nothing is rendered as dotenv text), to grafana alloy (a systemd drop-in with a second `EnvironmentFile=`, leaving the package conffile alone), and to per-variable files for images that read `*_FILE`. Each consumer's applied-state is recorded only after its command succeeds, so a failed restart is retried rather than reported as no-change. Caches the last payload that applied cleanly, refuses to run unless its config is root-owned `0600` and the endpoint is `https`, writes atomically with `fsync`, and serialises runs with `flock`.
* `packaging/`: systemd unit and timer, verified on a live host.
