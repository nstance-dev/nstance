# Nstance Network Module (Google Cloud)

Creates VPC network infrastructure including subnets, Cloud NAT, Cloud Router, firewall rules, and optional regional load balancers.

Cloud NAT remains the default. Set `nat_mode = "nstance-managed"` to disable
Cloud NAT and use dedicated NAT VMs. Set `fixed_public_ipv4_count` when stable
IPv4 egress is required and pass the appropriate `public_addresses` output
directly into each tenant's NAT configuration. Switching modes preserves the
subnets; nstance-server owns the live tagged routes in Nstance-managed mode.

## Usage

```hcl
module "network" {
  source  = "nstance-dev/nstance/google//modules/network"
  version = "~> 1.0"

  cluster       = module.cluster
  vpc_cidr_ipv4 = "172.18.0.0/16"

  subnets = {
    "public" = {
      "us-central1-a" = [{ ipv4_cidr = "172.18.0.0/24", public = true, nat_gateway = true }]
    }
    "nstance" = {
      "us-central1-a" = [{ ipv4_cidr = "172.18.1.0/28", nat_subnet = "public" }]
    }
  }
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
