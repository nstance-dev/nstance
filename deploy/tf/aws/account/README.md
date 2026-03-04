# Nstance Account Module (AWS)

Creates IAM roles and instance profiles for Nstance server and agent EC2 instances, with least-privilege permissions for EC2, S3, Secrets Manager, and ELB access.

## Usage

```hcl
module "account" {
  source  = "nstance-dev/nstance/aws//modules/account"
  version = "~> 1.0"

  cluster = module.cluster
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
