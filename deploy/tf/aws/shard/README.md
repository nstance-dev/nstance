# Nstance Shard Module (AWS)

Deploys a single Nstance shard including security groups, server instances (via Auto Scaling Groups), shard configuration, and group definitions for agent instance pools.

Set `server_userdata` to use a complete external server image configuration,
including optional proxy and tunnel services. When omitted, the module
uses its built-in nstance-server installer.

## Usage

```hcl
module "shard" {
  source  = "nstance-dev/nstance/aws//modules/shard"
  version = "~> 1.0"

  cluster = module.cluster
  account = module.account
  network = module.network

  shard = "us-west-2a"
  zone  = "us-west-2a"

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
