# Nstance: Fast, Cloud-Agnostic VM Auto-Scaler

Nstance is a faster alternative to traditional Auto-Scaling Groups (ASGs), replacing glue scripts and complexity with a cloud-agnostic VM provisioner.

Nstance is Open Source, released under the Apache 2.0 license.

Key features:

* __Faster instance scaling:__
    Provision Kubernetes nodes faster than traditional bootstrap and certificate workflow methods.

* __Not just for Kubernetes:__
    Agnostic design for any workload e.g. Docker Compose VMs, NAT gateways, and more.

* __Multi-Cloud & Hybrid-Cloud:__
    Supports public cloud (AWS, Google Cloud) and on-prem/private cloud (Proxmox VE).

* __Self Healing & Instance Expiry:__
    Automatic detection and replacement of unhealthy instances. Automatic rotation with configurable expiry.

* __Built-in CA:__
    Integrated certificate authority for fast and secure certificate issuance and renewal/rotation.

* __Spot Instance Support:__
    With Kubernetes node draining and termination handling.

* __Kubernetes Auto-Scaling:__
    Nstance includes its own Operator and CRDs which enables integration with the Cluster Autoscaler via Cluster API.

* __On-Demand Instances:__
    Using Kubernetes Pod annotations, the Nstance Operator will create a new dedicated VM for the lifetime of the Pod.

* __Multi-Tenancy:__
    Run one or more Kubernetes clusters with isolation on each Nstance cluster.

Nstance is designed to be simple and easy to operate. To achieve a balance of lowest cost, highest reliability, and easiest operation, Nstance uses simple yet proven cloud primitives - VMs (and optionally, ASGs), a cloud secrets store (AWS Systems Manager Parameter Store by default on AWS and Google Cloud Secret Manager by default on Google Cloud), and object storage (such as AWS S3, Google Cloud Storage, or S3-compatible services supporting If-Match headers such as Ceph RGW or SeaweedFS).

## Documentation

Check out the comprehensive documentation in [./docs](./docs) or read it rendered on the official Nstance website at <https://nstance.dev>

## Development

See [docs/development/local-setup.md](docs/development/local-setup.md) for development environment setup and usage.

## License

Nstance is licensed under the Apache License, Version 2.0.
Copyright The Nstance Authors

See the [LICENSE](./LICENSE) file for details.
