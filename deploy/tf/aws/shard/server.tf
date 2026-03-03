# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

# Server Leader ENI (Stable IP for shard leader)
resource "aws_network_interface" "server_leader" {
  subnet_id       = local.server_subnet_id
  security_groups = [aws_security_group.server.id]

  description = "Stable ENI for Nstance Server shard leader (${var.shard})"

  tags = merge(local.common_tags, {
    Name                = "${local.name_prefix}-server-leader-eni-${var.shard}"
    "nstance:component" = "server-leader-eni"
  })

  depends_on = [terraform_data.validate_server_subnet]
}

# Server AMI (Debian)
data "aws_ami" "debian_arm64" {
  most_recent = true
  owners      = ["136693071363"] # Debian

  filter {
    name   = "name"
    values = ["debian-13-arm64-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  filter {
    name   = "architecture"
    values = ["arm64"]
  }
}

data "aws_ami" "debian_amd64" {
  most_recent = true
  owners      = ["136693071363"] # Debian

  filter {
    name   = "name"
    values = ["debian-13-amd64-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  filter {
    name   = "architecture"
    values = ["x86_64"]
  }
}

locals {
  server_ami_id = local.server_arch == "arm64" ? data.aws_ami.debian_arm64.id : data.aws_ami.debian_amd64.id
}

# Server Launch Template
resource "aws_launch_template" "server" {
  name_prefix   = "${local.name_prefix}-server-${var.shard}-"
  image_id      = local.server_ami_id
  instance_type = var.server_instance_type

  iam_instance_profile {
    arn = var.account.server_instance_profile_arn
  }

  network_interfaces {
    associate_public_ip_address = false
    security_groups             = [aws_security_group.server.id]
    ipv6_address_count          = var.network.enable_ipv6 ? 1 : 0
  }

  key_name = var.ssh_key_name != "" ? var.ssh_key_name : null

  user_data = base64encode(local.server_userdata)

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
    instance_metadata_tags      = "enabled"
  }

  block_device_mappings {
    device_name = "/dev/sda1"
    ebs {
      volume_size           = 20
      volume_type           = "gp3"
      delete_on_termination = true
      encrypted             = true
    }
  }

  tag_specifications {
    resource_type = "instance"
    tags = merge(local.common_tags, {
      Name                = "${local.name_prefix}-server-${var.shard}"
      "nstance:component" = "server"
    })
  }

  tag_specifications {
    resource_type = "volume"
    tags = merge(local.common_tags, {
      Name = "${local.name_prefix}-server-volume-${var.shard}"
    })
  }

  tags = merge(local.common_tags, {
    Name = "${local.name_prefix}-server-lt-${var.shard}"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# Server Auto-Scaling Group
resource "aws_autoscaling_group" "server" {
  name                = "${local.name_prefix}-server-asg-${var.shard}"
  vpc_zone_identifier = [local.server_subnet_id]
  desired_capacity    = var.server_count
  min_size            = 1
  max_size            = var.server_count + 1 # +1 allows rolling updates

  launch_template {
    id      = aws_launch_template.server.id
    version = "$Latest"
  }

  health_check_type         = "EC2"
  health_check_grace_period = 300

  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 50
    }
  }

  tag {
    key                 = "Name"
    value               = "${local.name_prefix}-server-${var.shard}"
    propagate_at_launch = true
  }

  tag {
    key                 = "nstance:shard"
    value               = var.shard
    propagate_at_launch = true
  }

  tag {
    key                 = "nstance:component"
    value               = "server"
    propagate_at_launch = true
  }

  lifecycle {
    create_before_destroy = true
  }

  depends_on = [
    aws_s3_object.shard_config
  ]
}
