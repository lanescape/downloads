#!/usr/bin/env bash
# Refuses Terraform that infra/ and bootstrap/ must never hold
# (specs/0002-deployment.md). Run from the repository's root; ci.yml runs it
# on every pull request.
set -euo pipefail

cd "$(dirname "$0")/.."
failed=0

refuse() {
  local why=$1 pattern=$2
  shift 2
  local found
  if found=$(grep -rEn --include='*.tf' -e "$pattern" "$@"); then
    echo "$why:"
    echo "$found"
    failed=1
  fi
}

# Infrastructure Manager runs no provisioners or external programs as the
# infra identity, and nothing from outside this directory: no modules, and
# no JSON configuration a reader, or these checks, could miss.
refuse "infra/ may not use provisioners" '(^|[^a-z_])provisioner([^a-z_]|$)|(^|[^a-z_])connection[[:space:]]*\{' infra
refuse "infra/ may not run programs" 'local-exec|remote-exec' infra
refuse "infra/ may not use external data sources or the external provider" '"external"|hashicorp/external' infra
refuse "infra/ and bootstrap/ may not use modules" '^[[:space:]]*module[[:space:]]+"' infra bootstrap
if found=$(find infra bootstrap -name '*.tf.json' -o -name '*.tfvars.json' -o -name '*.tftest.json' | grep .); then
  echo "infra/ and bootstrap/ may not hold JSON configuration, which these checks don't read:"
  echo "$found"
  failed=1
fi

# Only Google's provider, from HashiCorp's registry.
if found=$(grep -rEn --include='*.tf' -e '^[[:space:]]*source[[:space:]]*=' infra bootstrap | grep -v '"hashicorp/google"'); then
  echo "infra/ and bootstrap/ may use only the hashicorp/google provider:"
  echo "$found"
  failed=1
fi

# Accounts, project-level grants, role definitions, log sinks and the
# domain mapping are bootstrap/'s, so a merged change can't widen any
# account's access or move the domain.
refuse "infra/ may not manage accounts, project-level grants, roles, log sinks or domain mappings" \
  'resource[[:space:]]+"google_(service_account[a-z_]*|project_iam[a-z_]*|organization_[a-z_]*|folder_[a-z_]*|logging_[a-z_]*sink[a-z_]*|iam_[a-z_]*|project_service|cloud_run_domain_mapping)"' infra

# The project is shared: every grant adds a member, and none replaces a
# policy or a role's members.
refuse "Grants must add members, never replace a policy or a role's members" \
  'resource[[:space:]]+"google_[a-z0-9_]*_iam_(policy|binding)"' infra bootstrap

# Nothing is granted to everyone: the service is public through Cloud Run's
# invoker check being off, not through IAM, and its secrets are its own.
refuse "Nothing may be granted to the public" 'allUsers|allAuthenticatedUsers' infra bootstrap

# A secret's value is added by hand, never through Terraform, whose state
# would hold it.
refuse "infra/ and bootstrap/ may not hold secrets' values" \
  'resource[[:space:]]+"google_secret_manager_secret_version"|data[[:space:]]+"google_secret_manager_secret_version' infra bootstrap

if [ "$failed" != 0 ]; then
  exit 1
fi
echo "infra/ and bootstrap/ hold nothing they may not."
