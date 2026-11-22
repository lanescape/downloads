# The service

`https://downloads.lanescape.dev` is where the organization's downloads live: the addresses that apps embed and websites link to, which never change when where the files are stored does. The service holds no files. It answers each request with a temporary redirect to the file at its group's provider, which serves the bytes.

## Groups

A group is a set of downloads under one path, such as `lanescape/browser`. Its name may match a repository's, but needn't: a group is whatever the organization publishes together. A group's path is one or more segments of lowercase letters, digits and hyphens, separated by `/`, and no group's path is within another's, so every request belongs to at most one group.

A download's address is `https://downloads.lanescape.dev/<group>/<key>`, such as `https://downloads.lanescape.dev/lanescape/browser/v1/latest`, and `<key>` is the object's key at the provider. What keys a group has is its publisher's to decide; `lanescape/browser`'s are in that repository's specs/0004-releases.md.

## Requests

- `GET` or `HEAD` of `/<group>/<key>` is answered `307 Temporary Redirect`, with `Location` a URL of the object at the group's provider that is good for the configuration's `url_lifetime`, signed for that method alone. The client follows it to the provider, which answers as it does for any object: the object, or `404` if there's none.
- The key is at most 1,024 bytes, S3's limit, and its segments are letters, digits, `.`, `_` and `-`, and none is `.` or `..`. Anything escaped in the path, and an empty segment, is refused. The query is ignored. These hold for requests as they reach the service: Cloud Run's front end may decode some escapes first, and answers some methods, such as `TRACE` and `CONNECT`, itself.
- Every other method is `405`, with `Allow: GET, HEAD`, `OPTIONS *` too. A path in no group, or a key that isn't allowed, is `404`.
- Every answer is `Cache-Control: no-store`: a path such as `v1/latest` changes, and a redirect is only as good as its URL's signature.
- The service doesn't check that the object exists, which would cost a request to the provider for every download; the provider says so itself.

The log records each request's method, status, group and key, or the path asked for when there's no key. A redirect's URL is a credential for its object until it expires: it's never logged, and nor is anything a provider signs with.

## Providers

Each group has one provider. The only kind so far is a Cloudflare R2 bucket. Spreading a group over several providers, or sending a client to the one nearest, can come later without changing any address.

**R2.** The bucket is private: R2 serves a bucket publicly only from a custom domain on a Cloudflare zone or from its rate-limited development URL, and the service needs neither. The service presigns, with AWS Signature Version 4 in the query, a path-style URL on R2's S3 API, `https://<account ID>.r2.cloudflarestorage.com/<bucket>/<key>`, with R2's region, `auto`, and an unsigned payload. Presigning is local: the service makes no request to R2. It signs with an R2 API token that may only read the group's bucket (Object Read, scoped to it). R2 doesn't charge for data it serves, through the S3 API or otherwise.

## Configuration

`config/groups.yaml` declares every group and its provider, and is embedded into the service. It holds only what isn't secret, in plain text: for R2, the account's ID and the bucket's name. Each credential is referred to by the name of the Secret Manager secret that holds it, in a field ending in `_secret_name`, such as `access_key_id_secret_name`; the field holds the secret's name, never its value. A secret's name is always written out in full in the configuration, and nothing derives it from a group's or a provider's name.

```yaml
url_lifetime: 1h
groups:
  lanescape/browser:
    r2:
      account_id: <the Cloudflare account's ID>
      bucket: lanescape-browser
      access_key_id_secret_name: lanescape-downloads-lanescape-browser-r2-access-key-id
      secret_access_key_secret_name: lanescape-downloads-lanescape-browser-r2-secret-access-key
```

The service refuses to start if the configuration is invalid: an unknown field, a group's path that isn't allowed, one group within another, two groups whose paths differ only in `/` and `-`, an account ID or bucket name R2 wouldn't have, a `url_lifetime` outside a minute to 7 days, R2's limit, or a secret's name that isn't a Secret Manager secret's, doesn't start with `lanescape-downloads-`, or is named twice. The prefix is required because the project is shared, and the infrastructure's grants on secrets reach only names that start with it (specs/0002-deployment.md, Secrets).

### Naming

The organization names credentials the same way everywhere, so that a name says what it holds:

- **A secret of this service's**, in Secret Manager: `lanescape-downloads-<group, with / as ->-<provider>-<field>`, such as `lanescape-downloads-lanescape-browser-r2-secret-access-key`.
- **A publisher's GitHub secret**, in the environment of the repository that uploads to one provider: `<PROVIDER>_<FIELD>`, upper case, such as `R2_ACCESS_KEY_ID` and `R2_SECRET_ACCESS_KEY`. What isn't secret, such as the account and the bucket, is in that repository's own files.
- **Fields:** R2's are `account-id`, `bucket`, `access-key-id` and `secret-access-key`.

A group's publisher holds a token that may write its bucket; this service holds a different one, that may only read it.

## Secrets

At startup the service reads the value of every secret the configuration names, and refuses to start, naming the secret, if any has none. On Cloud Run each is mounted at `/secrets/<name>/value` (`DOWNLOADS_SECRETS_DIR`). Outside Cloud Run, `DOWNLOADS_SECRETS_FILE` names a YAML or JSON file that maps each secret's name to its value instead, for running the service locally and for tests. Surrounding whitespace is dropped from a value. No error or log line holds a value.

## Emulators

Tests use only this repository's emulators. `emulators/r2` emulates what the service and its tests use of R2's S3 API: path-style `GET`, `HEAD` and `PUT` of objects, signed in a header or a presigned URL, verified by its own implementation of Signature Version 4, which its tests hold to AWS's published examples, rather than by the signer the service uses. Like R2, it refuses a wrong signature, and an expired one with `403 ExpiredRequest`, and a header-signed request with an `x-amz-` header it didn't sign, gives each token read or read and write access to its own buckets, evaluates `If-Match`, compared strongly, and `If-None-Match`, compared weakly, on a write atomically, answering `412 PreconditionFailed`, and gives back an object's quoted ETag, `Content-Type`, `Cache-Control` and `x-amz-meta-*`. `cmd/r2-emulator` serves it for a local run, with `DOWNLOADS_R2_ENDPOINT` pointing the service at it.
