terraform {
  required_version = ">= 1.11"
}

# Hands the worker source to a caller that deploys it, so the script lives in exactly
# one place. Without this, anyone deploying the worker keeps a copy in their own
# repository and the two drift — which is the whole reason this module exists.
#
# `?ref=` pins it: a caller gets the script that shipped with that release, not
# whatever is on main.

locals {
  script_path = "${path.module}/../../worker/secrets-proxy.js"
}
