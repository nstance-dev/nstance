# Nstance Shard Module (AWS)

Deploys a single Nstance shard including security groups, server instances (via Auto Scaling Groups), shard configuration, and group definitions for agent instance pools.

Set `server_userdata` to use a complete external server image configuration,
including optional proxy and tunnel services. When omitted, the module
uses its built-in nstance-server installer.

Rolling server updates wait 60 seconds after each replacement enters
`InService`. Set `server_instance_warmup_seconds = 300` on the shard module
for a longer wait, or `0` to skip it. This is a fixed delay, not a Nstance
readiness check; allow enough time for your userdata script to finish. The 
separate EC2 health-check grace period remains 300 seconds.

## Usage

```hcl
module "shard" {
  source  = "nstance-dev/nstance/aws//modules/shard"
  version = "~> 2.0"

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
