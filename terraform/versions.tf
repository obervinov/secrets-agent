terraform {
  # optional() defaults in variable types need 1.3; terraform_data needs 1.4.
  required_version = ">= 1.4"
}

# No required_providers on purpose. The module reaches the host over SSH and uses
# terraform_data, which terraform implements itself — so it installs the agent on
# anything reachable by SSH, whoever created it, without pulling a provider into the
# caller's lock file.
