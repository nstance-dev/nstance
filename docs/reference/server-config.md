---
title: "Server Configuration"
weight: 50
description: "Detailed reference for all Nstance Server configuration options."
---

# Server Configuration

Below we'll provide an example/reference configuration file for Nstance Server, using example data for a fictional Kubernetes cluster:

```jsonc
{
  "server": {
    "cluster_id": "example-cluster",                   // Cluster ID (lowercase alphanumeric + hyphens)
    "provider": {
      "kind": "aws",
      "region": "us-west-2",
      "zone": "us-west-2a", // aka CAPI "Failure Domain"
    },
    "shard": "us-west-2a",
    "subnet_pools": {
      "control-plane": ["subnet-12345678"],           // Maps subnet pools to provider subnet IDs
      "ingress": ["subnet-23456789"],                 // Subnets for ingress/load balancer nodes
      "workers": ["subnet-87654321", "subnet-abcdef"] // Each key can map to multiple subnets for capacity
    },
    "dynamic_subnet_pools": ["workers"], // Optional: Restrict dynamic groups to these subnet pool IDs (empty = any allowed)
    "secrets": {
      "provider": "object-storage",
      "prefix": "",
      "encryption_key": {
        "provider": "aws-secrets-manager",
        "source": "arn:",
      },
    },
    "bind": {
      "health_addr": "0.0.0.0:8990",         // HTTP health endpoint for ASG health checks (returns 200 after successful boot)
      "election_addr": "0.0.0.0:8991",        // HTTPS leader election health endpoint for peer-to-peer leader election health checks
      "registration_addr": "0.0.0.0:8992",    // Client (Agent/Operator) registration (anonymous)
      "operator_addr": "0.0.0.0:8993",        // Operator connections (authenticated)
      "agent_addr": "0.0.0.0:8994"            // Agent connections (authenticated)
    },
    "advertise": {
      "health_addr": "10.0.0.1:8990",         // HTTP health endpoint (can be queried by ASGs or monitoring)
      "election_addr": "10.0.0.1:8991",        // HTTPS leader election health endpoint (used by s3lect for peer health checks)
      "registration_addr": "10.0.0.1:8992",    // Address used by Nstance Agents and Nstance Operator to register
      "operator_addr": "10.0.0.1:8993",        // Address used by Nstance Operator to connect
      "agent_addr": "10.0.0.1:8994"            // Address used by Nstance Agents to connect
      // Note: if host part is empty or "0.0.0.0"/"::", it is auto-detected from the primary network interface
    },
    "leader_network": {
      // Leader network configuration for shard leadership (provider-specific)
      // AWS: requires ip and interface_id (ENI ID)
      // GCP: leader_network presence enables alias IP assignment
      "ip": "10.0.0.100",                             // Stable leader IP address (ENI private IP for AWS, reserved IP for GCP)
      "interface_id": "eni-0abc123def456789"           // AWS ENI ID (required for AWS, not used for GCP)
    },
    "garbage_collection": {
      "interval": "2m",               // How often to backfill provider instances and look for dangling ones
      "registration_timeout": "5m"     // How long to wait for instance registration before terminating as dangling
    },
    "expiry": {
      "eligible_age": "21d",      // Optional: Age at which instances become eligible for opportunistic expiry
      "forced_age": "30d",        // Optional: Age at which to force expiry regardless of draining state
      "ondemand_age": "7d"        // Optional: Maximum age for on-demand instances before forced expiry
    },
    "image_refresh_interval": "6h", // Optional: How often to refresh image resolutions (default: 6h)
  },
  "images": {
    "debian_13_arm64": {
      "provider": "aws",
      "filters": [
        {"name": "name", "values": ["debian-13-arm64-*"]},
        {"name": "owner-id", "values": ["136693071363"]}  // Debian
      ],
      "sort": "creation-date",
      "order": "desc",
      "fallback": "ami-026fccd88446aa0bf" // Optional: Fallback AMI ID if lookup fails
    },
    "debian_13_amd64": {
      "provider": "aws",
      "filters": [
        {"name": "name", "values": ["debian-13-amd64-*"]},
        {"name": "owner-id", "values": ["136693071363"]}
      ],
      "sort": "creation-date",
      "order": "desc",
      "fallback": "ami-0abcd1234efgh5678"
    }
  },
  "certificates": {
    "kubelet.client": {
      "kind": "client",
      "cn": "system:node:{{ .Instance.ID }}",
      "organization": ["system:nodes"],
      "dns": [
        "localhost",
        "{{ .Instance.ID }}",
        "{{ .Instance.Hostname }}"
      ],
      "ip": [
        "127.0.0.1",
        "::1",
        "{{ .Instance.IP4 }}",
        "{{ .Instance.IP6 }}",
      ],
    },
    "kubelet.server": {
      "kind": "server",
      "cn": "kubelet",
      "dns": [
        "localhost",
        "{{ .Instance.ID }}",
        "{{ .Instance.Hostname }}"
      ],
      "ip": [
        "127.0.0.1",
        "::1",
        "{{ .Instance.IP4 }}",
        "{{ .Instance.IP6 }}",
      ],
    }
    "kube-apiserver.server": {
      "kind": "server",
      "cn": "kubernetes",
      "dns": [
        "localhost",
        "kubernetes",
        "kubernetes.default",
        "kubernetes.default.svc",
        "kubernetes.default.svc.cluster",
        "kubernetes.default.svc.cluster.local",
        "kubernetes.svc.cluster.local",
        "{{ .Instance.ID }}",
        "{{ .Instance.Hostname }}",
        "{{ .Cluster.FQDN }}"
      ],
      "ip": [
        "127.0.0.1",
        "::1",
        "198.18.0.1",
        "fdc6::1",
        "{{ .Instance.IP4 }}"
        "{{ .Instance.IP6 }}"
      ],
    },
  },
  "defaults": {
    "args": { // Passed to RunInstances. You cannot set InstanceType/SubnetId/Userdata/MinCount/MaxCount.
      "Ipv6AddressCount": 1,
      "BlockDeviceMappings": [
        {
          "DeviceName": "/dev/sda1", // root volume
          "Ebs": {
            "VolumeSize": 50, // in GB
            "VolumeType": "gp3",
            "Encrypted": true,
          },
        },
      ],
      "MetadataOptions": {
        "HttpEndpoint": "enabled", // default
        "HttpProtocolIpv6": "disabled", // default
        "HttpPutResponseHopLimit": 1, // default
        "HttpTokens": "required", // always require IMDSv2
        "InstanceMetadataTags": "enabled",
      },
      "PrivateDnsNameOptions": {
        "EnableResourceNameDnsARecord": true,
      },
      "TagSpecifications": [
        {
          "ResourceType": "instance",
          "Tags": [
            {
              "Key": "InstanceId",
              "Value": "{{ .Instance.ID }}",
            },
            {
              "Key": "InstanceKind",
              "Value": "{{ .Instance.Kind }}",
            },
          ],
        },
      ],
    },
    "vars": {
      "ClusterSlug": "example-cluster",
      "ClusterFQDN": "example-cluster.cluster.cool",
      "Environment": "production",
      "CloudBilingID": "123412341234",
      "SSHAuthorizedKey": "",
      "TelemetryBucket": "",
    },
  },
  "templates": {
    "nat": {
      "kind": "nat",
      "arch": "arm64",
      "args": {
        "ImageId": "ami-026fccd88446aa0bf", // debian for arm64
        "SubnetId": "{{ .Instance.Subnet.ID }}",
        "SecurityGroupIds": ["sg-12341234abcd1234ab"],
        "IamInstanceProfile": {
          "Arn": "arn:aws:iam::123412341234:role/example-instance-role",
        },
      },
      "userdata": {
        "templateString": "#!/bin/bash\necho \"hello world\"",
      },
      // defaults:
      "size": 1,
      "instance_type": "t4g.small",
      "vars": {}
    },
    "knc": {
      "kind": "knc", // 3 lowercase letters, used as prefix for instance ID (puidv7)
      "arch": "arm64", // used to validate instance type
      "files": {
        // send secret `tunnel-20250924.json` as `tunnel.json` file
        "tunnel.json": {
          "kind": "secret",
          "source": "tunnel-20250924.json",
        },
        // send a certificate generated using a public key sent from the agent
        "kubelet.client.crt": {
          "kind": "certificate",
          "template": "kubelet.client",
          "key": {
            "source": "agent",
            "name": "kubelet.client.pub",
          },
        },
        // send templated environment file
        "instance.env": {
          "kind": "env",
          "template": {
            "INSTANCE_ID": "{{ .Instance.ID }}",
            "ENVIRONMENT": "{{ .Vars.Environment }}",
            "K8S_NODE_LABELS": "{{ .Vars.KUBELET_NODE_LABELS }}",
            "CLUSTER_FQDN": "{{ .Cluster.FQDN }}"
          }
        },
        // send templated JSON configuration file
        "kubelet-config.json": {
          "kind": "json",
          "template": {
            "kind": "KubeletConfiguration",
            "apiVersion": "kubelet.config.k8s.io/v1beta1",
            "address": "{{ .Instance.IP4 }}",
            "port": 10250,
            "cgroupDriver": "systemd",
            "clusterDomain": "cluster.local",
            "nodeLabels": {
              "instance.example.com/id": "{{ .Instance.ID }}",
            },
        },
      },
      "args": { // Passed to RunInstances. You cannot set InstanceType/SubnetId/Userdata/MinCount/MaxCount.
        "ImageId": "{{ .Image.debian_13_arm64 }}", // resolved from images config
        "SecurityGroupIds": ["sg-23412341abcd1234ab"],
        "IamInstanceProfile": {
          "Arn": "arn:aws:iam::123412341234:role/example-instance-role",
        },
        // example of overriding a default arg.
        // note we use a merge strategy for objects
        // - for arrays, if the value is an empty object, it will not overwrite the base object
        // - if a value in an array is not an object, it will overwrite it
        "BlockDeviceMappings": [
          {
            "Ebs": {
              "VolumeSize": 100, // in GB
            },
          },
        ]
      },
      "userdata": {
        "templateUrl": "https://example.com/userdata/knc_arm64.sh",
      },
      // defaults:
      "size": 1,
      "instance_type": "t4g.medium",
      "vars": {
        "K8SAPIHostname": "example-cluster.cluster.cool",
        "OIDCIssuer": "https://auth.example.com",
        "RegistryBucket": "",
      },
    },
  },
  "groups": {
    "nat": {
      "template": "nat",
      "size": 1,
      "instance_type": null, // use template default
      "subnet_pool": "control-plane", // Subnet pool ID (resolved via server.subnet_pools)
    },
    "main": {
      "template": "knc",
      "size": 1,
      "subnet_pool": "control-plane",
      "vars": {
        "KUBELET_NODE_LABELS": "controlplane",
      }
    },
    "ingress": {
      "template": "knd",
      "subnet_pool": "ingress",
      "vars": {
        "KUBELET_NODE_LABELS": "traefik",
      }
    },
    "apps": {
      "template": "knd",
      "size": 2,
      "instance_type": "t4g.xlarge",
      "subnet_pool": "workers",
      "vars": {}
    },
  }
}
```

