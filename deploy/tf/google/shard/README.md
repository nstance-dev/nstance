# Nstance Shard Module (Google Cloud)

Deploys a single Nstance shard including firewall rules, server instances (via Managed Instance Groups), shard configuration, and group definitions for agent instance pools.

Set `server_userdata` to use a complete external server image configuration,
including optional proxy and tunnel services. When omitted, the module
uses its built-in nstance-server installer.

## Usage

```hcl
module "shard" {
  source  = "nstance-dev/nstance/google//modules/shard"
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
