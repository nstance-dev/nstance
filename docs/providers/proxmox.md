---
title: "Proxmox VE"
weight: 30
description: "How Nstance integrates with Proxmox VE for on-premise virtual machine lifecycle management."
---

# Proxmox Integration Guide for Nstance

This document describes how Nstance integrates with Proxmox VE for on-premise virtual machine lifecycle management.

## Overview

Nstance Server can use Proxmox VE as a provider for VM lifecycle management, enabling on-premise container management node orchestration such as for Kubernetes clusters.

## Concepts Mapping

| Nstance Concept | Proxmox Equivalent | Notes |
|-----------------|-------------------|-------|
| Region | n/a | Single cluster per provider instance |
| Zone | Proxmox Cluster | e.g. one VE cluster for us-east-1a, one for us-east-2a equivalents |
| SubnetID | Bridge + VLAN | e.g. `vmbr0` or `vmbr0.100` |
| InstanceType | Resource spec | `cpu:4,memory:8192,disk:50` |
| ProviderInstanceID | VMID | Proxmox numeric VM ID (unique at any point in time, but may be reused after deletion) |

## Bootstrap Scripts

Nstance provides a set of bootstrap shell scripts for bootstrapping a Proxmox VE cluster and all its nodes to run Nstance - see [deploy/proxmox](https://github.com/nstance-dev/nstance/tree/main/deploy/proxmox)

| Script | Purpose | Run where |
|--------|---------|-----------|
| `vm-template-setup.sh` | Create a VM template from a cloud image | Each PVE node |
| `seaweedfs-test-setup.sh` | Install single-node SeaweedFS (S3-compatible object storage) | One PVE node (dev/test only) |
| `dnsmasq-test-setup.sh` | Install dnsmasq DHCP server for a bridge interface | One PVE node per subnet (dev/test only) |
| `create-shard-config.sh` | Generate shard config (JSONC) and upload to object storage | Any machine with object storage access |
| `server-with-keepalived.sh` | Install nstance-server + keepalived as systemd services | Each PVE node |

All scripts support `--dry-run` and `--help`. See each script's header for full options.

## Requirements

### Proxmox Environment

- Proxmox VE 8+ cluster (7.3 introduced VM tags in GUI, required for instance tracking, 8 changed the boot format)
- API access enabled with dedicated API token
- Shared storage accessible from all nodes (for VM templates and cloud-init ISOs)
- DHCP server on target network bridge
- VM template with cloud-init support (qemu-guest-agent recommended for Proxmox management)

### Network Requirements

- Nstance Server must have network connectivity to the Proxmox API (port 8006)
- VMs must have network connectivity to the Nstance Server gRPC endpoint
- Bridge/VLAN configuration consistent across all cluster nodes

If using keepalived for VIP failover:
- VRRP (IP protocol 112) multicast must be permitted between nodes on the interface where the VIP lives. If the Proxmox firewall is enabled, add a rule to allow VRRP from the VIP subnet (e.g. `IN ACCEPT -source 10.0.0.0/24 -p vrrp` in `/etc/pve/firewall/cluster.fw`). Without this, nodes cannot see each other's VRRP advertisements and both will hold the VIP simultaneously (split-brain).

### Object Storage Backend

Nstance Server requires an object storage backend that supports `If-Match` headers for leader election. Supported providers include AWS S3, Google Cloud Storage, and S3-compatible services such as Ceph RGW and SeaweedFS.

#### Option 1: Public Cloud Object Storage (Recommended for Hybrid Public/Private Deployments)

For hybrid public/private cloud setups, use a managed object storage service from a public cloud provider (e.g. AWS S3 or Google Cloud Storage).

**AWS S3:**
```bash
nstance-server --storage s3 --bucket nstance --shard <shard> --id <id>

# Standard AWS SDK authentication
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
```

**Google Cloud Storage:**
```bash
nstance-server --storage gcs --bucket nstance --shard <shard> --id <id>

# Standard Google Cloud SDK authentication
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json
```

#### Option 2: SeaweedFS with etcd (Recommended for Private Cloud Deployments)

For fully private cloud deployments, run [SeaweedFS](https://github.com/seaweedfs/seaweedfs) with etcd as the metadata store:

**Why SeaweedFS:**
- **Apache 2.0 License** - Permissive licensing suitable for any deployment
- **PutObject Preconditions** - Supports `If-Match` headers required for Nstance leader election
- **Lightweight** - Simple deployment, low resource overhead
- **S3 API Compatible** - Works with standard AWS SDK

**Why etcd:**
SeaweedFS needs a metadata store. To ensure high availability and strong consistency, even in the event of partioning between Proxmox VE clusters, etcd is recommended for the metadata store.

**Production Deployment with etcd:**

```bash
# SeaweedFS with etcd for HA metadata
weed master -mdir=/data/master -peers=master1:9333,master2:9333,master3:9333
weed volume -mserver=master1:9333,master2:9333,master3:9333 -dir=/data/volume
weed filer -master=master1:9333,master2:9333,master3:9333
weed s3 -filer=localhost:8888 -port=8333
```

**Nstance Server Configuration:**

```bash
nstance-server --storage s3 --bucket nstance --shard <shard> --id <id>

# S3-compatible credentials
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=admin
export AWS_SECRET_ACCESS_KEY=admin
export AWS_ENDPOINT_URL=http://seaweedfs.example.com:8333
export AWS_S3_USE_PATH_STYLE=true
```

Use the `create-shard-config.sh` bootstrap script to generate the shard configuration file — see the [proxmox bootstrap scripts](https://github.com/nstance-dev/nstance/tree/main/deploy/proxmox) for details.

#### Option 3: Proxmox Ceph RGW (Alternative for Private Cloud Deployments)

If your Proxmox cluster already runs Ceph for storage, you can enable the RADOS Gateway (RGW) for S3 compatibility. This avoids deploying additional infrastructure but Ceph is known to be more complex to configure and manage.

#### Deployment Topology

Run one object storage deployment per region, accessible by all Nstance Server shards in that region.

### Authentication

Proxmox API connection is configured using environment variables:

```bash
export PROXMOX_API_URL='https://localhost:8006/api2/json' # defaults to this if not set
export PROXMOX_TOKEN_ID='nstance@pve!nstance-token'
export PROXMOX_TOKEN_SECRET='xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx'
```

Note: use single quotes on the `PROXMOX_TOKEN_ID` value to escape the exclamation mark.

The Nstance Server will fail to start if `PROXMOX_TOKEN_ID` or `PROXMOX_TOKEN_SECRET` is missing when using the `proxmox` provider. `PROXMOX_API_URL` defaults to `https://localhost:8006/api2/json` if not set, which works when nstance-server runs on the Proxmox node itself.

**Creating an API Token:**

1. In the Proxmox web UI, navigate to **Datacenter → Permissions → API Tokens → Add**
2. Select a user (e.g. `root@pam` or create a dedicated `nstance@pve` user)
3. Enter a Token ID (e.g. `nstance-token`)
4. Uncheck **Privilege Separation** to inherit the user's permissions
5. Click **Add** and copy the secret immediately (shown only once)

The `PROXMOX_TOKEN_ID` format is `user@realm!tokenid`, e.g. `root@pam!nstance-token`.

**Required Proxmox Permissions:**

The API token needs the following privileges. The simplest setup is
`PVEVMAdmin` + `PVEDatastoreAdmin` + `PVEAuditor` + `PVESDNUser` on `/`
(see bootstrap scripts for exact commands).

For reference, the complete list of individual privileges used:

| Privilege | Used for | Justification |
|-----------|----------|---------------|
| `Sys.Audit` | Query node resources for scheduling, cluster discovery, task status | `GET /cluster/resources`, `GET /cluster/status`, `GET /nodes/{node}/status`, `GET /nodes/{node}/tasks/{upid}/status` |
| `VM.Allocate` | Reserve next VMID, clone target allocation, delete VMs | `GET /cluster/nextid`, `POST .../clone` target, `DELETE /nodes/{node}/qemu/{vmid}` |
| `VM.Audit` | Read VM status/config, list VMs for template resolution | `GET /nodes/{node}/qemu`, `GET .../qemu/{vmid}/status/current`, `GET .../qemu/{vmid}/config` |
| `VM.Clone` | Clone VM templates | `POST /nodes/{node}/qemu/{vmid}/clone` |
| `VM.PowerMgmt` | Start and stop VMs | `POST .../status/start`, `POST .../status/stop` |
| `VM.Config.CPU` | Set CPU cores | `POST .../config` with `cores` |
| `VM.Config.Memory` | Set memory | `POST .../config` with `memory` |
| `VM.Config.Network` | Set network devices | `POST .../config` with `net0` |
| `VM.Config.Disk` | Set boot order, resize disks | `POST .../config` with `boot`, `PUT .../resize` |
| `VM.Config.CDROM` | Attach/unmount cloud-init ISO | `POST .../config` with `ide2` |
| `VM.Config.Options` | Set VM tags, description, start-on-boot | `POST .../config` with `tags`, `description`, `onboot` |
| `Datastore.Audit` | List and browse storage contents | `GET /nodes/{node}/storage`, `GET .../content/{volume}` |
| `Datastore.Allocate` | Delete cloud-init ISO volumes | `DELETE /nodes/{node}/storage/{storage}/content/{volume}` |
| `Datastore.AllocateSpace` | Allocate disk space during clone | `POST .../clone` target storage allocation |
| `Datastore.AllocateTemplate` | Upload cloud-init ISOs | `POST /nodes/{node}/storage/{storage}/upload` |
| `SDN.Use` | Access network bridges when cloning VMs | `POST .../clone` and `POST .../config` with `net0` |

## Provider Configuration

### ProviderConfig

| Field | Type | Description |
|-------|------|-------------|
| `kind` | string | Must be `"proxmox"` |
| `region` | string | Cluster name (for metadata only) |
| `zone` | string | Same as region |
| `insecure_tls` | bool | Skip TLS certificate verification (default: `false`, in `options`) |
| `cloud_init_iso_storage` | string | Storage for cloud-init ISOs (default: `"local"`, in `options`) |

API URL and credentials (`PROXMOX_API_URL`, `PROXMOX_TOKEN_ID`, and `PROXMOX_TOKEN_SECRET`) are read from environment variables — see [Authentication](#authentication).

### Instance Template Args

Provider-specific arguments in instance templates or defaults:

| Arg | Type | Description |
|-----|------|-------------|
| `StoragePool` | string | **Required.** Storage for VM disks (e.g. `"local-lvm"`) |
| `TemplateName` | string | Template name to look up per node (mutually exclusive with `TemplateVMID`) |
| `TemplateVMID` | int | Template VMID to clone from (mutually exclusive with `TemplateName`) |
| `Bridge` | string | Network bridge (e.g. `"vmbr0"`) |
| `VLANTag` | int | VLAN tag for network interface |
| `Cores` | int | Number of CPU cores |
| `Memory` | int | Memory in MB |
| `DiskSize` | string | Disk size (e.g. `"50G"`) |
| `StartOnBoot` | bool | Start VM on Proxmox host boot |
| `Pool` | string | Proxmox resource pool |

**Template Configuration:**

Use `TemplateVMID` when you have either a single node cluster or shared storage and a single template accessible from all nodes. Use `TemplateName` when each node has its own local template with the same name but different VMIDs (Nstance will look up the correct VMID on the target node at VM creation time).

Either `TemplateName` or `TemplateVMID` must be specified (but not both). These can be set in `defaults.args` for a shard-wide default, or overridden per-template or per-group.

**Example:**

```json
{
  "defaults": {
    "args": {
      "StoragePool": "local-lvm",
      "TemplateName": "debian-13-template",
      "Bridge": "vmbr0",
      "Cores": 2,
      "Memory": 2048
    }
  },
  "templates": {
    "database": {
      "args": {
        "TemplateName": "debian-12-template",
        "Cores": 4,
        "Memory": 8192,
        "DiskSize": "100G"
      }
    }
  }
}
```

## VM Lifecycle

### Instance Creation

When creating a VM, Nstance:

1. Selects a target node using TOPSIS scheduling algorithm
2. Clones the VM from the configured template (new VM name = instance ID)
3. Configures VM with requested resources (CPU, memory, disk)
4. Sets VM description with managed notes (metadata known as "annotations" such as group, kind, created timestamp)
5. Generates and attaches a cloud-init ISO with request userdata script
6. Applies "association" metadata tags (`nstance`, `<cluster-id>`, `<shard>`)
7. Starts the VM

### Instance Deletion

When deleting a VM, Nstance:

1. Stops the VM gracefully (if running)
2. Deletes the cloud-init ISO from storage
3. Deletes the VM

### Instance Status

Nstance queries VM status via the Proxmox API. Private IPs are populated at agent registration, not from the Proxmox provider.

**Status Mapping:**

| Proxmox Status | Nstance Status |
|----------------|----------------|
| `running` | Running |
| `stopped` | Stopped |
| `paused` | Suspended |
| Other | Unknown |

### Instance Listing

Nstance uses the `/cluster/resources?type=vm` API to enumerate all VMs cluster-wide efficiently. VMs are filtered by the `nstance` tag.

### Instance Metadata

Nstance uses a structured metadata model for Proxmox VMs:

**Identifier:**
- Instance ID is stored as the VM name (e.g. `tst06dx9xy919t3v9kzd2xdsyzb3g`) — this is the authoritative, globally unique identifier
- Provider instance ID is the Proxmox VMID — unique at any point in time but may be reused after a VM is deleted (Proxmox assigns VMIDs via `cluster.NextID()` which recycles freed IDs)

**Association Metadata (used for filtering and GC):**
- `nstance` - Ownership tag identifying nstance-managed VMs
- `<cluster-id>` - Cluster ID as a tag (e.g. `example-cluster`)
- `<shard>` - Shard ID as a tag (e.g. `dev-1`)

**Annotation Metadata (stored in VM notes, informational only):**
The VM description field contains a managed notes block:
```
# DO NOT EDIT BELOW - managed by nstance #
group: test
kind: tst
created: 2026-01-20T12:00:00Z
# DO NOT EDIT ABOVE - managed by nstance #
```

These annotations are never used by nstance for filtering or reconciliation - only for display purposes in the Proxmox UI/API to assist operators.

## Node Scheduling

The scheduler selects the optimal node for VM placement using **TOPSIS** (Technique for Order of Preference by Similarity to Ideal Solution), the same multi-criteria decision-making algorithm used by Proxmox VE's built-in HA scheduler.

TOPSIS ranks nodes by their geometric distance to an ideal solution (best possible values) and anti-ideal solution (worst possible values). The node closest to ideal and farthest from anti-ideal wins.

**Scheduling Criteria:**
- Free memory (60% weight)
- Free CPU (40% weight)

Uses `/cluster/resources?type=node` to get all node resource usage in a single API call.

## Cloud-Init Integration

Nstance uses cloud-init ISOs for VM configuration. The cloud-init ISO contains:
- User-data from the instance template
- Meta-data with instance ID and hostname

## Network and Load Balancer Operations

Leader network and load balancer operations are not currently implemented for the Proxmox provider:

- `AssignLeaderNetwork` - Not implemented (returns error)
- `ReleaseLeaderNetwork` - Not implemented (returns error)
- `CheckSubnetCapacity` - Always returns available
- `RegisterWithLB` - No-op
- `DeregisterFromLB` - No-op
- `ListLBInstances` - Returns empty

## Deployment Topology

### Recommended Architecture

Each Proxmox VE cluster requires exactly one Nstance Server shard (1 cluster = 1 shard):

```
┌─────────────────────────────────────────────────────────────────┐
│                     Proxmox Datacenter Manager                  │
│                        (Region equivalent)                      │
├─────────────────────┬─────────────────────┬─────────────────────┤
│   PVE Cluster A     │   PVE Cluster B     │   PVE Cluster C     │
│                     │                     │                     │
│  ┌─────┐ ┌─────┐   │  ┌─────┐ ┌─────┐   │  ┌─────┐ ┌─────┐    │
│  │node1│ │node2│   │  │node1│ │node2│   │  │node1│ │node2│    │
│  └─────┘ └─────┘   │  └─────┘ └─────┘   │  └─────┘ └─────┘    │
│                     │                     │                     │
│  ┌───────────────┐ │  ┌───────────────┐ │  ┌───────────────┐  │
│  │Nstance Server │ │  │Nstance Server │ │  │Nstance Server │  │
│  │  (HA CT)      │ │  │  (HA CT)      │ │  │  (HA CT)      │  │
│  └───────────────┘ │  └───────────────┘ │  └───────────────┘  │
└─────────────────────┴─────────────────────┴─────────────────────┘
                              │
                    SDN / EVPN Fabric
```

**Recommendation:** Run Nstance Server as a Proxmox HA container (LXC) within each cluster for automatic failover between nodes. Alternative, run multiple containers and rely on Nstance Server leader election to ensure only one is the primary/leader.

### Mapping to Cloud Concepts

| Cloud Concept | Proxmox Equivalent |
|--------------|-------------------|
| Region | Proxmox Datacenter Manager deployment |
| Availability Zone | Individual PVE cluster |
| Shard | One per PVE cluster |
| VPC | SDN Zone (EVPN or Simple) |
| Subnet | SDN VNet / VLAN |

### Multi-Cluster Networking

For spanning workloads across multiple Proxmox VE clusters:

1. **Deploy Proxmox Datacenter Manager** to manage multiple clusters as a unified environment
2. **Configure SDN with EVPN** for layer 2/3 connectivity between clusters:
   - Create an SDN Zone (EVPN type for cross-cluster)
   - Define VNets that span clusters
   - Assign VNet to bridge on each cluster's nodes
3. **Configure Nstance Server** with one shard per cluster, all pointing to the same object storage backend

**Example Shard Configuration:**

```jsonc
{
  "shards": [
    {
      "id": "dc1-cluster-a",
      "provider": {
        "kind": "proxmox",
        "region": "dc1",
        "zone": "cluster-a"
      }
    },
    {
      "id": "dc1-cluster-b", 
      "provider": {
        "kind": "proxmox",
        "region": "dc1",
        "zone": "cluster-b"
      }
    }
  ]
}
```

### Single Cluster Deployment

For simpler deployments with a single Proxmox VE cluster:

- One Nstance Server instance with one shard
- Zone scheduling distributes VMs across nodes within the cluster
- No Datacenter Manager or SDN required

## Limitations

### No Load Balancer Integration

Proxmox has no native load balancer. For Kubernetes ingress:
- Use MetalLB with BGP or L2 mode
- Deploy HAProxy/Traefik as a VM or on bare metal
- Use external load balancer appliance

If BGP is not an option for your deployment, you may wish to use VRRP via something like [keepalived](https://keepalived.readthedocs.io/en/latest/introduction.html) or use a load balancer like [gobetween](https://github.com/yyyar/gobetween), for sending requests to a healthy node running an ingress controller like Traefik using a consistent "virtual IP".

### No Spot/Preemptible Instances

Proxmox does not have a spot instance concept. The agent's spot termination monitoring is disabled for the `proxmox` provider.

### VM Templates

Proxmox "templates" are roughly equivalent to AWS AMIs - they're pre-built VM images that new instances are cloned from. Unlike AWS which has a marketplace of official AMIs, Proxmox requires you to create templates from cloud images.

| AWS | Proxmox |
|-----|---------|
| AMI (Amazon Machine Image) | VM Template |
| `RunInstances` with `ImageId` | `Clone` from template VMID |
| Official AMIs from AWS/Debian/etc. | Create from cloud images |

#### Template Requirements

The base VM template must have:
- cloud-init package installed and enabled
- qemu-guest-agent installed and running (recommended for Proxmox management, not required by Nstance)
- Network configured for DHCP
- SSH server enabled

#### Guide: Creating a Debian Template

Debian provides official cloud images with cloud-init pre-configured. This is the recommended base OS for Nstance cluster VMs.

##### Storage Options for Templates

There are two ways to specify a VM template in Nstance args: `TemplateVMID` or `TemplateName`.

If using `TemplateVMID`, note that VM templates in Proxmox VE multi-node clusters will either have a single VMID if you store them in shared storage, or different VMIDs if local to the node.

So if you don't have shared storage, you can instead use `TemplateName` and use a consistent name across each node, and Nstance will lookup the template VMID per-node when provisioning a new VM.

For example, create a template named `debian-13-template` on each node:
- Node A: VMID 9000, name `debian-13-template` (local-lvm)
- Node B: VMID 9001, name `debian-13-template` (local-lvm)
- Node C: VMID 9002, name `debian-13-template` (local-lvm)

Then configure in your defaults or template args: `"TemplateName": "debian-13-template"`

Nstance queries VMs on the scheduled node and finds the matching template by name. An error is returned if zero or more than one template matches.

For single-node deployments, either option works — but note that `TemplateVMID` will require one less API call to Proxmox VE.

##### Template Creation

The easiest way to create a template is with the provided bootstrap script. Shell/SSH into each Proxmox node (or just one, if using shared storage) and run:

```bash
# Download and run the bootstrap script
curl -fSL -o vm-template-setup.sh https://raw.githubusercontent.com/nstance-dev/nstance/main/deploy/proxmox/vm-template-setup.sh
chmod +x vm-template-setup.sh

# Create a template with defaults (Debian 13 Trixie, local-lvm storage)
./vm-template-setup.sh

# Or customise options
# ./vm-template-setup.sh --storage ceph-pool --bridge vmbr1

# Or preview what will happen without making changes by doing a dry-run
# ./vm-template-setup.sh --dry-run
```

The script is idempotent — it skips creation if a template with the same name already exists on the node, downloads the cloud image only if not already present, and automatically selects the next available VMID starting from 9000. Run `./vm-template-setup.sh --help` for all options.

**Note:** If bootstrapping multiple nodes simultaneously, two nodes may select the same VMID since Proxmox VMIDs are cluster-wide. The second node's `qm create` will fail — simply re-run the script on that node. To avoid this, either run the script on one node at a time, or pass `--min-vmid` with a different starting VMID per node (e.g. `--min-vmid 9000`, `--min-vmid 9100`).

Note: We don't resize the template disk. Nstance handles disk sizing via the `DiskSize` template argument when cloning VMs.

The template name (default `debian-13-template`) is then used in Nstance args as `TemplateName`.

**Verifying the template:**

```bash
# List templates
qm list | grep template

# Show template config (replace VMID with the one reported by the script)
qm config 9000
```

The template is now ready to be used.

**Note:** For other distributions (e.g. Ubuntu), use the `--image-url` flag to point to a different cloud image and `--template-name` for the name. Ensure the image includes cloud-init (qemu-guest-agent recommended).

##### Manual Template Creation (Advanced)

If you prefer to create the template manually, or need to customise steps beyond what the bootstrap script provides, follow these steps on each Proxmox node (or just one, if using shared storage):

{{< details title="Click to expand manual steps" >}}

**Step 1: Download the Debian cloud image**

```bash
# Download Debian 13 (Trixie) cloud image
cd /var/lib/vz/template/iso/
curl -fSLO https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2
```

**Step 2: Create a VM and import the disk**

Replace `local-lvm` below with your shared storage name if using shared storage.

VMID 9000 is commonly used for templates. Note that if you are not using shared storage, you must use a different VMID per node, such as 9001, 9002, and so on.

```bash
export VMID=9000

# Create a new VM
qm create $VMID --name debian-13-template --memory 2048 --cores 2 --net0 virtio,bridge=vmbr0 --scsihw virtio-scsi-pci

# Import the cloud image as the primary disk
qm set $VMID --scsi0 local-lvm:0,import-from=/var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2

# Add cloud-init drive
qm set $VMID --ide2 local-lvm:cloudinit

# Set boot order (Proxmox 8+ format)
qm set $VMID --boot order=scsi0

# Configure serial console (required by many cloud images)
qm set $VMID --serial0 socket --vga serial0

# Enable QEMU guest agent
qm set $VMID --agent enabled=1
```

Note: We don't resize the template disk here. Nstance handles disk sizing via the `DiskSize` template argument when cloning VMs.

**Step 3: Configure cloud-init defaults (optional)**

```bash
# Set default user (can be overridden via Nstance server config for userdata)
qm set $VMID --ciuser debian

# Set to use DHCP
qm set $VMID --ipconfig0 ip=dhcp
```

**Step 4: Convert to template**

```bash
# Convert the VM to a template
qm template $VMID
```

The template VMID (9000 in this example) is then used in Nstance args as `TemplateVMID`, OR template name ("debian-13-template" in this example) as `TemplateName`.

{{< /details >}}

## Troubleshooting

### Common Issues

1. **VM Creation Fails**: Check storage pool has sufficient space and is accessible from target node
2. **No IP Address**: Ensure network is configured and agent has registered with the server
3. **Cloud-Init Not Applied**: Verify cloud-init service is enabled in the template
4. **Scheduling Fails**: Check node status and resource availability
5. **VIP on multiple nodes (keepalived split-brain)**: VRRP multicast is blocked between nodes. Verify with `tcpdump -i <iface> vrrp` — you should see advertisements from the peer. Common causes:
   - Proxmox firewall blocking VRRP (protocol 112). Add `IN ACCEPT -source <vip-subnet> -p vrrp` to cluster firewall rules.
   - keepalived configured on the wrong interface (e.g. `vmbr0` instead of the VLAN interface where the VIP subnet lives). The `server-with-keepalived.sh` script auto-detects this from the VIP address.

### Proxmox VE Debugging Commands

```bash
# Check cluster status
pvesh get /cluster/status

# List all VMs
pvesh get /cluster/resources --type vm

# Check node resources
pvesh get /nodes/<node>/status

# Get VM status
pvesh get /nodes/<node>/qemu/<vmid>/status/current
```

## Further Reading

- [go-proxmox Documentation](https://github.com/luthermonson/go-proxmox) - API client reference
- [Proxmox VE API](https://pve.proxmox.com/pve-docs/api-viewer/) - Official API documentation
