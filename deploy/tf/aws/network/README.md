# Nstance Network Module (AWS)

Creates VPC infrastructure including subnets, NAT gateways, route tables, provider-aware VPC endpoints, and optional Network Load Balancers. The SSM API endpoint is enabled whenever Parameter Store is used, independently of Session Manager; messaging and Secrets Manager endpoints remain conditional.

## Usage

```hcl
module "network" {
  source  = "nstance-dev/nstance/aws//modules/network"
  version = "~> 1.0"

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
