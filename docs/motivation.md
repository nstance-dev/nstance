---
title: "Motivation"
weight: 1
description: "Background context on why Nstance was created"
---

# Motivation for creating Nstance

Nstance was created to solve several challenges with current VM architectures.

This document specifically uses Kubernetes cluster deployments to provide concrete examples, however keep in mind that Nstance can be used without Kubernetes and many of the motivations stand without Kubernetes.

Nstance project values:

1. Simplicity: make infrastructure easy to understand, set up, and operate, with sensible defaults.

2. Security: focus on secure design from the start, with a firm belief that avoiding complexity supports better security.

3. Durability: be easy to operate, with "what happens if I delete everything by accident" as the simplest litmus test.

4. Minimalism: aiming to use low-cost primitives with longevity, for minimal cost-of-service and minimal future cost-of-change.

The key motivations for Nstance are:

1. [Simplifying Infrastructure Bootstrapping](#1-simplifying-infrastructure-bootstrapping)

2. [Separating Concerns](#2-separating-concerns)

3. [Scale-To-Zero Support](#3-scale-to-zero-support)

4. [True Cattle-Not-Pets & No Chaos](#4-true-cattle-not-pets--no-chaos)

5. [Multi-Cloud & On-Prem Portability](#5-multi-cloud--on-prem-portability)

6. [Faster Auto-Scaling & Failure Detection](#6-faster-auto-scaling--failure-detection)

7. [Spot Instance Support](#7-spot-instance-support)

## 1. Simplifying Infrastructure Bootstrapping

Philosophically, Nstance takes a different path to conventional approaches with infrastructure.

Nstance believes in a GitOps approach to infrastructure and using two tools for distinct purposes:

1. ArgoCD/FluxCD style technologies for managing in-cluster resources

2. OpenTofu/Terraform for managing out-of-cluster resources

With a notion that you should not have a single service as a single-point-of-failure for deploying either.

For example, it's common to use something like Cluster API (CAPI) and have a "management cluster" which then is responsible for deploying and configuring multiple "workload clusters". But what happens if you upgrade and break your management cluster? And while it's broken, you need to deal with an incident in a workload cluster? The Nstance philosophy avoids this problem entirely.

## 2. Separating Concerns

Nstance separates three concerns:

1. Requesting to scale VMs up and down - done by Nstance Operator (in cluster), or Nstance Admin tool (when not using Kubernetes)

2. Actually scaling VMs up and down - done by Nstance Server (outside of cluster)

3. Creating the infrastructure surrounding those VMs (VPCs, Subnets, etc) - done via OpenTofu/Terraform

If principle-of-least-privilege roles/permissions are used (as is done with Nstance deployment examples), this means no workload running in-cluster has the ability to hijack credentials or exploit in-cluster containers to connect to your cloud provider APIs.

Nstance Server's role is also limited to only being able to affect change on the minimal amount of resources needed to scale VMs; critical services like object storage, databases, DNS, and more are restricted from Nstance Server as well.

Another notable aspect of Nstance's design is that the Certificate Authority (CA) key lives outside of the Kubernetes cluster and is not stored in its etcd KV store accessible via the kube-apiserver.

## 3. Scale-To-Zero Support

Often a Kubernetes cluster will have non-trivial instance sizes. In many environments, the cluster may not be used 24/7, and admins therefore may find the notion of automatically scaling to zero appealing. Traditionally, doing this with a Kubernetes cluster introduces a lot of complexity. When a new node bootstraps, it expects to be able to contact the Kubernetes API. It works when you keep the control plane running, but given this often has a cost associated, we don't consider this to be true scale-to-zero.

The Nstance approach is to support running Nstance Server on the smallest possible VM available; often this can be cheaper than even a single IPv4 address. You can rapidly scale cluster VMs to zero and back up to many.

And to support true scale-to-zero, in public clouds we opt to place each Nstance Server in a traditional Auto-Scaling Group (ASG), and Nstance Server itself supports scaling to zero - as object storage is used for state persistence.

## 4. True Cattle-Not-Pets & No Chaos

The "cattle not pets" analogy in DevOps refers to treating servers as disposable resources (cattle) rather than unique, irreplaceable systems (pets). While this concept has been around since around 2011-2012, implementing it is still too hard.

Chaos Engineering is the practice of intentionally introducing controlled failures - like server shutdowns, latency injections, or network issues - to test a system's resilience and uncover hidden weaknesses.

Some folks use controlled server shutdowns (Chaos Engineering) as their litmus test of whether they have achieved "cattle not pets". The goal with Nstance was that doing this approach on a fresh cluster should always yield a successful outcome: servers should be *immediately* replaced, with no additional setup or complexity. Even if you delete the last VM in the cluster, it should self-heal.

## 5. Multi-Cloud & On-Prem Portability

Many organisations struggle with multi-cloud. Managed Kubernetes offerings can significantly differ across clouds. But many of the primitives like VPCs, Subnets, VMs, object storage, etc. are very similar. The goal with Nstance was that in-cluster configuration running on an Nstance cluster could be cloud-agnostic, and use of Nstance in multi-cloud or hybrid-cloud setups could streamline and simplify implementation.

For example:

* Subnet Pools help make configuration portable: groups synced across an Nstance shard in AWS and in Google Cloud can both reference a set of subnets by name, and the specific subnet resources are mapped in each cloud's shard config (set via OpenTofu/Terraform).

* Userdata to configure a VM can be entirely cloud-agnostic: Nstance Agent handles receiving secrets, TLS certificates, etc. and the underlying secrets or encryption keys can be stored in cloud-specific storage (e.g. AWS Systems Manager Parameter Store by default, or AWS Secrets Manager explicitly) while Nstance Server handles the abstraction layer transparently to each VM.

## 6. Faster Auto-Scaling & Failure Detection

If you've ever had an incident where you're waiting for an Auto-Scaling Group (ASG) to create a replacement instance, you'll probably feel this: there's no visibility or predictability into when a replacement VM will be created, and it can take tens of seconds (or longer) to just be created.

Nstance's goal is to start VM replacement in milliseconds, not seconds or minutes. As soon as a shutdown signal is received, Nstance Agent reports it to Nstance Server, and Nstance Server checks in with the cloud provider API to determine if an immediate VM replacement is necessary (or after a configurable grace window, replaces it anyway).

## 7. Spot Instance Support

You might be thinking: points 4 and 6 would probably make it trivial to support spot instances. The good news is: your thinking is correct.

At the start, this was not actually a design goal, but with the right set of constraints from other design goals, the design has lent itself to tackle many wishlist items either inherently or with minor additions, which makes Nstance an encouraging foundation to build from.
