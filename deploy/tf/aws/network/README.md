# Nstance Network Module (AWS)

Creates VPC infrastructure including subnets, NAT gateways, route tables,
provider-aware VPC endpoints, and optional Network Load Balancers. The free S3
gateway endpoint is created automatically. Set `enable_interface_endpoints = true`
to create billed PrivateLink endpoints for the configured AWS integrations;
otherwise, AWS API traffic uses normal public service endpoints.

Nstance NAT instances are the default. Set `nat_mode = "provider"` to use AWS
NAT Gateway, or `nat_mode = "none"` for native IPv6 egress without translation.
Use `ipv4_enabled` and `ipv6_enabled` to select IPv4-only, dual-stack, or
IPv6-only workload networking.
Every subnet with `nat_subnet` keeps its own route table when switching between
provider NAT and Nstance NAT instances, so the switch does not replace node subnets. Set
`fixed_public_ipv4_count` when stable IPv4 egress is required and pass the
appropriate `nat_public_addresses` output directly into each tenant's NAT
configuration. Replacements reassociate the same Elastic IP after the new VM is
healthy, so no spare public IPv4 address is required.

## Usage

```hcl
module "network" {
  source  = "nstance-dev/nstance/aws//modules/network"
  version = "~> 2.0"

  cluster       = module.cluster
  vpc_cidr_ipv4 = "172.18.0.0/16"

  subnets = {
    "public" = {
      "us-west-2a" = [{ ipv4_cidr = "172.18.0.0/24", public = true, nat_gateway = true }]
    }
    "nstance" = {
      "us-west-2a" = [{ ipv4_cidr = "172.18.1.0/28", nat_subnet = "public" }]
    }
  }
}
```

See the [full documentation](https://nstance.dev/docs/reference/opentofu-terraform/) for detailed usage, examples, and architecture.
