# Nstance Cluster Module (GCP)

Creates shared cluster resources including a cluster ID, GCS bucket for config/state storage, and encryption key in GCP Secret Manager. Automatically enables required GCP APIs.

## Usage

```hcl
module "cluster" {
  source  = "nstance-dev/nstance/gcp//modules/cluster"
  version = "~> 1.0"

  cluster_id = "my-cluster"
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