Nstance Server configuration file consists of:

* `server` block - defining options for the Nstance Server process, including:
  * `provider` - options such as cloud (kind) and region.
  * `cluster_id` - cluster identification (ID)
  * `shard`  - the unique server zone shard (must be lowercase alphanumeric with hyphens, no leading/trailing/consecutive hyphens, max 32 characters)
  * `subnet_pools` - maps subnet pools to provider subnet IDs
  * `dynamic_subnet_pools` - optional list of subnet pool IDs restricting which subnet pools dynamic groups can use (empty = any allowed)
  * `secrets` - config for retrieving and storing secrets including encryption key options
  * `bind` - addresses for the Nstance Server process to bind to
  * `advertise` - addresses for clients to connect to
  * `garbageCollection` - background provider sync/cleanup settings (`interval` cadence and `registrationTimeout` for dangling instance detection)
  * `expiry` - optional instance age limits for automatic rotation
* `images` block - defining (optional) automatic image resolution configuration
* `certificates` block - defining certificate templates for TLS certificate generation
* `files` block - defining files to send to agent
  * kind = `certificate` where `template` = certificate templates, and `key` can reference an instance public key (kind of like a CSR)
  * kind = `secret` where `source` = secret file
  * kind = `env` where files are dynamically generated as .env format from key-value template objects
  * kind = `json` where files are dynamically generated as JSON from template objects with templated string values
  * kind = `string` where files are dynamically generated from raw string templates for custom formats
* `defaults` block - defining global defaults for all templates/groups/instances.
* `templates` block - defining instance templates which can be referenced by groups and dynamic instances.
* `groups` block - defining static groups (for initial config), or static+dynamic groups (for live config).

For more information see: [Image Resolution](templates-and-vars.md#image-resolution), [Args & Vars Merge Strategy](templates-and-vars.md#args-and-vars-merge-strategy), [Args](templates-and-vars.md#args), [Vars](templates-and-vars.md#vars), and [Userdata Templates](templates-and-vars.md#userdata-templates).
