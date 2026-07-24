---
title: "Getting Started"
weight: 10
description: "Get started with Nstance quickly."
---

# Getting Started with Nstance

Nstance can be deployed on [AWS](#aws), [Google Cloud](#google-cloud), or [Proxmox](#proxmox) - either as a single cloud deployment, or in complex multi-cloud and hybrid-cloud configurations.

To get started quickly, this page only covers the common single-cloud deployment approach.

Note that we use the [OpenTofu](https://opentofu.org/) `tofu` command below, but you can easily substitute that for `terraform` if required.

## AWS

Create a new Nstance cluster in AWS using OpenTofu:

```bash
export AWS_PROFILE=
export AWS_REGION=us-west-2

curl -O https://raw.githubusercontent.com/nstance-dev/terraform-aws-nstance/refs/heads/main/examples/single-shard/main.tf

tofu init

tofu apply -var="profile=${AWS_PROFILE}" -var="region=${AWS_REGION}" -var="cluster_id=test"
```

Destroy:

```bash
export AWS_PROFILE=
export AWS_REGION=us-west-2

# 1. Destroy the nstance-server ASG to stop it from managing instances
tofu destroy -var="profile=${AWS_PROFILE}" -var="region=${AWS_REGION}" -var="cluster_id=test" -target=module.shard.aws_autoscaling_group.server

# 2. Terminate all Nstance-managed instances to avoid orphaned resources or subnet deletion failures
INSTANCE_IDS=$(aws --profile="${AWS_PROFILE}" --region="${AWS_REGION}" ec2 describe-instances \
  --filters "Name=tag:nstance:managed,Values=true" "Name=tag:nstance:cluster-id,Values=test" "Name=instance-state-name,Values=running,stopped,pending" \
  --query 'Reservations[].Instances[].InstanceId' --output text)
if [ -n "$INSTANCE_IDS" ]; then
  aws --profile="${AWS_PROFILE}" --region="${AWS_REGION}" ec2 terminate-instances --instance-ids $INSTANCE_IDS
  aws --profile="${AWS_PROFILE}" --region="${AWS_REGION}" ec2 wait instance-terminated --instance-ids $INSTANCE_IDS
fi

# 3. Force-delete the S3 bucket holding nstance cluster state
export BUCKET_NAME=$(tofu state show 'module.cluster.aws_s3_bucket.nstance[0]' | awk -F'"' '/^[[:space:]]*bucket[[:space:]]*=/ { print $2 }')
aws --profile="${AWS_PROFILE}" s3 rb "s3://${BUCKET_NAME}" --force
tofu state rm 'module.cluster.aws_s3_bucket.nstance[0]'

# 4. Destroy all remaining Nstance cluster resources
tofu destroy -var="profile=${AWS_PROFILE}" -var="region=${AWS_REGION}" -var="cluster_id=test"
```

See the [OpenTofu/Terraform reference](../reference/opentofu-terraform.md) for full module documentation and advanced configurations.

## Google Cloud

Create a new Nstance cluster in Google Cloud using OpenTofu:

```bash
export GOOGLE_PROJECT=
export GOOGLE_REGION=us-central1
gcloud auth application-default login # or set GOOGLE_APPLICATION_CREDENTIALS

curl -O https://raw.githubusercontent.com/nstance-dev/terraform-google-nstance/refs/heads/main/examples/single-shard/main.tf

tofu init

tofu apply -var="project=${GOOGLE_PROJECT}" -var="region=${GOOGLE_REGION}" -var="cluster_id=test"
```

Destroy:
```bash
export GOOGLE_PROJECT=
export GOOGLE_REGION=us-central1
gcloud auth application-default login # or set GOOGLE_APPLICATION_CREDENTIALS

# 1. Destroy the nstance-server instance group manager to stop it from managing instances
tofu destroy -var="project=${GOOGLE_PROJECT}" -var="region=${GOOGLE_REGION}" -var="cluster_id=test" -target=module.shard.google_compute_instance_group_manager.server

# 2. Delete all Nstance-managed instances to avoid orphaned resources or subnet deletion failures
gcloud compute instances list \
  --project="${GOOGLE_PROJECT}" \
  --filter="labels.nstance-managed=true AND labels.nstance-cluster-id=test" \
  --format="value(name,zone)" | while read NAME ZONE; do
    gcloud compute instances delete "$NAME" --zone="$ZONE" --project="${GOOGLE_PROJECT}" --quiet
  done

# 3. Force-delete the GCS bucket holding nstance cluster state
export BUCKET_NAME=$(tofu state show 'module.cluster.google_storage_bucket.nstance[0]' | awk -F'"' '/^[[:space:]]*name[[:space:]]*=/ { print $2 }')
gcloud storage rm -r "gs://${BUCKET_NAME}"
tofu state rm 'module.cluster.google_storage_bucket.nstance[0]'

# 4. Destroy all remaining Nstance cluster resources
tofu destroy -var="project=${GOOGLE_PROJECT}" -var="region=${GOOGLE_REGION}" -var="cluster_id=test"
```

See the [OpenTofu/Terraform reference](../reference/opentofu-terraform.md) for full module documentation and advanced configurations.

## Proxmox

The easiest way to deploy Nstance on a Proxmox VE cluster is to use the provided [proxmox bootstrap scripts](https://github.com/nstance-dev/nstance/tree/main/deploy/proxmox). You can run them on your Proxmox nodes via the Proxmox Shell or SSH. There's a set of commands to run once per cluster, and then a set of commands to run once per node.

### Proxmox VE & Object Storage

To deploy Nstance on a Proxmox VE cluster, you will need to have an object storage solution, e.g:

1. Use a public cloud offering such as S3 or GCS

2. Use the built-in Proxmox Ceph with RGW for S3-compatibility

3. Run SeaweedFS on your proxmox cluster or adjacent servers

The instructions below demonstrate how to deploy a single SeaweedFS process for dev/testing setups, to allow you to get started quickly. For production setups, you'll want to either deploy SeaweedFS in a HA configuration, or consider options 1 or 2 above. See the Nstance Proxmox docs for full integration details and requirements.

### Run Once Per Proxmox VE Cluster

1. **Set up object storage** — use an existing S3-compatible backend, or for dev/test you can create a single-node SeaweedFS service with:
   ```bash
   ./seaweedfs-test-setup.sh --bucket nstance
   ```

2. **Create a Proxmox API token** (save the token secret from the output):
   ```bash
   pveum user add nstance@pve
   pveum aclmod / -user nstance@pve -role PVEVMAdmin,PVEDatastoreAdmin,PVEAuditor,PVESDNUser
   pveum user token add nstance@pve nstance-token --privsep 0
   export PROXMOX_TOKEN_SECRET='<token-secret>'
   export PROXMOX_API_URL='https://localhost:8006/api2/json' # optional, this is the default
   export PROXMOX_TOKEN_ID='nstance@pve!nstance-token'
   ```

3. **Export credentials** used by the remaining steps (example AWS credentials below work with SeaweedFS setup from above):
   ```bash
   export AWS_ACCESS_KEY_ID=admin
   export AWS_SECRET_ACCESS_KEY=admin
   export AWS_ENDPOINT_URL=http://localhost:8333
   export AWS_S3_USE_PATH_STYLE=true
   ```

4. **Generate and upload the shard config:**
   ```bash
   ./create-shard-config.sh \
       --vip 10.0.0.100 --shard dev --bucket nstance \
       --s3-endpoint http://localhost:8333 \
       --userdata ./my-userdata.sh # or https://example.com/userdata.sh
   ```

5. **Generate a shared encryption key** (then copy to all nodes):
   ```bash
   mkdir -p /etc/nstance
   openssl rand 32 > /etc/nstance/encryption.key
   ```

6. **Set up DHCP on the VM bridge** for dev/test, if VMs are on a private VLAN without an existing DHCP server, run on one node only:
   ```bash
   ./dnsmasq-test-setup.sh --interface vmbr1
   ```

### Run On Each Proxmox VE Node

7. **Enable NAT for VM internet access** (if VMs are on a private bridge without a gateway, replacing `<subnet-cidr>`):
   ```bash
   sysctl -w net.ipv4.ip_forward=1
   echo "net.ipv4.ip_forward=1" > /etc/sysctl.d/99-nat-forward.conf
   iptables -t nat -A POSTROUTING -s <subnet-cidr> -o vmbr0 -j MASQUERADE
   apt-get install -y iptables-persistent && netfilter-persistent save
   ```

8. **Export env vars from earlier** (replace `<token-secret>` with the value from step 2):
   ```bash
   export PROXMOX_TOKEN_SECRET='<token-secret>'
   export PROXMOX_API_URL='https://localhost:8006/api2/json' # optional, this is the default
   export PROXMOX_TOKEN_ID='nstance@pve!nstance-token'
   export AWS_ACCESS_KEY_ID=admin
   export AWS_SECRET_ACCESS_KEY=admin
   export AWS_ENDPOINT_URL=http://localhost:8333
   export AWS_S3_USE_PATH_STYLE=true
   ```

9. **Create VM template** on each node (can skip if using shared storage and already created):
   ```bash
   ./vm-template-setup.sh
   ```

10. **Install nstance-server using keepalived for a Virtual IP** (replace `<virtual-ip>` with a valid IP address):

    Note: in production, you may want a different solution to keepalived and VRRP. This is provided as a reference. Configuration of your network is not in scope here.
    ```bash
    NSTANCE_VERSION=$(curl -sL https://api.github.com/repos/nstance-dev/nstance/releases/latest | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4)
    curl -fSL -o nstance-server.tar.gz "https://github.com/nstance-dev/nstance/releases/download/${NSTANCE_VERSION}/nstance-server_${NSTANCE_VERSION#v}_linux_amd64.tar.gz" && tar -xvzf nstance-server.tar.gz
    ./server-with-keepalived.sh \
        --server-binary ./nstance-server \
        --vip <virtual-ip> --shard dev --bucket nstance
    ```

11. **Verify nstance-server is running**
   ```bash
   systemctl status nstance-server
   ```
