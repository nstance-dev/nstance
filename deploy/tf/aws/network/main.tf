# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

data "aws_region" "current" {}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  region      = data.aws_region.current.id
  name_prefix = coalesce(var.name_prefix, var.cluster.name_prefix)
  shards      = var.cluster.shards

  # Use existing VPC or create new one
  use_existing_vpc = var.vpc_id != ""
  vpc_id           = local.use_existing_vpc ? var.vpc_id : aws_vpc.main[0].id
  vpc_cidr_block   = local.use_existing_vpc ? var.vpc_cidr_ipv4 : aws_vpc.main[0].cidr_block
  vpc_ipv6_cidr    = local.use_existing_vpc ? null : (var.enable_ipv6 ? aws_vpc.main[0].ipv6_cidr_block : null)
}

# VPC with optional dual-stack (IPv4 + IPv6) - only when not using existing VPC
resource "aws_vpc" "main" {
  count = local.use_existing_vpc ? 0 : 1

  cidr_block                       = var.vpc_cidr_ipv4
  assign_generated_ipv6_cidr_block = var.enable_ipv6
  enable_dns_hostnames             = true
  enable_dns_support               = true

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-vpc"
  })
}

# Internet Gateway - only when creating new VPC
resource "aws_internet_gateway" "main" {
  count = local.use_existing_vpc ? 0 : 1

  vpc_id = local.vpc_id

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-igw"
  })
}

# Egress-only Internet Gateway for IPv6 private subnets - only when creating new VPC with IPv6
resource "aws_egress_only_internet_gateway" "main" {
  count = local.use_existing_vpc ? 0 : (var.enable_ipv6 ? 1 : 0)

  vpc_id = local.vpc_id

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-eigw"
  })
}

# Flatten subnets: role key -> zone -> list -> individual subnet definitions
# Key format: "role/zone/index"
locals {
  subnet_definitions = merge([
    for role, zones in var.subnets : merge([
      for zone, defs in zones : {
        for idx, def in defs : "${role}/${zone}/${idx}" => {
          role        = role
          zone        = zone
          index       = idx
          existing    = try(def.existing, null) != null
          id          = try(def.existing, null)
          ipv4_cidr   = try(def.ipv4_cidr, null)
          ipv6_netnum = try(def.ipv6_netnum, null)
          ipv6_cidr   = try(def.ipv6_cidr, null)
          public      = try(def.public, false)
          nat_gateway = try(def.nat_gateway, false)
          nat_subnet  = try(def.nat_subnet, null)
          shards      = try(def.shards, [])
        }
      }
    ]...)
  ]...)

  # Filter to only subnets that need creation (have ipv4_cidr, not existing)
  # Compute effective IPv6 CIDR from ipv6_netnum if set, otherwise use explicit ipv6_cidr
  subnets_to_create = {
    for k, v in local.subnet_definitions : k => merge(v, {
      ipv6_cidr = coalesce(
        v.ipv6_cidr,
        v.ipv6_netnum != null && local.vpc_ipv6_cidr != null ? cidrsubnet(local.vpc_ipv6_cidr, 8, v.ipv6_netnum) : null
      )
    }) if !v.existing
  }

  # Filter to existing subnets (have existing field)
  existing_subnets = { for k, v in local.subnet_definitions : k => v if v.existing }

  # Subnets with public = true
  public_subnets = { for k, v in local.subnet_definitions : k => v if v.public }

  # Subnets with nat_gateway = true (keyed by AZ for uniqueness)
  nat_gateway_subnets = { for k, v in local.subnet_definitions : k => v if v.nat_gateway }

  # Map of AZ -> logical subnet ID for NAT gateways (one per AZ)
  nat_gateway_by_az = {
    for k, v in local.nat_gateway_subnets : v.zone => k...
  }

  # For each role that has nat_gateway=true, map zone -> logical subnet ID
  nat_gateway_by_role_zone = {
    for k, v in local.nat_gateway_subnets : "${v.role}/${v.zone}" => k
  }

  # Subnets with nat_subnet set (need private route table association)
  nat_routed_subnets = { for k, v in local.subnet_definitions : k => v if v.nat_subnet != null }

  # Unique AZs that have NAT gateways
  nat_gateway_azs = distinct([for k, v in local.nat_gateway_subnets : v.zone])

  # Map of all subnet IDs: key -> subnet_id
  all_subnet_ids = merge(
    { for k, v in local.subnets_to_create : k => aws_subnet.managed[k].id },
    { for k, v in local.existing_subnets : k => v.id }
  )

  # Collect all unique shard IDs mentioned in any subnet definition
  all_shard_refs = distinct(flatten([
    for k, v in local.subnet_definitions : v.shards
  ]))

  # Validate shard references if cluster.shards is specified
  invalid_shards = length(local.shards) > 0 ? [
    for s in local.all_shard_refs : s if !contains(local.shards, s)
  ] : []

  # Build subnets output: role -> zone -> list of {id, shards, public}
  subnets_output = {
    for role, zones in var.subnets : role => {
      for zone, defs in zones : zone => [
        for idx, def in defs : {
          id     = local.all_subnet_ids["${role}/${zone}/${idx}"]
          shards = try(def.shards, [])
          public = try(def.public, false)
        }
      ]
    }
  }

  # Public subnet IDs by AZ (for NLB placement)
  public_subnet_ids_by_az = {
    for k, v in local.public_subnets : v.zone => local.all_subnet_ids[k]...
  }
  public_subnet_ids = { for az, ids in local.public_subnet_ids_by_az : az => ids[0] }

  # NAT gateway IDs by AZ
  nat_gateway_ids_by_az = {
    for az in local.nat_gateway_azs : az => aws_nat_gateway.per_az[az].id
  }

  # Private route table IDs by AZ
  private_route_table_ids_by_az = {
    for az in local.nat_gateway_azs : az => aws_route_table.private_per_az[az].id
  }

  # All route table IDs for S3 endpoint attachment
  all_route_table_ids = concat(
    local.use_existing_vpc ? [] : [aws_route_table.public[0].id],
    [for az in local.nat_gateway_azs : aws_route_table.private_per_az[az].id]
  )

  # First private subnet per AZ for VPC interface endpoints
  # Pick subnets that have nat_subnet set (i.e., private subnets with NAT routing)
  private_subnets_by_az = {
    for k, v in local.nat_routed_subnets : v.zone => k...
  }
  interface_endpoint_subnet_ids = [
    for az, keys in local.private_subnets_by_az : local.all_subnet_ids[keys[0]]
  ]
}

