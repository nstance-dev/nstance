---
title: "Spot Instances"
weight: 40
description: "Automatic detection and handling of spot and preemptible instance termination notices."
---

# Spot Instances

Nstance Agent supports automatic detection and handling of spot instance termination notices across multiple cloud providers. The agent automatically detects the cloud provider and uses provider-specific metadata endpoints to detect termination notices, then normalizes the data into a common format for health reports.

## Provider Detection

The agent determines the cloud provider through:
1. **Environment Variable**: `NSTANCE_PROVIDER` can explicitly set the provider (`aws`, `google`, `proxmox`)
2. **Auto-Detection**: Falls back to checking provider-specific metadata endpoints

## Spot Instance Detection

On startup, the Nstance Agent automatically detects if it is running on a spot/preemptible instance by querying the detected cloud provider's metadata service.

**AWS Support:**
- Query metadata endpoint: `http://169.254.169.254/latest/meta-data/instance-life-cycle`
- If response is `spot`, enable spot termination monitoring
- Auto-detected by default when running on AWS

**Google Cloud Support:**
- Query metadata endpoint: `http://metadata.google.internal/computeMetadata/v1/instance/scheduling/preemptible`
- If response is `TRUE`, enable preemptible instance monitoring

## Spot Termination Monitoring

When running on a spot instance, the Agent starts a background monitor that polls for termination notices using provider-specific endpoints:

**Poll Configuration:**
- **Default Interval**: 2 seconds (configurable via `NSTANCE_SPOT_POLL_INTERVAL`)
- **Provider-Specific Endpoints:**
  - **AWS**: `http://169.254.169.254/latest/meta-data/spot/instance-action`
  - **Google Cloud**: `http://metadata.google.internal/computeMetadata/v1/instance/preempted`
  - **Proxmox VE**: n/a

**Detection Logic:**

**AWS:**
- 404 response = no termination notice (normal)
- 200 response = termination notice detected
- Other errors = logged but don't trigger termination handling

**Google Cloud:**
- Checks `preempted` metadata during graceful shutdown window
- Returns termination notice when `TRUE`, but no deadline provided

**Termination Notice Format (AWS):**
```json
{
  "action": "terminate",
  "time": "2024-01-01T12:00:00Z"
}
```

**Note**: Each provider has different metadata formats and termination notice mechanisms. The Agent normalizes these into a common `termination_notice` structure for health reports. The `deadline` field is optional - AWS provides a termination time (typically 2 minutes notice), but Google Cloud does not provide a specific deadline (for Google Cloud, we only know about preemption after termination has started).

## Health Report Integration

When a spot termination notice is detected:

1. **Parse Notice**: Extract termination time (if available) and action from metadata response
2. **Include in Health Reports**: Add `termination_notice` field to all subsequent health reports
3. **Continue Reporting**: Agent continues sending health reports until actual termination occurs

**Example Health Report with Termination Notice:**

```json
{
  "version": "1.0.0",
  "instance_id": "knc0000000001r010000000000000",
  "timestamp": "2024-01-01T11:58:00Z",
  "termination_notice": {
    "action": "terminate",
    "deadline": "2024-01-01T12:00:00Z"
  }
}
```

**Note**: The `deadline` field is optional - AWS provides a termination time (typically 2 minutes notice), but Google Cloud does not provide a specific deadline.

## Server-Side Termination Handling

When a health report contains a spot termination notice:

1. **Termination Notice Received**: Health report includes `termination_notice` field with action and optional deadline
2. **Mark as Terminating**: Instance marked as terminating in SQLite (similar to unhealthy instances)
3. **Schedule Replacement**: Immediately trigger reconciliation to create replacement instance
4. **Initiate Drain**: If group has `drain_timeout > 0`, follow standard drain coordination flow
5. **Cleanup**: After drain completion or timeout, instance is deleted (or allowed to terminate naturally by cloud provider)

**Termination Notice Structure:**
- `action`: Action type (e.g., "terminate", "stop", "hibernate")
- `deadline`: Optional timestamp when cloud provider will terminate the instance (AWS provides this with typically 2 minutes notice, Google Cloud does not)

**Processing Logic:**
- Server treats spot termination notices similar to unhealthy instance detection
- Replacement instances are created immediately upon first notice (don't wait for actual termination)
- Drain coordination reuses existing infrastructure (`drain_started_at`, `WatchInstanceEvents`, `AcknowledgeDrained`)
- Multiple health reports with the same termination notice are handled idempotently
- If leader changes during spot termination, new leader may re-notify Operator (handled idempotently)

## Spot Termination Workflow

**From Agent Perspective:**

1. **Normal Operation**: Agent runs normally, polling spot metadata every 2 seconds
2. **Notice Detected**: Metadata endpoint returns termination notice (typically 2 minutes before termination)
3. **Report to Server**: Include termination notice in health report
4. **Continue Operation**: Agent continues normal operation and health reporting
5. **Await Termination**: AWS terminates the instance at the specified time

**Server Coordination:**

The Server handles the termination notice by:
- Immediately scheduling a replacement instance
- Triggering drain coordination with the Operator (for Kubernetes nodes)
- Waiting for drain completion or timeout before allowing termination

## Error Handling

**Metadata Polling Errors:**
- Transient network errors are logged but don't affect normal operation
- Only a 200 response with valid termination data triggers termination handling
- Invalid JSON responses are logged as errors and ignored

**Multiple Detections:**
- Once a termination notice is detected, it persists in all subsequent health reports
- Server handles duplicate termination notices idempotently (similar to drain coordination)
