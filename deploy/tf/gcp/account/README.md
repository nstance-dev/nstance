# Nstance Account Module (GCP)

Creates service accounts for Nstance server and agent instances with least-privilege IAM bindings for Compute, Storage, and Secret Manager access.

## Usage

```hcl
module "account" {
  source  = "nstance-dev/nstance/gcp//modules/account"
  version = "~> 1.0"

  cluster = module.cluster
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
