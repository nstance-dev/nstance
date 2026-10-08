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

The workflow requires Go, OpenTofu, Mise, `make`, `tar`, and the provider CLI.
Mise installs the pinned Zig cross-compiler used to build the SQLite-enabled
server against a glibc 2.36 baseline, independent of the host operating system.
Each command reports any missing tool before making its relevant changes.
Cleanup also requires `jq` to read resource ownership from OpenTofu state and
Google Cloud route metadata.

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

The binary download URLs expire. If you need to apply changes or replace
instances after they expire, run the provider's `upload` command again first.

### Test load balancer

The test configuration includes a public TCP load balancer named `workers`:
an AWS Network Load Balancer (NLB) or a Google Cloud regional passthrough load
balancer. **This adds cloud charges.** Its purpose is to let you send requests to
the example's worker instances and, with a wake proxy installed, test waking
them through incoming traffic.

Requests arrive on port `8080` and are forwarded to port `8080` on instances in
the example's `workers` group. While those instances are asleep, a wake proxy
receives requests instead. AWS uses a separate port for that proxy;
Google Cloud's passthrough load balancer requires the same port throughout:

| Provider | Public listening port | Worker port | Wake-proxy port |
|----------|-----------------------|-------------|-----------------|
| AWS | `8080` | `8080` | `18080` |
| Google Cloud | `8080` | `8080` | `8080` |

The public load balancer exposes only the test service. The Nstance admin APIs
remain private.

After applying, find the address and port to connect to with:

```bash
tofu -chdir=temp/aws-test output -json load_balancer_endpoints
# or
tofu -chdir=temp/google-test output -json load_balancer_endpoints
```

Look for the `workers` entry in the output. AWS returns a DNS name; Google Cloud
returns an IP address.

If you only want to test Nstance without a load balancer, set
`"load_balancers": {}` in the generated `deployment.auto.tfvars.json` before
applying. Only the disposable test workflow enables it by default; the
single-shard examples and production network modules do not.

If you customise the network, the load balancer uses the `public` subnet role,
workers use `workers`, and the proxy is expected alongside the test server in
`public`. If you move the server, change `proxy_subnets` to match. The Google
Cloud example also lists its zone in `cluster.shards` to create the right
load-balancer firewall rules; this is deployment configuration, not a list of
shards for nstance-server to coordinate.

For a test prepared before this load-balancer support was added, rerun
`make aws-prepare` or `make google-prepare` before applying. Preparation
regenerates `main.tf` and the deployment variables, restores the default load
balancer, and overwrites custom settings in those files. Reapply any custom
settings afterward.

## Local admin CLI

Both cloud test deployments keep the Nstance Server APIs private. The AWS and
Google Cloud CLIs can tunnel the operator API to the local machine without
adding a public listener or Internet-wide firewall rule.

### AWS

Create a local operator identity for the `nstance-admin` CLI using the same AWS
profile and region as the test deployment:

```bash
make aws-admin
```

The generated identity is retained under `temp/aws-test/operator-identity`.
Re-running the target reuses that identity. The target also writes connection
defaults to `temp/aws-test/admin.env`.

Next, start an SSM Session Manager port-forwarding session in one terminal:

```bash
make aws-portfwd
```

This resolves the EC2 instance and private address of the shard leader ENI,
then forwards `127.0.0.1:18993` through that instance to its operator API. It
requires the Session Manager plugin in addition to the AWS CLI. Set
`NSTANCE_ADMIN_LOCAL_PORT` to use a different local port, or
`NSTANCE_ADMIN_REMOTE_PORT` if the deployment's operator bind port was
customized.

With the port-forwarding session open, shard commands can use the local
endpoint:

```bash
source temp/aws-test/admin.env

./bin/nstance-admin group list
./bin/nstance-admin group scale workers 3
```

Scaling to `0` scales the group down completely. The gRPC connection remains
mutually authenticated: SSM supplies the private transport, while
`nstance-admin` still verifies the server CA and presents its operator client
certificate.

### Google Cloud

Create a local operator identity for the `nstance-admin` CLI using the same
Google Cloud project as the test deployment:

```bash
make google-admin
```

The generated identity is retained under `temp/google-test/operator-identity`.
Re-running the target reuses that identity. The target also writes connection
defaults to `temp/google-test/admin.env`.

Next, start an IAP TCP-forwarding session in one terminal:

```bash
make google-portfwd
```

This forwards `127.0.0.1:28993` directly to the shard's stable private leader
address using IAP TCP forwarding. The deployment permits the operator port only
from Google's IAP forwarding range, so the API is not exposed to the public
internet and no SSH key is required. The same `NSTANCE_ADMIN_LOCAL_PORT` and
`NSTANCE_ADMIN_REMOTE_PORT` overrides are supported.

With the port-forwarding session open, use the local endpoint:

```bash
source temp/google-test/admin.env

./bin/nstance-admin group list
./bin/nstance-admin group scale workers 3
```

### Tenant state

After building `nstance-admin` and sourcing the provider's `admin.env`, inspect
the test tenant with:

```bash
./bin/nstance-admin tenant status default
```

The corresponding `tenant sleep default` and `tenant wake default` commands
are also available. Sleep checks activity by default; `--force` skips that
check, and `--wake-at` accepts an RFC3339 wake deadline. These commands report
durable tenant state, not completion of VM termination or startup.

Before putting the tenant to sleep, make sure the worker backend and wake proxy
are healthy. `--force` skips the activity check, but not the checks that ensure
traffic can be routed safely.

## Cleanup

```bash
make aws-destroy
# or
make google-destroy
```

Destroy first stops nstance-server, removes Nstance-managed instances and the
cluster's runtime-created secrets and state bucket, and then destroys the
remaining OpenTofu resources. Google Cloud cleanup also deletes runtime-created
NAT44 and NAT64 routes belonging to the cluster and its network before network
deletion. It removes uploaded test artifacts, deleting an owned artifact bucket
only after removing the cluster's object prefix; unrelated objects are never
purged to force bucket deletion.

Both providers verify that non-terminated instances, artifact objects, and
managed OpenTofu resources are gone before removing their generated local
directory. Google Cloud also verifies deletion of owned runtime NAT routes.
AWS waits for runtime instances to finish terminating, including instances
already shutting down, and ignores normal terminated EC2 records. If cleanup
fails, the directory and remaining state are retained; resolve the reported
error and rerun the same destroy command. Already-missing buckets and empty
artifact prefixes are safe to retry, while permission and deletion errors
remain visible.

The reusable Google Cloud signing service account, its impersonation permission,
and project APIs are retained for subsequent test runs.
