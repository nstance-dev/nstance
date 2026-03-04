# Nstance Cluster Module (AWS)

Creates shared cluster resources including a cluster ID, S3 bucket for config/state storage, and encryption key in AWS Secrets Manager.

## Usage

```hcl
module "cluster" {
  source  = "nstance-dev/nstance/aws//modules/cluster"
  version = "~> 1.0"

  cluster_id = "my-cluster"
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
