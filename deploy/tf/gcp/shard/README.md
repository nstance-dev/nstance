# Nstance Shard Module (GCP)

Deploys a single Nstance shard including firewall rules, server instances (via Managed Instance Groups), shard configuration, and group definitions for agent instance pools.

## Usage

```hcl
module "shard" {
  source  = "nstance-dev/nstance/gcp//modules/shard"
  version = "~> 1.0"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-central1-a"
  zone  = "us-central1-a"

  groups = {
    "default" = {
      "workers" = {
        size        = 1
        subnet_pool = "workers"
      }
    }
  }
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