# Validation: ipv4_cidr and existing are mutually exclusive
resource "terraform_data" "validate_cidr_existing_exclusive" {
  for_each = local.subnet_definitions

  lifecycle {
    precondition {
      condition     = !(each.value.ipv4_cidr != null && each.value.existing)
      error_message = "Subnet ${each.key}: ipv4_cidr and existing are mutually exclusive. Specify one or the other."
    }
    precondition {
      condition     = each.value.ipv4_cidr != null || each.value.existing
      error_message = "Subnet ${each.key}: must specify either ipv4_cidr or existing."
    }
  }
}

# Validation: nat_gateway = true requires public = true
resource "terraform_data" "validate_nat_gateway_public" {
  for_each = local.nat_gateway_subnets

  lifecycle {
    precondition {
      condition     = each.value.public
      error_message = "Subnet ${each.key}: nat_gateway = true requires public = true."
    }
  }
}

# Validation: at most one nat_gateway per AZ
resource "terraform_data" "validate_one_nat_per_az" {
  for_each = local.nat_gateway_by_az

  lifecycle {
    precondition {
      condition     = length(each.value) <= 1
      error_message = "AZ ${each.key}: only one subnet can have nat_gateway = true. Found: ${jsonencode(each.value)}"
    }
  }
}

# Validation: nat_subnet must reference a role with nat_gateway in same AZ
resource "terraform_data" "validate_nat_subnet_ref" {
  for_each = local.nat_routed_subnets

  lifecycle {
    precondition {
      condition     = contains(keys(local.nat_gateway_by_role_zone), "${each.value.nat_subnet}/${each.value.zone}")
      error_message = "Subnet ${each.key}: nat_subnet = \"${each.value.nat_subnet}\" but no subnet in role \"${each.value.nat_subnet}\" has nat_gateway = true in AZ ${each.value.zone}."
    }
  }
}

