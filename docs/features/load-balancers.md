---
title: "Load Balancers"
weight: 60
description: "Automatic registration and deregistration of instances with cloud provider load balancers."
---

# Load Balancers

Nstance supports automatic registration/de-registration of instances with cloud provider load balancers.

## Configuration

Load balancers are defined in the `load_balancers` configuration section:

```jsonc
{
  "load_balancers": {
    "www": {
      "provider": "aws",
      "target_group_arns": [
        "arn:aws:elasticloadbalancing:region:account:targetgroup/www-80/123",
        "arn:aws:elasticloadbalancing:region:account:targetgroup/www-443/456"
      ]
    },
    "internal": {
      "provider": "gcp",
      "backend_service_name": "internal-backend",
      "instance_group_name": "internal-ig"
    }
  },
  "groups": {
    "web-servers": {
      "template": "web",
      "size": 3,
      "load_balancers": ["www"]
    },
    "api-servers": {
      "template": "api",
      "size": 2,
      "load_balancers": ["www", "internal"]
    }
  }
}
```

**Provider-Specific Fields:**
- **AWS**: `target_group_arns` (required) - Array of NLB target group ARNs. For multi-port NLBs, include one ARN per port/listener. The server registers instances with all specified target groups.
- **GCP**: `backend_service_name` (required), `instance_group_name` (required) - Backend service and unmanaged instance group names. Instances are added to the instance group once (GCP instance groups are not port-specific).

## Lifecycle Integration

**Registration Flow:**
1. Instance created via provider API
2. Agent registers and sends first health report to server
3. Server registers instance with all configured load balancer groups for its group
4. Status tracked in `lb_instances` table: `pending` → `registered` (or `failed`)
5. Failed registrations retried on subsequent health reports

**Deregistration Flow:**
1. Instance marked for deletion (unhealthy, expired, or scale-down)
2. **Deregister from load balancer groups** (first, per explicit ordering)
3. Drain coordination with Operator (if Kubernetes node)
4. Terminate instance via provider API (once drained, if Kubernetes node)

**Reconciliation & Cache Management:**
- **Automatic retries**: Pending or failed registrations are retried on every health report
- **Leader election validation**: On leader election, all LB groups are validated and cache warmed from provider APIs by querying each load balancer's current instance list
- **Cache table**: `lb_instances` is a reconciliation cache tracking registration state; it's synced from provider APIs during validation
- **Eventual consistency**: Ensures correct state even after Nstance Server leadership changes or crashes
