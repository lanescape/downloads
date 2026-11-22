terraform {
  # The Terraform CD has Infrastructure Manager use, pinned with
  # --tf-version-constraint. No backend: Infrastructure Manager keeps the
  # state.
  required_version = ">= 1.5.7"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.10"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
