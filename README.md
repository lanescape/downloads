# Lanescape Downloads

The service behind `https://downloads.lanescape.dev`, where the `lanescape` organization's downloads live. Apps embed these addresses and websites link to them, and they never change when where the files are stored does. A download's address is `https://downloads.lanescape.dev/<group>/<key>`, such as `https://downloads.lanescape.dev/lanescape/browser/v1/latest`: the service answers with a temporary redirect to the object at the group's provider, which serves it. So far each group has one provider, a private Cloudflare R2 bucket, which the service presigns a short-lived URL of.

The specs in [`specs/`](specs/) describe it:

- [0001 The service](specs/0001-service.md): groups, requests, providers, the configuration and how credentials are named, secrets, and the emulators.
- [0002 Code, tests and deployment](specs/0002-deployment.md): the region, the domain, Terraform through Infrastructure Manager, secrets in Secret Manager, and CI and CD on the runner service.

## Adding a group

Add it to [`config/groups.yaml`](config/groups.yaml), with its provider's account and bucket, and the names of the Secret Manager secrets that will hold a read-only token's access key ID and secret access key, named as specs/0001-service.md, Naming, says. Once the pull request is merged and deployed, the maintainer adds the secrets' values (`infra/README.md`) and reruns the deploy.

## Building and testing

```sh
go vet ./...
gofmt -l .
go test -race ./...
```

Tests need Go, at the version `go.mod` names, and run against this repository's own emulators. `go test -race` needs cgo, and so a C compiler: the runner service's image and GitHub's Ubuntu 26.04 have one, and on a Mac it's Xcode's command line tools. The tests of `cd/apply.sh` also run Bash 4.4 or later and jq: every supported system has jq at `/usr/bin/jq`, and Linux's `/bin/bash` is new enough, but a Mac's own is 3.2, so on a Mac they use Homebrew's, at `/opt/homebrew/bin/bash` or `/usr/local/bin/bash`, and skip without it.

## Supported systems

The code is built and tested on these systems, and nothing in the repository works around an older one (specs/0002-deployment.md, Supported systems):

- **Linux**, where CI and CD run: Debian 13 (trixie), with Linux 6.12 LTS, on the runner service's machines, which run this repository's own CI and CD and the merge policy, and Ubuntu 26.04 on GitHub's runners, which run a fork's pull request's CI, and CD's checks and gate in a fork, where the gate skips the deploy.
- **macOS 26.2** or later, for building and testing on a Mac, matching the macOS SDK the organization pins, `MacOSX26.2.sdk` from Xcode 26.3. No workflow runs on macOS.

These are where the code is built, not where the service runs, which is its image, on Debian 13 slim, on Cloud Run.

## Running it locally

Against the R2 emulator, with an object in its bucket:

```sh
echo 20261122.1 > /tmp/latest
go run ./cmd/r2-emulator -addr 127.0.0.1:9000 \
  -token 'read-id:read-secret:read:lanescape-browser' \
  -object 'lanescape-browser/v1/latest=/tmp/latest' &
cat > /tmp/secrets.yaml <<'YAML'
lanescape-downloads-lanescape-browser-r2-access-key-id: read-id
lanescape-downloads-lanescape-browser-r2-secret-access-key: read-secret
YAML
DOWNLOADS_SECRETS_FILE=/tmp/secrets.yaml DOWNLOADS_R2_ENDPOINT=http://127.0.0.1:9000 PORT=8080 \
  go run ./cmd/downloads &
curl -L http://127.0.0.1:8080/lanescape/browser/v1/latest
```

Licensed under the [MIT License](LICENSE).