resource "terraform_data" "validate_shards" {
  count = length(local.invalid_shards) > 0 ? 1 : 0

  lifecycle {
    precondition {
      condition     = length(local.invalid_shards) == 0
      error_message = "Invalid shard IDs in subnet shards filters: ${jsonencode(local.invalid_shards)}. Valid shards are: ${jsonencode(local.shards)}"
    }
  }
}

# Validation: enable_ipv6 requires ipv6_netnum or ipv6_cidr on managed subnets
resource "terraform_data" "validate_ipv6_cidrs" {
  for_each = var.enable_ipv6 && !local.use_existing_vpc ? local.subnets_to_create : {}

  lifecycle {
    precondition {
      condition     = each.value.ipv6_cidr != null
      error_message = "Subnet ${each.key}: enable_ipv6 is true but no ipv6_netnum or ipv6_cidr specified. Either set ipv6_netnum (0-255), ipv6_cidr, or set enable_ipv6 = false."
    }
  }
}

# Managed subnets (created from ipv4_cidr)
resource "aws_subnet" "managed" {
  for_each = local.subnets_to_create

  vpc_id                          = local.vpc_id
  cidr_block                      = each.value.ipv4_cidr
  ipv6_cidr_block                 = each.value.ipv6_cidr
  availability_zone               = each.value.zone
  map_public_ip_on_launch         = each.value.public
  assign_ipv6_address_on_creation = each.value.ipv6_cidr != null

  tags = merge(var.tags, {
    Name           = "${local.name_prefix}-${each.value.role}-${each.value.zone}-${each.value.index}"
    "nstance:role" = each.value.role
    "nstance:zone" = each.value.zone
  })
}

# Public route table (shared, routes to IGW) - only when creating new VPC
resource "aws_route_table" "public" {
  count = local.use_existing_vpc ? 0 : 1

  vpc_id = local.vpc_id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main[0].id
  }

  dynamic "route" {
    for_each = var.enable_ipv6 ? [1] : []
    content {
      ipv6_cidr_block = "::/0"
      gateway_id      = aws_internet_gateway.main[0].id
    }
  }

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-public-rt"
  })
}

# Elastic IP for NAT Gateway - one per AZ
resource "aws_eip" "per_az" {
  for_each = local.use_existing_vpc ? toset([]) : toset(local.nat_gateway_azs)

  domain = "vpc"

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-nat-eip-${each.key}"
  })

  depends_on = [aws_internet_gateway.main]
}

# NAT Gateway - one per AZ in the subnet with nat_gateway = true
resource "aws_nat_gateway" "per_az" {
  for_each = local.use_existing_vpc ? {} : {
    for az in local.nat_gateway_azs : az => local.nat_gateway_by_az[az][0]
  }

  allocation_id = aws_eip.per_az[each.key].id
  subnet_id     = local.all_subnet_ids[each.value]

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-nat-${each.key}"
  })

  depends_on = [aws_internet_gateway.main, aws_subnet.managed]
}

# Private route table - one per AZ with NAT gateway
resource "aws_route_table" "private_per_az" {
  for_each = local.use_existing_vpc ? toset([]) : toset(local.nat_gateway_azs)

  vpc_id = local.vpc_id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.per_az[each.key].id
  }

  dynamic "route" {
    for_each = var.enable_ipv6 ? [1] : []
    content {
      ipv6_cidr_block        = "::/0"
      egress_only_gateway_id = aws_egress_only_internet_gateway.main[0].id
    }
  }

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-private-rt-${each.key}"
  })
}

# Route table association for public subnets
resource "aws_route_table_association" "public" {
  for_each = local.use_existing_vpc ? {} : local.public_subnets

  subnet_id      = local.all_subnet_ids[each.key]
  route_table_id = aws_route_table.public[0].id

  depends_on = [aws_subnet.managed]
}

# Route table association for subnets with nat_subnet (private route via NAT in same AZ)
resource "aws_route_table_association" "private" {
  for_each = local.use_existing_vpc ? {} : local.nat_routed_subnets

  subnet_id      = local.all_subnet_ids[each.key]
  route_table_id = aws_route_table.private_per_az[each.value.zone].id

  depends_on = [aws_subnet.managed]
}

