# Bootstrap

What `infra/` can't make for itself, or mustn't be able to change (specs/0002-deployment.md, Infrastructure). The maintainer applies it by hand, with their own credentials, and again whenever it changes. Nothing applies it automatically.

It holds:

- the APIs, which stay enabled if they're removed from here, since other teams use them too;
- the Terraform state bucket, `lanescape-downloads-tfstate`, which holds this configuration's state, and `lanescape-downloads-infra-manager`, where Infrastructure Manager keeps the deployment's artifacts and logs (it keeps `infra/`'s state itself);
- the Artifact Registry repository, `lanescape-downloads`;
- the Cloud Run service, `lanescape-downloads`, as an unreachable shell that `infra/` adopts and deploys, with the infra identity's grant on it alone;
- the domain mapping of `downloads.lanescape.dev` to it;
- every service account: the service's (`lanescape-downloads-service`), CD's (`lanescape-downloads-deployer`) and the infra identity (`lanescape-downloads-infra`);
- the custom roles and every project-level grant, each limited by IAM Conditions to the service's own resources by name wherever IAM can do that;
- CD's workload identity pool, `lanescape-downloads-github`, whose provider accepts only a push to `main` by `cd.yml` in this repository, checked by the repository's and the organization's IDs.

## What must exist first

- **Your access:** Owner of `lanescape-dev`, or enough to enable APIs, make service accounts, custom roles and project-level grants.
- **The domain:** you must be a verified owner of `lanescape.dev` (`gcloud domains list-user-verified`), since Cloud Run maps a domain only for one, and its record, `downloads.lanescape.dev CNAME ghs.googlehosted.com.`, must be in the zone. Both are already so.
- **The state bucket,** made by hand once, since this configuration's state lives in it:

  ```sh
  gcloud storage buckets create gs://lanescape-downloads-tfstate --project=lanescape-dev \
    --location=us-central1 --uniform-bucket-level-access --public-access-prevention
  gcloud storage buckets update gs://lanescape-downloads-tfstate --versioning
  ```

  The first apply adopts it (`state.tf`).

## Applying

```sh
cd bootstrap
gcloud auth application-default login
terraform init
terraform plan -out=bootstrap.tfplan
terraform apply bootstrap.tfplan
```

Read the plan before applying it: it's the only review this configuration's changes get beyond the pull request's. `infra/check.sh`, which CI runs, refuses any grant that replaces a policy or a role's members rather than adding one, and any grant to everyone.

Google provisions the domain's certificate once the mapping exists; `terraform output domain_records` shows the mapping's status.

## After the first apply

CD deploys nothing until the repository's Actions variables below are all set (specs/0002-deployment.md, CI and CD), so set them last. None of them is a secret:

```sh
repo=lanescape/downloads
gh variable set DOWNLOADS_WORKLOAD_IDENTITY_PROVIDER -R "$repo" --body "$(terraform output -raw workload_identity_provider)"
gh variable set DOWNLOADS_DEPLOYER -R "$repo" --body "$(terraform output -json accounts | jq -r .deployer)"
gh variable set DOWNLOADS_IMAGE_REPOSITORY -R "$repo" --body "$(terraform output -raw image_repository)"
gh variable set DOWNLOADS_INFRA_MANAGER_BUCKET -R "$repo" --body "$(terraform output -raw infra_manager_artifacts_bucket)"
gh variable set DOWNLOADS_SERVICE_URL -R "$repo" --body "$(terraform output -raw service_url)"
```

Then let CD deploy `infra/` for the first time: rerun the latest CD run on main, whose deploy was skipped, or push to main. That apply makes the secrets `config/groups.yaml` names, with no value, so it fails where the service would start. Add their values as `infra/README.md` says, then rerun the failed run's deploy.

## What IAM can't limit

IAM Conditions limit grants by name only for some resource types. The grants here that can't be limited, and so reach other teams' resources in the shared project, are:

- **Infrastructure Manager:** CD may create, read and update any deployment in the project, start previews, read any deployment's Terraform state and lock any deployment, which a preview of a deployment that exists needs, and read and release any deployment's lock, granted as a precaution since Google doesn't document whether a preview needs them. The infra identity holds the agent's state permissions on any deployment. CD can't delete a deployment, and can't make one run as any account but the infra identity.
- **Making secrets:** the infra identity may create a secret of any name, which touches nothing that exists, but can manage only those named `lanescape-downloads-*`.
- **Reads:** the infra identity may read the project and Cloud Run's operations, which Terraform polls.

Only a project of the service's own would remove these.
