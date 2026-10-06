---
title: "Cloud Testing"
weight: 30
description: "Build local Nstance binaries and deploy standalone AWS or Google Cloud test clusters."
---

# Cloud Testing

The cloud test workflow deploys Nstance directly from the current checkout. It
does not require an Nstance release to test changes. OpenTofu state,
generated configuration, and built archives are kept under `temp/aws-test` or
`temp/google-test`.

Each provider has four commands:

```bash
make aws-prepare
make aws-upload
make aws-apply
make aws-destroy
```

Replace `aws` with `google` for Google Cloud. Preparation is normally run once.
Run `upload` again after changing Go code, then run `apply`. OpenTofu module and
configuration changes require only another `apply`.

The workflow requires Go, OpenTofu, `make`, `tar`, and the provider CLI. Building
the SQLite-enabled Linux server also requires `aarch64-linux-musl-gcc` for AWS
or `x86_64-linux-musl-gcc` for Google Cloud. Each command reports any missing
tool before making its relevant changes.

## AWS

Configure an AWS CLI profile, then prepare the deployment:

```bash
export AWS_PROFILE=default
export AWS_REGION=us-west-2
export AWS_ZONE=us-west-2a

make aws-prepare
make aws-upload
make aws-apply
```

The workflow builds arm64 binaries and uses the example's Graviton instance
types. It creates a private S3 artifact bucket and seven-day presigned download
URLs.

## Google Cloud

Authenticate both the Google Cloud CLI and Application Default Credentials,
then prepare the deployment:

```bash
gcloud auth login
gcloud auth application-default login

export GOOGLE_PROJECT=my-project
export GOOGLE_REGION=us-central1
export GOOGLE_ZONE=us-central1-a

make google-prepare
make google-upload
make google-apply
```

The workflow builds amd64 binaries. It creates a private Cloud Storage artifact
bucket and a reusable `nstance-dev-artifacts` service account that signs
12-hour download URLs without a service-account key.

## Configuration

Both providers default to the cluster ID `nstance-test` and Nstance NAT
instances. Override the ID during preparation with
`NSTANCE_TEST_CLUSTER_ID`. To use an existing private artifact bucket, set
`NSTANCE_TEST_ARTIFACT_BUCKET`; destroy removes only that cluster's object
prefix and does not delete a caller-supplied bucket.

The generated `deployment.auto.tfvars.json` and `main.tf` can be edited before
applying to exercise other configurations. Running `prepare` again regenerates
both files.

Signed URLs expire. Run the provider's `upload` command again before applying
or replacing instances after expiration.

## Cleanup

```bash
make aws-destroy
# or
make google-destroy
```

Destroy first stops nstance-server, removes Nstance-managed instances and the
cluster state bucket, and then destroys the remaining OpenTofu resources. It
also removes uploaded test artifacts and its generated local directory. The
Google Cloud signing service account is retained for subsequent test runs.
