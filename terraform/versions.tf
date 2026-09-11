terraform {
  # Matches the rest of the estate. The features used here need less — terraform_data
  # landed in 1.4 — but 1.11 is what this was validated against, and a floor nobody
  # tested is a claim rather than a constraint.
  required_version = ">= 1.11"
}

# No required_providers on purpose. The module reaches the host over SSH and uses
# terraform_data, which terraform implements itself — so it installs the agent on
# anything reachable by SSH, whoever created it, without pulling a provider into the
# caller's lock file.
