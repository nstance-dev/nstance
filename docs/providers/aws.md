---
title: "AWS"
weight: 10
description: "How Nstance integrates with AWS services including EC2, S3, Parameter Store, Secrets Manager, and Elastic Load Balancing."
---

# AWS Integration Guide for Nstance

This document provides a comprehensive overview of how Nstance integrates with AWS services, including the specific APIs used, IAM permissions required, and operational considerations.

## Overview

Nstance Server leverages multiple AWS services for infrastructure management, configuration storage, secrets management, and load balancing. The server uses AWS SDK v2 and requires specific IAM permissions to operate.

## AWS Services Used

### 1. Amazon EC2 (Elastic Compute Cloud)

Nstance uses EC2 for virtual machine lifecycle management across multiple operations:

- **Instance Provisioning**: Creating new EC2 instances with custom configurations
- **Instance Termination**: Gracefully shutting down unhealthy or expired instances
- **Health Monitoring**: Querying instance status for reconciliation decisions
- **Leader Network Management**: Attaching/detaching ENIs for shard leadership
- **Capacity Planning**: Checking subnet IP address availability
- **AMI Resolution**: Looking up latest AMIs for instance templates

**Key Features:**
- Support for all EC2 instance types (including ARM64)
- Custom IAM instance profiles and security groups
- Userdata injection for agent initialization
- Tag-based instance identification and management

### 2. Amazon S3 (Object Storage)

S3 serves as the object storage backend for all persistent state:

- **Configuration Storage**: Static and dynamic group configurations
- **Instance Metadata**: Registration records and certificates
- **Leader Election**: Distributed coordination using S3 lockfiles
- **Certificate Logs**: Audit trail of certificate issuances
- (Optionally) **Secrets Storage**: Encrypted CA keys and custom secrets

See [Data Storage](../reference/data-storage.md) for the full bucket layout.

### 3. AWS Systems Manager Parameter Store

Parameter Store is the default AWS secrets backend. Its server default is `/nstance/`; Terraform derives `/<name_prefix>/` unless `secrets_prefix` is set. It can also supply the 32-byte encryption key when `object-storage` is selected explicitly.

Nstance values remain `[]byte` at its boundaries, but Parameter Store writes them as raw `SecureString` text without a base64 envelope. Writes must therefore be valid UTF-8 and fit the standard-tier 4 KiB value limit.

AWS Systems Manager Parameter Store is commonly called simply Parameter Store. This integration uses Parameter Store, not Session Manager. Session Manager is a separate AWS Systems Manager capability and requires additional messaging endpoints.

### 4. AWS Secrets Manager

AWS Secrets Manager remains an explicit alternative, either as the direct `aws-secrets-manager` store or as the encryption-key source for `object-storage`. Object storage itself is also an explicit alternative for CA keys, service account keys, and custom secrets.

**Security Model:**
- Encryption Key reduces required permissions (read-only access to Secrets Manager)
- Secrets are encrypted at rest and in transit
- Supports key rotation workflows

### 5. Elastic Load Balancing v2 (ELBv2)

Manages Network Load Balancer target groups for service exposure:

- **Target Registration**: Adding healthy instances to NLB target groups
- **Target Deregistration**: Removing instances during termination/drain
- **Health Monitoring**: Querying target health status

**Supported Load Balancers:**
- Network Load Balancers (NLB) for high-performance TCP/UDP load balancing
- Automatic registration/deregistration based on instance lifecycle

## AWS SDK API Usage

### EC2 APIs

