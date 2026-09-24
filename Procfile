# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

s3: mkdir -p temp/logs && go run ./cmd/dev-s3 -addr 127.0.0.1:$DEV_S3_PORT -dir $DEV_RUN_DIR/dev-s3 2>&1 | tee temp/logs/dev-s3.log
server: ./scripts/dev-server.sh
proxy: ./scripts/dev-proxy.sh
tunnel: ./scripts/dev-tunnel.sh
k8s: mkdir -p temp/logs && go run ./cmd/dev-k8s -addr 127.0.0.1:$DEV_K8S_PORT -dir $DEV_RUN_DIR/dev-k8s 2>&1 | tee temp/logs/dev-k8s.log
operator: mkdir -p temp/logs && ./scripts/dev-operator.sh 2>&1 | tee temp/logs/operator.log
