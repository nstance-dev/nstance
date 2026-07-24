---
title: "Load Balancers"
weight: 60
description: "Automatic registration and deregistration of instances with cloud provider load balancers."
---

# Load Balancers

Nstance supports automatic registration/de-registration of instances with cloud provider load balancers.

## Configuration

Load balancers are defined in the `load_balancers` configuration section. Each entry is a logical registration target with an AWS NLB, Google Cloud NLB, or provider-neutral tunnel shape.

```jsonc
{
  "load_balancers": {
    "www": {
      "provider": "aws",
      "target_groups": [{
        "arn": "arn:aws:elasticloadbalancing:region:account:targetgroup/www-443/456",
        "listener_port": 443,
        "target_port": 443,
        "proxy_port": 8443
      }]
    },
    "internal": {
      "provider": "google",
      "network_endpoint_groups": ["internal-us-central1-a"],
      "frontends": [{"ip": "34.10.20.30", "port": 443}]
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
- **AWS**: `target_groups` (required) - Entries contain `arn`, `listener_port`, `target_port`, and `proxy_port`. Current registration derives and registers the ARN list.
- **Google Cloud**: `network_endpoint_groups` and `frontends` (required) - NEG names plus forwarding-rule `ip` and `port` metadata.
- **Tunnel**: `listeners` (required) - Entries contain `target_port` and `proxy_port`; no infrastructure provider registration API is called.

For example, a Kubernetes deployment may define an `ingress` registration target for ports 80/443 and a `controlplane` registration target for port 6443. Those targets can sit behind separate provider load balancers or share one load balancer with different listeners; Nstance only needs the backend membership instance group handles. 

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