# VPC Endpoint for S3 (gateway endpoint - free) - attach to ALL route tables
resource "aws_vpc_endpoint" "s3" {
  count = local.use_existing_vpc ? 0 : (length(local.all_route_table_ids) > 0 ? 1 : 0)

  vpc_id            = local.vpc_id
  service_name      = "com.amazonaws.${local.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = local.all_route_table_ids

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-s3-endpoint"
  })
}

# Security group for VPC interface endpoints - only when creating new VPC
resource "aws_security_group" "vpc_endpoints" {
  count = local.use_existing_vpc ? 0 : 1

  name        = "${local.name_prefix}-vpc-endpoints-sg"
  description = "Security group for VPC endpoints"
  vpc_id      = local.vpc_id

  ingress {
    description = "HTTPS from VPC"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = [local.vpc_cidr_block]
  }

  dynamic "ingress" {
    for_each = var.enable_ipv6 && local.vpc_ipv6_cidr != null ? [1] : []
    content {
      description      = "HTTPS from VPC (IPv6)"
      from_port        = 443
      to_port          = 443
      protocol         = "tcp"
      ipv6_cidr_blocks = [local.vpc_ipv6_cidr]
    }
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  dynamic "egress" {
    for_each = var.enable_ipv6 ? [1] : []
    content {
      description      = "All outbound (IPv6)"
      from_port        = 0
      to_port          = 0
      protocol         = "-1"
      ipv6_cidr_blocks = ["::/0"]
    }
  }

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-vpc-endpoints-sg"
  })
}

# VPC Endpoint for Secrets Manager (interface endpoint) - place in private subnets, one per AZ
resource "aws_vpc_endpoint" "secretsmanager" {
  count = local.use_existing_vpc ? 0 : (length(local.interface_endpoint_subnet_ids) > 0 ? 1 : 0)

  vpc_id              = local.vpc_id
  service_name        = "com.amazonaws.${local.region}.secretsmanager"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = local.interface_endpoint_subnet_ids
  security_group_ids  = [aws_security_group.vpc_endpoints[0].id]
  private_dns_enabled = true

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-secretsmanager-endpoint"
  })

  depends_on = [aws_subnet.managed]
}

# VPC Endpoint for SSM - only when creating new VPC
resource "aws_vpc_endpoint" "ssm" {
  count = local.use_existing_vpc ? 0 : (var.enable_ssm && length(local.interface_endpoint_subnet_ids) > 0 ? 1 : 0)

  vpc_id              = local.vpc_id
  service_name        = "com.amazonaws.${local.region}.ssm"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = local.interface_endpoint_subnet_ids
  security_group_ids  = [aws_security_group.vpc_endpoints[0].id]
  private_dns_enabled = true

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-ssm-endpoint"
  })

  depends_on = [aws_subnet.managed]
}

# VPC Endpoint for SSM Messages - only when creating new VPC
resource "aws_vpc_endpoint" "ssmmessages" {
  count = local.use_existing_vpc ? 0 : (var.enable_ssm && length(local.interface_endpoint_subnet_ids) > 0 ? 1 : 0)

  vpc_id              = local.vpc_id
  service_name        = "com.amazonaws.${local.region}.ssmmessages"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = local.interface_endpoint_subnet_ids
  security_group_ids  = [aws_security_group.vpc_endpoints[0].id]
  private_dns_enabled = true

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-ssmmessages-endpoint"
  })

  depends_on = [aws_subnet.managed]
}

# VPC Endpoint for EC2 Messages - only when creating new VPC
resource "aws_vpc_endpoint" "ec2messages" {
  count = local.use_existing_vpc ? 0 : (var.enable_ssm && length(local.interface_endpoint_subnet_ids) > 0 ? 1 : 0)

  vpc_id              = local.vpc_id
  service_name        = "com.amazonaws.${local.region}.ec2messages"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = local.interface_endpoint_subnet_ids
  security_group_ids  = [aws_security_group.vpc_endpoints[0].id]
  private_dns_enabled = true

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-ec2messages-endpoint"
  })

  depends_on = [aws_subnet.managed]
}
