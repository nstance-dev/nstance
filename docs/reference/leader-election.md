---
title: "Leader Election"
weight: 35
description: "How Nstance Server uses object storage-based leader election for shard and cluster leadership."
---

# Leader Election

Nstance Server uses the [S3lect](https://github.com/podplane/s3lect) Go package for leader election, which uses a single object storage lockfile per leader election group.

It has two leader election groups:

## 1. Shard Leader

Used for managing which Nstance Server is the leader for a given zone shard.

This enables having multiple running Nstance Server processes for a single shard, commonly used to run a hot-standby in the event the leader fails (e.g. VM is rescheduled).

Once successfully elected leader, the Nstance Server assigns the leader network (e.g., attaching an ENI on AWS, or assigning an alias IP on Google Cloud) to ensure a stable IP address is available for all Nstance Agents and the Nstance Operator to communicate with it as leader for that shard.

The expected failover time is 11-15 seconds for inbound requests to the per-shard.

Nstance Servers which are not elected as a leader for their shard will idle until elected - essentially you only run multiple instances of Nstance Server per shard if you want to have a hot standy for faster recovery.

## 2. Cluster Leader

Used for managing the creation and rotation of secrets such as CA private key.

This ensures rotation of secrets such as the CA private key is a coordinated event across zone shards, because there should only be a single CA for all shards within a cluster.

Once successfully elected leader, the Nstance Server should check for the existence of the required secrets, and create them if necessary - with future support for automatic rotation planned.
