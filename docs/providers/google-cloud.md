---
title: "Google Cloud"
weight: 20
description: "How Nstance integrates with Google Cloud services including Compute Engine, Cloud Storage, Secret Manager, and load balancing."
---

# Google Cloud Integration Guide for Nstance

This document provides a comprehensive overview of how Nstance integrates with Google Cloud services, including the specific APIs used, IAM permissions required, and operational considerations.

## Overview

Nstance Server leverages multiple Google Cloud services for infrastructure management, configuration storage, secrets management, and load balancing. The server uses the Google Cloud Go client libraries (`cloud.google.com/go`) and the older `google.golang.org/api` client for some operations.

## Google Cloud Services Used

### 1. Compute Engine

Nstance uses Compute Engine for virtual machine lifecycle management across multiple operations:

- **Instance Provisioning**: Creating new VM instances with custom configurations
- **Instance Termination**: Deleting unhealthy or expired instances
- **Health Monitoring**: Querying instance status for reconciliation decisions
- **Leader Network Management**: Assigning/removing alias IPs for shard leadership
- **Capacity Planning**: Checking subnet IP address availability
- **Load Balancing**: Managing instance group membership

**Key Features:**
- Support for all Compute Engine machine types
- Custom service accounts and network tags for firewall targeting
- Startup-script injection via instance metadata for agent initialization
- Label-based instance identification and filtering (see [Instance Labels](#instance-labels))
- Preemptible and Spot VM support with termination detection

### 2. Cloud Storage (GCS)

GCS serves as the object storage backend for all persistent state:

- **Configuration Storage**: Static and dynamic group configurations
- **Instance Metadata**: Registration records and certificates
- **Leader Election**: Distributed coordination using GCS lockfiles with generation-based optimistic locking
- **Certificate Logs**: Audit trail of certificate issuances
- (Optionally) **Secrets Storage**: Encrypted CA keys and custom secrets

See [Data Storage](../reference/data-storage.md) for the full bucket layout.

**GCS-Specific Behavior:**
- Uses object generation numbers as ETags for optimistic locking (instead of S3 ETags)
- Precondition checks via `GenerationMatch` and `DoesNotExist` conditions
- 412 Precondition Failed responses indicate concurrent modification

### 3. Secret Manager

Used as an optional secrets backend for secure storage of sensitive cryptographic material:

- **Encryption Key**: For encrypting data stored in object storage
- **Certificate Authority Keys**: Private keys for CA operations
- **Service Account Keys**: Kubernetes service account signing keys
- **Custom Secrets**: Distributed to instances via agent

**Security Model:**
- Secrets are automatically versioned — each write creates a new version
- The `latest` version alias is used for reads
- Secrets are encrypted at rest and in transit
- Secret names are prefixed (e.g., `nstance/`) for access scoping

**Configuration:** Set `secrets.provider` to `gcp-secret-manager` and provide the `project_id` in the secrets configuration. Alternatively, use `object-storage` with an encryption key to store secrets in GCS instead.

### 4. Instance Groups (Load Balancing)

Manages unmanaged instance groups for service exposure via load balancers:

- **Instance Registration**: Adding healthy instances to instance groups
- **Instance Deregistration**: Removing instances during termination/drain
- **Membership Listing**: Querying current group membership for cache warming

**Supported Load Balancers:**
- Network Load Balancers (TCP/UDP) via backend services with instance groups
- Internal Load Balancers via instance groups
- Automatic registration/deregistration based on instance lifecycle

## Google Cloud SDK API Usage

### Compute Engine APIs

| SDK Method | IAM Permission | Purpose |
|------------|---------------|---------|
| `instances.insert` | `compute.instances.create` | Create new VM instances |
| `instances.delete` | `compute.instances.delete` | Delete instances |
| `instances.get` | `compute.instances.get` | Query instance status/metadata |
| `instances.list` | `compute.instances.list` | List instances with label filters |
| `instances.updateNetworkInterface` | `compute.instances.updateNetworkInterface` | Assign/remove alias IPs for leader network |
| `subnetworks.get` | `compute.subnetworks.get` | Check subnet IP availability |
| `instanceGroups.addInstances` | `compute.instanceGroups.update` | Register instances with instance groups |
| `instanceGroups.removeInstances` | `compute.instanceGroups.update` | Deregister instances from instance groups |
| `instanceGroups.listInstances` | `compute.instanceGroups.list` | List instance group membership |

### Cloud Storage APIs

| SDK Method | IAM Permission | Purpose |
|------------|---------------|---------|
| `objects.get` | `storage.objects.get` | Retrieve stored data |
| `objects.create` | `storage.objects.create` | Store/update data |
| `objects.delete` | `storage.objects.delete` | Remove data |
| `objects.get` (attrs) | `storage.objects.get` | Check object existence/metadata |
| `objects.list` | `storage.objects.list` | Enumerate stored objects |

### Secret Manager APIs

| SDK Method | IAM Permission | Purpose |
|------------|---------------|---------|
| `secretVersions.access` | `secretmanager.versions.access` | Retrieve secret values |
| `secrets.addVersion` | `secretmanager.versions.add` | Add new secret versions |
| `secrets.create` | `secretmanager.secrets.create` | Create new secrets |
| `secrets.get` | `secretmanager.secrets.get` | Check if a secret exists |
| `secrets.delete` | `secretmanager.secrets.delete` | Remove secrets |
| `secrets.list` | `secretmanager.secrets.list` | List secrets by prefix |

## IAM Permissions

Nstance Server requires a dedicated service account with specific IAM roles. The recommended approach is to create a custom IAM role with the minimum required permissions.

### Compute Engine Permissions

```
compute.instances.create
compute.instances.delete
compute.instances.get
compute.instances.list
compute.instances.updateNetworkInterface
compute.subnetworks.get
compute.subnetworks.use
compute.instanceGroups.update
compute.instanceGroups.list
compute.disks.create
compute.zones.get
```

### Cloud Storage Permissions

```
storage.objects.get
storage.objects.create
storage.objects.delete
storage.objects.list
storage.buckets.get
```

### Secret Manager Permissions (if using `gcp-secret-manager`)

```
secretmanager.versions.access
secretmanager.versions.add
secretmanager.secrets.create
secretmanager.secrets.get
secretmanager.secrets.delete
secretmanager.secrets.list
```

### Key Considerations

- **Resource Scoping**: Storage permissions should be scoped to specific buckets using IAM conditions
- **Secret Prefixing**: Secret Manager access can be limited using IAM conditions on the `nstance/` resource name prefix
- **Project Scope**: All permissions should be scoped to the deployment project
- **Service Account**: VMs created by Nstance can use a separate service account (configured via the `ServiceAccount` arg) from the Nstance Server itself

## Instance Labels

GCP uses **labels** (not tags) for instance metadata and identification. Labels must be lowercase with hyphens only (no colons or underscores allowed). Nstance automatically manages the following labels on all instances:

| Label Key | Example Value | Purpose |
|-----------|---------------|---------|
| `nstance-managed` | `true` | Identifies Nstance-managed instances |
| `nstance-instance-id` | `knc0000000001r010000000000000` | Nstance instance identifier |
| `nstance-cluster-id` | `my-cluster` | Cluster identifier |
| `nstance-shard` | `us-central1-a` | Zone shard identifier |
| `nstance-group` | `workers` | Group name |
| `nstance-template` | `default` | Template name |
| `nstance-instance-kind` | `machinepool` | Instance kind |

Label values are automatically sanitized: converted to lowercase with underscores replaced by hyphens.

Additional custom labels can be added via the `Labels` arg in configuration.

> **Important:** GCP network tags are used only for firewall rule targeting and are not used for instance identification or filtering. Use the `NetworkTags` arg to apply firewall tags to instances.

## Agent Instance Metadata

Nstance Agent on GCP uses the [Instance Metadata Service](https://cloud.google.com/compute/docs/metadata/overview) (IMDS) at `http://metadata.google.internal/computeMetadata/v1/` to discover its own identity. All requests require the `Metadata-Flavor: Google` header.

- **`instance/name`** — Retrieve provider instance ID
- **`instance/scheduling/preemptible`** — Detect if running as a preemptible/Spot VM
- **`instance/preempted`** — Detect preemption termination notice

The agent also supports reading instance ID from the cloud-init cache as a fast path.

## Leader Network Management

On GCP, leader network assignment uses **alias IP ranges** instead of ENIs (as on AWS):

- **Assign**: Adds a `/32` alias IP range to the instance's primary network interface (`nic0`)
- **Release**: Removes the alias IP range from the network interface
- Both operations use `instances.updateNetworkInterface` with the NIC fingerprint for safe concurrent updates

This allows the shard leader to be reachable at a stable internal IP address regardless of which instance currently holds leadership.

## Troubleshooting

### Common Issues

1. **IAM Permission Errors**: Verify the service account has all required permissions listed above
2. **GCS Access Denied**: Check bucket-level IAM policies and ensure the service account has `storage.objects.*` permissions
3. **Instance Creation Failures**: Verify subnet capacity, quota limits, and that the specified machine type is available in the target zone
4. **Leader Network Issues**: Ensure the alias IP range is within the subnet's secondary range (if applicable)
5. **Secret Manager Errors**: Confirm the `project_id` is correct and the service account has Secret Manager permissions
6. **Instance Group Errors**: Verify the instance group exists in the correct zone and the instance is in the same network

### Debugging Commands

```bash
# Check VM instance status
gcloud compute instances describe INSTANCE_NAME --zone=ZONE

# List Nstance-managed instances
gcloud compute instances list --filter="labels.nstance-managed=true"

# List instances in a specific shard
gcloud compute instances list --filter="labels.nstance-shard=us-central1-a"

# List GCS objects
gcloud storage ls gs://your-bucket/ --recursive

# Check a specific object
gcloud storage cat gs://your-bucket/shard/us-central1-a/config.jsonc

# Access a secret
gcloud secrets versions access latest --secret=nstance-ca-key

# List secrets with prefix
gcloud secrets list --filter="name:nstance"

# List instance group members
gcloud compute instance-groups unmanaged list-instances GROUP_NAME --zone=ZONE

# Check subnet details
gcloud compute networks subnets describe SUBNET_NAME --region=REGION

# View instance serial port output (useful for debugging startup-script)
gcloud compute instances get-serial-port-output INSTANCE_NAME --zone=ZONE
```

## Further Reading

- [Google Cloud Go Client Libraries](https://cloud.google.com/go/docs/reference) — SDK reference
- [Compute Engine Documentation](https://cloud.google.com/compute/docs) — VM lifecycle and networking
- [Cloud Storage Documentation](https://cloud.google.com/storage/docs) — Object storage reference
- [Secret Manager Documentation](https://cloud.google.com/secret-manager/docs) — Secrets management
- [IAM Best Practices](https://cloud.google.com/iam/docs/using-iam-securely) — Security recommendations
