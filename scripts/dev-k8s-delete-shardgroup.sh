#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0
#
# Helper script to delete an NstanceShardGroup in dev environment
# This sets deletionTimestamp to trigger finalizer logic instead of just removing the file

set -e

if [ -z "$1" ]; then
    echo "Usage: $0 <shardgroup-name> [namespace]"
    echo "Example: $0 hello--dev-1"
    exit 1
fi

NAME="$1"
NAMESPACE="${2:-default}"
FILE="temp/dev-k8s/nstanceshardgroups/${NAMESPACE}/${NAME}.json"

if [ ! -f "$FILE" ]; then
    echo "Error: $FILE does not exist"
    exit 1
fi

# Set deletionTimestamp to trigger finalizer
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
jq --arg ts "$TIMESTAMP" '.metadata.deletionTimestamp = $ts' "$FILE" > "${FILE}.tmp" && mv "${FILE}.tmp" "$FILE"

echo "Set deletionTimestamp on $NAME - finalizer should now process deletion"
