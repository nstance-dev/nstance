# Nstance Cluster Module (Google Cloud)

Creates shared cluster resources including a cluster ID, GCS bucket for config/state storage, and encryption key in Google Cloud Secret Manager. Automatically enables required Google Cloud APIs.

## Usage

```hcl
module "cluster" {
  source  = "nstance-dev/nstance/google//modules/cluster"
  version = "~> 1.0"

  cluster_id = "my-cluster"
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
