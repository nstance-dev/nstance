# Nstance <https://nstance.dev>
# Copyright 2026 Nadrama Pty Ltd
# SPDX-License-Identifier: Apache-2.0

data "aws_region" "current" {}
data "aws_caller_identity" "current" {}

locals {
  account_id  = data.aws_caller_identity.current.account_id
  region      = data.aws_region.current.id
  name_prefix = coalesce(var.name_prefix, var.cluster.name_prefix)
}

# ============================================================================
# EC2 Assume Role Policy (shared by server and agent)
# ============================================================================

data "aws_iam_policy_document" "ec2_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

# ============================================================================
# Nstance Server IAM Role
# ============================================================================

resource "aws_iam_role" "server" {
  name               = "${local.name_prefix}-server-role"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-server-role"
  })
}

resource "aws_iam_instance_profile" "server" {
  name = "${local.name_prefix}-server-profile"
  role = aws_iam_role.server.name
}

data "aws_iam_policy_document" "server" {
  # EC2 Instance Management
  statement {
    sid = "EC2InstanceManagement"
    actions = [
      "ec2:RunInstances",
      "ec2:TerminateInstances",
      "ec2:DescribeInstances",
      "ec2:DescribeNetworkInterfaces",
      "ec2:AttachNetworkInterface",
      "ec2:DetachNetworkInterface",
      "ec2:DescribeSubnets",
      "ec2:DescribeImages",
      "ec2:CreateTags"
    ]
    resources = ["*"]
  }

  # S3 Bucket Access
  statement {
    sid = "S3BucketAccess"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:ListBucket"
    ]
    resources = [
      var.cluster.bucket_arn,
      "${var.cluster.bucket_arn}/*"
    ]
  }

  # Secrets Manager Access
  statement {
    sid = "SecretsManagerAccess"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:UpdateSecret",
      "secretsmanager:CreateSecret",
      "secretsmanager:DeleteSecret"
    ]
    resources = [
      "arn:aws:secretsmanager:${local.region}:${local.account_id}:secret:nstance/*"
    ]
  }

  # ELB Access (for load balancer integration)
  statement {
    sid = "ELBAccess"
    actions = [
      "elasticloadbalancing:RegisterTargets",
      "elasticloadbalancing:DeregisterTargets",
      "elasticloadbalancing:DescribeTargetHealth"
    ]
    resources = ["*"]
  }

  # IAM PassRole for agent instances
  statement {
    sid     = "PassAgentRole"
    actions = ["iam:PassRole"]
    resources = [
      aws_iam_role.agent.arn
    ]
  }
}

resource "aws_iam_role_policy" "server" {
  name   = "${local.name_prefix}-server-policy"
  role   = aws_iam_role.server.id
  policy = data.aws_iam_policy_document.server.json
}

resource "aws_iam_role_policy_attachment" "server_ssm" {
  count = var.enable_ssm ? 1 : 0

  role       = aws_iam_role.server.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# ============================================================================
# Nstance Agent IAM Role
# ============================================================================

resource "aws_iam_role" "agent" {
  name               = "${local.name_prefix}-agent-role"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json

  tags = merge(var.tags, {
    Name = "${local.name_prefix}-agent-role"
  })
}

resource "aws_iam_instance_profile" "agent" {
  name = "${local.name_prefix}-agent-profile"
  role = aws_iam_role.agent.name
}

data "aws_iam_policy_document" "agent" {
  # Minimal permissions for agent - mainly just EC2 metadata access
  # Agents communicate with server via gRPC, not directly with AWS services

  # Allow reading EC2 instance metadata (for spot termination detection)
  statement {
    sid       = "DescribeSelf"
    actions   = ["ec2:DescribeInstances"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "ec2:ResourceTag/nstance:managed"
      values   = ["true"]
    }
  }
}

resource "aws_iam_role_policy" "agent" {
  name   = "${local.name_prefix}-agent-policy"
  role   = aws_iam_role.agent.id
  policy = data.aws_iam_policy_document.agent.json
}

resource "aws_iam_role_policy_attachment" "agent_ssm" {
  count = var.enable_ssm ? 1 : 0

  role       = aws_iam_role.agent.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}
