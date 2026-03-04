# Nstance Network Module (GCP)

Creates VPC network infrastructure including subnets, Cloud NAT, Cloud Router, firewall rules, and optional regional load balancers.

## Usage

```hcl
module "network" {
  source  = "nstance-dev/nstance/gcp//modules/network"
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
