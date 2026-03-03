---
title: "Server API"
weight: 60
description: "gRPC API services exposed by the Nstance Server for agent and operator communication."
---

# Server API

The Nstance Server exposes three gRPC services, each on a separate bind address configured in the [server config](server-config.md). Access to the Agent and Operator services is restricted by the role encoded in the client certificate.

## Registration Service

**Authentication**: Anonymous (TLS only, no client certificate required)

The Registration service handles client registration for both agents and operators. Clients present a registration nonce JWT and their public key in exchange for a signed client certificate.

See [API Client Registration](instance-lifecycle.md#api-client-registration) for the full registration flow, nonce lifecycle, and security model.

## Agent Service

**Authentication**: mTLS with agent role certificate

The Agent service provides bidirectional communication between the server and agents running on managed instances.

### KeyGenRequests

A persistent server-to-agent stream where the server sends key generation requirements based on the instance template configuration. See [Certificate Generation Workflow](files-and-certificates.md#certificate-generation-workflow) for details.

### SubmitPublicKeys

Agents submit generated public keys to the server for storage and future certificate issuance. See [Public Key Submission and Storage](files-and-certificates.md#public-key-submission-and-storage) for details.

### ReceiveFiles

A persistent server-to-agent stream for delivering files including certificates, secrets, and templated environment files. See [Agent File Receiver](files-and-certificates.md#agent-file-receiver) for supported file types and validation.

### ReportHealth

A persistent client stream where agents send periodic health reports. The server uses these for health monitoring, certificate generation, configuration drift detection, and load balancer registration. See [Health Monitoring](../features/health-monitoring.md) for report format and processing, and [Runtime Config Drift Detection](instance-lifecycle.md#runtime-config-drift-detection) for how config hash comparison triggers file pushes.

## Operator Service

**Authentication**: mTLS with operator role certificate

The Operator service provides API endpoints for the Nstance Operator to manage groups, instances, and configuration.

### GetConfigStatus

Returns the current server configuration metadata:

- `etag` — Content hash of the configuration (e.g., `d41d8cd98f00b204e9800998ecf8427e`)
- `last_modified` — Timestamp of the last configuration change (e.g., `2024-01-01T12:00:00Z`)

### RefreshConfig

Triggers the server to download and apply the latest configuration from object storage:

1. **Download Latest**: Fetch the current `config/{shard}.jsonc` from object storage
2. **Compare ETags**: Check if the configuration has changed since last download
3. **Validate Configuration**: Validate the downloaded configuration before applying
4. **Update Local Copy**: Replace local configuration with validated version
5. **Return Response**: Respond to the Operator request
6. **Restart Server**: Restart to apply the configuration changes

**Responses:**
- **200**: Configuration successfully updated
- **304**: No changes detected (ETag match)
- **400**: Configuration validation failed
- **500**: Object storage download or processing error

The refresh operation includes configuration validation to prevent applying invalid configurations that could disrupt service operation.

### ListGroups

Returns all groups and their current state for the shard.

### UpsertGroup

Creates or updates a group. See [Dynamic Group Updates via Operator](instance-lifecycle.md#dynamic-group-updates-via-operator) for the server-side processing flow.

### DeleteGroup

Deletes a group. The reconciler gracefully scales down all managed instances. See [Group Deletion](../features/auto-scaling.md#group-deletion) for details.

### CreateInstance / DeleteInstance / GetInstanceStatus

Manage individual on-demand instances. See [On-Demand Nodes](../features/on-demand-nodes.md) for details.

### AcknowledgeDrained

Confirms that a Kubernetes node drain has completed, allowing the server to proceed with instance deletion. See [Instance Draining](instance-lifecycle.md#instance-draining) for the full drain coordination flow.

### WatchGroups

A persistent server-to-operator stream for real-time group change notifications.

- On connect, the server sends an initial snapshot of all current groups as `UPSERT` events
- The server pushes a `GroupEvent` whenever a group is created, updated, or deleted:
  - `UPSERT`: Group created or modified — includes full `GroupStatus`
  - `DELETE`: Group removed — includes only the group ID
- On stream disconnect, the operator triggers a full sync via `ListGroups()` to catch any missed events

### WatchInstances

A persistent server-to-operator stream for instance lifecycle events, primarily used for [drain coordination](instance-lifecycle.md#instance-draining). The server sends events when instances are marked for deletion (spot termination, expiry, unhealthy replacement) and when instances are deleted.

### WatchErrors

A persistent server-to-operator stream for error events such as provider errors and configuration validation failures, surfaced as Kubernetes events by the operator.