| SDK Method | IAM Action | Purpose |
|------------|------------|---------|
| `RunInstances` | `ec2:RunInstances` | Create new EC2 instances |
| `TerminateInstances` | `ec2:TerminateInstances` | Terminate instances |
| `DescribeInstances` | `ec2:DescribeInstances` | Query instance status/metadata |
| `DescribeNetworkInterfaces` | `ec2:DescribeNetworkInterfaces` | ENI status for leader network operations |
| `AttachNetworkInterface` | `ec2:AttachNetworkInterface` | Assign leader network (attach ENI to new leader) |
| `DetachNetworkInterface` | `ec2:DetachNetworkInterface` | Release leader network (detach ENI from old leader) |
| `DescribeSubnets` | `ec2:DescribeSubnets` | Check subnet IP availability |
| `DescribeImages` | `ec2:DescribeImages` | Resolve AMI IDs from filters |

### S3 APIs

| SDK Method | IAM Action | Purpose |
|------------|------------|---------|
| `GetObject` | `s3:GetObject` | Retrieve stored data |
| `PutObject` | `s3:PutObject` | Store/update data |
| `DeleteObject` | `s3:DeleteObject` | Remove data |
| `HeadObject` | `s3:GetObject` | Check object existence/metadata |
| `ListObjectsV2` | `s3:ListBucket` | Enumerate stored objects |

### Secrets Manager APIs

| SDK Method | IAM Action | Purpose |
|------------|------------|---------|
| `GetSecretValue` | `secretsmanager:GetSecretValue` | Retrieve secret values |
| `UpdateSecret` | `secretsmanager:UpdateSecret` | Update existing secrets |
| `CreateSecret` | `secretsmanager:CreateSecret` | Create new secrets |
| `DeleteSecret` | `secretsmanager:DeleteSecret` | Remove secrets |

### Systems Manager Parameter Store APIs

| SDK Method | IAM Action | Purpose |
|------------|------------|---------|
| `GetParameter` (with decryption) | `ssm:GetParameter` | Retrieve and decrypt a `SecureString` |
| `PutParameter` | `ssm:PutParameter` | Create or overwrite a `SecureString` |
| `DeleteParameter` | `ssm:DeleteParameter` | Remove a parameter |

### ELBv2 APIs

| SDK Method | IAM Action | Purpose |
|------------|------------|---------|
| `RegisterTargets` | `elasticloadbalancing:RegisterTargets` | Add instances to target groups |
| `DeregisterTargets` | `elasticloadbalancing:DeregisterTargets` | Remove instances from target groups |
| `DescribeTargetHealth` | `elasticloadbalancing:DescribeTargetHealth` | Query target health status |

## IAM Permissions

Nstance Server requires a dedicated IAM role with specific permissions. The complete policy is available in [`server-aws-iam.json`](https://github.com/nstance-dev/nstance/blob/main/examples/server-aws-iam.json).

### Key Considerations

- **Resource Scoping**: S3 permissions are scoped to specific buckets
- **Secret Prefixing**: Parameter Store and direct Secrets Manager access are limited to the effective `secrets_prefix`
- **Regional Scope**: All permissions should be scoped to the deployment region
- **Tag-Based Access**: Consider adding tag-based conditions for EC2 resources

## Troubleshooting

### Common Issues

1. **IAM Permission Errors**: Verify the IAM policy matches `examples/server-aws-iam.json`
2. **S3 Access Denied**: Check bucket permissions and KMS encryption settings
3. **EC2 Launch Failures**: Verify subnet capacity and security group rules
4. **Leader Network Issues**: Ensure ENI exists and is in correct VPC/subnet

### Debugging Commands

```bash
# Check EC2 instance status
aws ec2 describe-instances --instance-ids i-1234567890abcdef0

# List S3 objects
aws s3 ls s3://your-bucket/ --recursive

# Check secrets access
aws secretsmanager list-secrets --include-planned-deletion

# Verify target group health
aws elbv2 describe-target-health --target-group-arn arn:aws:elasticloadbalancing:...
```

## Further Reading

- [AWS SDK v2 Documentation](https://aws.github.io/aws-sdk-go-v2/docs/) - SDK reference
- [IAM Best Practices](https://docs.aws.amazon.com/IAM/latest/UserGuide/best-practices.html)
