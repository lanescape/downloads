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

Tests need nothing but Go, at the version `go.mod` names, and run against this repository's own emulators.

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
