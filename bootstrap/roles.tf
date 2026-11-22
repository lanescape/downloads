# The service's custom roles and every project-level grant.
#
# The project holds other teams' resources, so each grant is limited by IAM
# Conditions to the service's own resources by name wherever IAM can do
# that: here, Secret Manager secrets. The Cloud Run service's grant is on the
# service itself (run.tf). Infrastructure Manager, creating a secret and a
# few reads can't be limited by name; those grants are apart and say so
# (README.md, What IAM can't limit). Every grant here adds a member; none
# replaces a role's or the project's policy.

# --- The infra identity -----------------------------------------------------
#
# It holds no project-level IAM administration, no service account creation
# or keys, no role definitions and no log sinks, so a merged change can't
# widen what any account holds in the project (specs/0002-deployment.md).

# The secrets infra/ manages: only their metadata and who may read them,
# never a value. Their names are config/groups.yaml's, each starting
# lanescape-downloads-, and only such secrets are reached.
resource "google_project_iam_custom_role" "infra_named" {
  role_id     = "lanescapeDownloadsInfraNamed"
  title       = "Lanescape Downloads infrastructure, by name"
  description = "Manages the service's secrets' metadata and access. Granted only on lanescape-downloads- names."
  permissions = [
    "secretmanager.secrets.delete",
    "secretmanager.secrets.get",
    "secretmanager.secrets.getIamPolicy",
    "secretmanager.secrets.setIamPolicy",
    "secretmanager.secrets.update",
  ]
  depends_on = [google_project_service.apis]
}

resource "google_project_iam_member" "infra_named" {
  project = var.project_id
  role    = google_project_iam_custom_role.infra_named.name
  member  = google_service_account.infra.member

  condition {
    title       = "lanescape-downloads-infra-secrets"
    description = "Only the service's own secrets, named lanescape-downloads-*."
    expression  = <<-EOT
      resource.type == "secretmanager.googleapis.com/Secret" &&
      resource.name.startsWith("projects/${data.google_project.this.number}/secrets/${local.secret_prefix}")
    EOT
  }
}

# What can't be limited by name: making a secret (which touches nothing
# that exists), reading the project and Cloud Run's operations (Terraform
# polls its own), writing Infrastructure Manager's logs, and the state
# permissions of Infrastructure Manager's own agent role, without its
# project-wide Cloud Storage and Cloud Build access.
resource "google_project_iam_custom_role" "infra_project" {
  role_id     = "lanescapeDownloadsInfraProject"
  title       = "Lanescape Downloads infrastructure, project"
  description = "Creates the service's secrets and runs Infrastructure Manager's applies. Holds nothing that changes an existing resource."
  permissions = [
    "config.artifacts.import",
    "config.deployments.deleteState",
    "config.deployments.getLock",
    "config.deployments.getState",
    "config.deployments.updateState",
    "config.previews.upload",
    "config.revisions.getState",
    "logging.logEntries.create",
    "resourcemanager.projects.get",
    "run.operations.get",
    "secretmanager.secrets.create",
  ]
  depends_on = [google_project_service.apis]
}

resource "google_project_iam_member" "infra_project" {
  project = var.project_id
  role    = google_project_iam_custom_role.infra_project.name
  member  = google_service_account.infra.member
}

# Deploys the service on Cloud Run, granted on it alone (run.tf). It can't
# delete it.
resource "google_project_iam_custom_role" "infra_run" {
  role_id     = "lanescapeDownloadsInfraRun"
  title       = "Lanescape Downloads infrastructure, Cloud Run"
  description = "Deploys the service's Cloud Run service. Granted only on lanescape-downloads."
  permissions = [
    "run.services.get",
    "run.services.getIamPolicy",
    "run.services.setIamPolicy",
    "run.services.update",
  ]
  depends_on = [google_project_service.apis]
}

# --- CD ---------------------------------------------------------------------

# Starts applies and previews of the lanescape-downloads deployment, and
# reads their results. A preview of a deployment that exists reads its
# Terraform state and locks the deployment while it plans, so the deployer
# may read the state and take the lock; reading and releasing the lock are
# granted too, as a precaution, since Google doesn't document which a
# preview checks. Infrastructure Manager can't be limited by name in IAM;
# the deployer can't delete a deployment, and changing one to run as any
# account but lanescape-downloads-infra needs acting as that account.
resource "google_project_iam_custom_role" "deployer" {
  role_id     = "lanescapeDownloadsDeployer"
  title       = "Lanescape Downloads CD"
  description = "Starts applies and previews of the lanescape-downloads Infrastructure Manager deployment."
  permissions = [
    "config.deployments.create",
    "config.deployments.get",
    "config.deployments.getLock",
    "config.deployments.getState",
    "config.deployments.lock",
    "config.deployments.unlock",
    "config.deployments.update",
    "config.operations.get",
    "config.previews.create",
    "config.previews.delete",
    "config.previews.export",
    "config.previews.get",
    "config.revisions.get",
    "config.revisions.list",
  ]
  depends_on = [google_project_service.apis]
}

resource "google_project_iam_member" "deployer" {
  project = var.project_id
  role    = google_project_iam_custom_role.deployer.name
  member  = google_service_account.deployer.member
}
