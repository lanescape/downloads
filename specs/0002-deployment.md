# Code, tests and deployment

## Code and tests

The service is Go, at the version `go.mod`'s `toolchain` line pins. Every behaviour has a test, against this repository's emulators where it involves a provider (specs/0001-service.md, Emulators). Changing what a spec describes changes the spec in the same pull request.

## Supported systems

The code is built and tested on these systems, and nothing in the repository works around an older one: an older kernel, distribution, macOS or tool is out of scope.

- **The runner service's machines**, which run this repository's own CI and CD and every pull request's merge policy, a fork's included: Debian 13 (trixie), with its Linux 6.12 LTS kernel. Workflows rely on the tools the runner service's image ships, among them Bash 5.2, a C compiler, `gh`, `jq` at `/usr/bin/jq`, `curl` and `unzip`. They set up Go at the version `go.mod` pins, CI installs the Terraform versions it pins, and the deploy sets up `gcloud`.
- **GitHub's runners**, which run a fork's pull request's CI, and CD's checks and gate when a fork's own main is pushed, where the gate skips the deploy: Ubuntu 26.04, `ubuntu-26.04`, whose image has the system tools those jobs rely on; there, as on the runner service, they set up Go and CI installs its pinned Terraform versions.
- **macOS 26.2** or later, for anyone building or testing on a Mac, matching the macOS SDK the organization pins, `MacOSX26.2.sdk` from Xcode 26.3. No workflow runs on macOS. A Mac's own Bash is 3.2, older than CD's script needs, so its tests run with Homebrew's Bash, and skip without it (README.md, Building and testing).

These are the systems the code is built and tested on, not the one the service runs on: its image is the program on Debian 13 slim, on Cloud Run (The Cloud Run service, below), and the program, built without cgo, needs nothing from it.

## Region

The service runs in project `lanescape-dev`, shared with other teams, so everything it owns is named `lanescape-downloads-*`. It runs on Cloud Run in Iowa, `us-central1`, the region of its images, its secrets and its Infrastructure Manager deployment, because Cloud Run maps a custom domain to a service only in some regions, and Montreal, the region of the organization's other services, isn't yet one of them. Its providers are elsewhere anyway: R2 stores the browser's bucket in the US. When Cloud Run maps domains in Montreal, the service moves there; its addresses don't change.

## Domain

`downloads.lanescape.dev` is mapped to the service by a Cloud Run domain mapping, which Google provisions a certificate for. Its DNS record, `downloads.lanescape.dev CNAME ghs.googlehosted.com.`, is in the `lanescape.dev` zone in Cloud DNS. Google describes domain mappings as a preview, with added latency; the service accepts that until it can move.

## Infrastructure

Two Terraform configurations, as `lanescape/runners` has:

- **`bootstrap/`**, which the maintainer applies by hand, holds what the service's own infrastructure mustn't be able to change: the APIs, the service's accounts (`lanescape-downloads-service`, `-deployer` and `-infra`), every project-level grant and custom role, CD's workload identity pool, the images' Artifact Registry repository, the state and Infrastructure Manager buckets, the Cloud Run service as an unreachable shell, and the domain mapping, which only a verified owner of the domain may make.
- **`infra/`**, which only Infrastructure Manager applies, from CD, as `lanescape-downloads-infra`, holds the Cloud Run service, adopted from the shell, and the metadata of the secrets the configuration names, with the service's access to them.

`infra/check.sh`, which CI runs, refuses provisioners, programs, external data and modules; accounts, project-level grants, roles, log sinks and domain mappings in `infra/`; any grant that replaces a policy rather than adding a member; any grant to everyone; and any secret's value.

### The Cloud Run service

- Public: Cloud Run's invoker check is off, since it serves public downloads, and nothing is granted to `allUsers`.
- No instance while nothing is asked for, and a maximum of two set for the service as a whole, a rollout's old and new revisions together, which Cloud Run can briefly exceed. An instance answers many requests at once, each only a signature, with a 30-second timeout, 1 CPU and 256 MiB, CPU only during requests.
- Its image is built by `ko` from `cmd/downloads`, on Debian 13 slim, named by digest (`.ko.yaml`), for linux/amd64, and deployed by digest.
- Its account holds no project role. It may read only its own secrets.

### Secrets

The secrets are the ones `config/groups.yaml` names, exactly as it names them: CD lists them with `go run ./cmd/downloads -secret-names` and passes them to `infra/`, which makes each in Secret Manager, metadata only, replicated in the region, with deletion protection, lets the service's account read it, and mounts its latest version at `/secrets/<name>/value`. The maintainer adds each value by hand, with `gcloud secrets versions add`; Terraform never holds one, since its state would. The infra identity's grant on secrets is limited to names starting `lanescape-downloads-`. An instance reads the latest versions as it starts, so replacing a token is: add the new access key ID and secret access key, wait for Secret Manager's eventually consistent `latest` to name them, redeploy, which starts a revision whose instances all have the new pair, and revoke the old token once that revision serves and the URLs it signed have expired (infra/README.md).

Adding a group is a pull request that adds it to `config/groups.yaml`. Once it's deployed, its secrets exist without values, and the service can't start a new revision until the maintainer adds them; the deploy fails there, and is rerun once they're added.

## CI and CD

Workflows run on the runner service, `lanescape-2vcpu-linux-amd64`, through `lanescape/runners/actions/checkout@v1`, or on GitHub's runners for a fork's pull request's CI and for CD's checks and gate in a fork (Supported systems). They use the latest stable GitHub Actions releases, pinned by commit.

- **CI**, on every pull request: `go vet`, `gofmt`, `go test -race`, actionlint, `infra/check.sh`, and `terraform fmt` and `validate` of both configurations, `infra/` with 1.5.7, the Terraform CD has Infrastructure Manager use, too.
- **CD**, on every push to main: the same checks, then a gate, which skips the deploy unless this is the repository, by ID, the variables below are set and look as they should, and the commit is still main's head. The deploy signs in with GitHub's token through the workload identity pool, which accepts only a push to main by `cd.yml` in this repository, as `lanescape-downloads-deployer`; builds and pushes the image; lists the secrets; asks Infrastructure Manager for a preview of `infra/` at the commit, then applies it, printing both one's logs (`cd/apply.sh`); and, once applied, runs the smoke check.
- **The smoke check**, `go run ./cmd/downloads -smoke-check <URL>`, asks the deployed service, at its `run.app` URL, for `smoke-check/<random>`, a fresh key no group has, in every group, and expects a redirect to the provider that the provider answers with `404 NoSuchKey`: the signature and the token's access were accepted, since a wrong signature or a token without access is refused with `403` first. It never prints the redirect's URL.

CD is given, as repository variables, none of them secret: `DOWNLOADS_WORKLOAD_IDENTITY_PROVIDER`, `DOWNLOADS_DEPLOYER`, `DOWNLOADS_IMAGE_REPOSITORY`, `DOWNLOADS_INFRA_MANAGER_BUCKET` and `DOWNLOADS_SERVICE_URL`, from `bootstrap/`'s outputs (bootstrap/README.md). The repository has no GitHub secrets: the providers' credentials are in Secret Manager.

## Merging

Every commit and pull request is attributable to a person, who has write access to this repository. A fork's pull request runs CI on GitHub's runners but doesn't merge: a maintainer brings the change onto a branch here (`merge-policy.yml`).
