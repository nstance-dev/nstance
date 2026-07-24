# Nstance Cluster Module (AWS)

Creates shared cluster resources including a cluster ID and S3 bucket for config/state storage. Secrets default to direct AWS Systems Manager Parameter Store under `/nstance/<cluster-id>/`. Object storage can be selected explicitly; its encryption key defaults to a safely generated SecureString in Parameter Store and can instead use AWS Secrets Manager.

Requires Terraform/OpenTofu 1.11 or newer and AWS provider 6.8 or newer for ephemeral key generation and write-only SSM parameter values.

## Usage

```hcl
module "cluster" {
  source  = "nstance-dev/nstance/aws//modules/cluster"
  version = "~> 1.0"

  cluster_id = "my-cluster"
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
