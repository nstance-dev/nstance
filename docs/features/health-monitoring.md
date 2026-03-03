---
title: "Health Monitoring"
weight: 30
description: "Agent health reports, unhealthy node detection, and automatic instance replacement."
---

# Health Monitoring

Agent health reports, unhealthy node detection, and automatic instance replacement.

Each Nstance Agent maintains a persistent gRPC stream to the Nstance Server in its zone shard, sending health reports periodically on this stream.

These reports enable Nstance Server to detect failed/unhealthy VM instances, spot instance termination notices, and schedule replacement instances to be created before deprovisioning the failed/unhealthy/terminating nodes.

The persistent stream connection also enables instant disconnect detection - when an agent crashes, is killed, or loses network connectivity, the server detects the stream closure within less than 30 seconds (via gRPC keepalive) and immediately initiates health verification.

## Agent Health Reports

**Connection Type**: Persistent client streaming (agent maintains long-lived connection)

**Frequency**: Default 60 seconds (configurable via `NSTANCE_METRICS_INTERVAL`)

Agents establish a persistent gRPC stream to the Nstance Server and send health reports periodically on this stream. The server monitors the stream for disconnections, enabling instant detection of agent failures.

Agents include a `config_hash` field containing the runtime config hash from `/opt/nstance-agent/identity/config.hash`. This enables the server to detect configuration drift and push updated files when needed. See [Push Updates & Instance Rotation](../reference/instance-lifecycle.md#push-updates--instance-rotation) for details.

**Payload Example**:

Note: the actual payloads will be sent via gRPC, not JSON.

```json
{
  "version": "1.0.0",
  "instance_id": "knc0000000001r010000000000000",
  "count": 1234,
  "timestamp": "2024-01-01T00:00:00Z",
  "started": "2024-01-01T00:00:00Z",
  "window_start": "2024-01-01T00:00:00Z",
  "window_end": "2024-01-01T00:15:00Z",
  "uptime": 262215,
  "one_minute": {
    "cpu_usage": 15.2,
    "memory_usage": 45.8,
    "network_bytes_sent": 1234567,
    "network_bytes_received": 7654321,
    "disk_used": 42.5
  },
  "five_minutes": {
    "cpu_usage": 12.1,
    "memory_usage": 44.2,
    "network_bytes_sent": 1234567,
    "network_bytes_received": 7654321,
    "disk_used": 42.5
  },
  "fifteen_minutes": {
    "cpu_usage": 10.8,
    "memory_usage": 43.1,
    "network_bytes_sent": 1234567,
    "network_bytes_received": 7654321,
    "disk_used": 42.5
  },
  "files": {
    "ca.crt": "2024-01-01T00:00:00Z",
    "kubelet.key": "",
    "failed.crt": "error: permission denied"
  },
  "config_hash": "sha256:abc123..."
}
```

## Health Report Processing

When health reports are received by the server:

1. **Store health record** in SQLite (operational data)
2. **Process missing files** asynchronously (certificates, secrets, templated files)
3. **Check for missing keys** and send key generation requests if needed
4. **Handle spot termination notices** if present (see [Spot Instances](spot-instances.md))
5. **Register with load balancer groups** if configured (on first successful health report)
6. **Reconcile pending/failed LB group registrations** for eventual consistency

## Unhealthy Node Detection

The Nstance Server is responsible for detecting and replacing unhealthy instances:

1. **gRPC Stream Disconnect**: Immediately triggers provider status check
   - Stream context cancellation detected in less than 30 seconds (via keepalive)
   - Distinguishes graceful shutdown from unexpected disconnect
   - If provider reports unhealthy → immediate replacement
2. **Single Missed Health Report**: Server checks provider API for instance status (via GC polling every 2 minutes)
   - If provider reports unhealthy → immediate replacement
   - If provider reports healthy → wait for 3 missed reports
3. **Three Missed Reports**: Instance marked as unhealthy and replaced
4. **Replacement Logic**: When an instance is unhealthy, the server always creates a replacement immediately (temporarily allowing `desired + 1` instances). If the provider reports the VM as non-running (stopping/stopped/deleting/deleted/failed) or not found, drain is skipped and the old instance is deleted immediately. If the provider reports the VM as running (3 missed reports case), drain coordination is used since the VM still has active workloads.

**Detection Timeline:**
- Agent crash/kill: 0-30 seconds (stream disconnect + keepalive)
- Network partition: 30 seconds (keepalive timeout)
- Graceful shutdown: Instant (stream closes gracefully, no false alarm)

## Network Resilience - Agent Health Stream Error Handling

If the health report stream fails:

- **Log the error** and reconnect with exponential backoff
- **Retry after 5 seconds** initially
- **Maintain persistent connection** to enable instant disconnect detection
- **Rationale**: Stream reconnection ensures both reliable reporting and fast failure detection
