# Infrastructure

The service's own resources (specs/0002-deployment.md, Infrastructure):

- the service on Cloud Run, `lanescape-downloads`, adopted from the shell `bootstrap/` makes: public, scaled to zero and to at most two instances, with each secret mounted from Secret Manager;
- the metadata of the secrets `config/groups.yaml` names, and the service's access to each.

The accounts, every project-level grant, the images' repository and the domain mapping are in `bootstrap/`, which only the maintainer applies.

## Who applies it

Only Infrastructure Manager, from CD, as `lanescape-downloads-infra`, never anyone by hand. That identity can change nothing but these resources: secrets named `lanescape-downloads-*`, and the Cloud Run service by a grant on it alone. It can't make or delete a Cloud Run service. CD (`.github/workflows/cd.yml`, `cd/apply.sh`) first asks for a preview, then starts the apply, from this repository at the commit it has just built, through Infrastructure Manager's API itself, as `lanescape/runners` does; the request is the same as this:

```sh
gcloud infra-manager deployments apply \
  projects/lanescape-dev/locations/us-central1/deployments/lanescape-downloads \
  --service-account=projects/lanescape-dev/serviceAccounts/lanescape-downloads-infra@lanescape-dev.iam.gserviceaccount.com \
  --git-source-repo=https://github.com/lanescape/downloads.git \
  --git-source-directory=infra \
  --git-source-ref="$GITHUB_SHA" \
  --artifacts-gcs-bucket=gs://lanescape-downloads-infra-manager \
  --tf-version-constraint=1.5.7 \
  --input-values="^;^service_image=<image>@sha256:<digest>;deploy_id=<run ID>-<attempt>;secret_names=<names, comma-separated>"
```

`secret_names` is what `go run ./cmd/downloads -secret-names` prints: the configuration's secrets' names, exactly as it writes them. `deploy_id` makes every apply a new revision, so a rerun of a deploy whose revision couldn't start tries again. Terraform here must work with 1.5.7, Infrastructure Manager's newest; CI validates it with that and the latest.

## Adding the secrets' values

An apply makes each secret the configuration names with no value, and the service refuses to start until every one has one. Add each value once, by hand, from the R2 API token made for the group: a token with Object Read on the group's bucket alone, never one that may write it. `printf '%s'` keeps a newline out of the value, and reading it with `read -s` keeps it out of your shell's history:

```sh
read -rs value && printf '%s' "$value" | gcloud secrets versions add lanescape-downloads-lanescape-browser-r2-access-key-id --project=lanescape-dev --data-file=-
read -rs value && printf '%s' "$value" | gcloud secrets versions add lanescape-downloads-lanescape-browser-r2-secret-access-key --project=lanescape-dev --data-file=-
unset value
```

Then rerun the failed CD run's deploy. An instance reads the secrets' latest versions as it starts.

To replace a token, make the new one in Cloudflare, add both of its values as above, then rerun the latest CD run's deploy, which starts a new revision whose every instance has the new pair. An instance that started between the two `versions add` may hold one value of each token until then, and its redirects fail. Revoke the old token only once the new revision serves and the URLs signed with the old one have expired, `url_lifetime` after it.

## What the service is given

`DOWNLOADS_SECRETS_DIR`, `/secrets`, where each secret is mounted at `<name>/value`, and `PORT`, 8080, as `cmd/downloads` reads them.
