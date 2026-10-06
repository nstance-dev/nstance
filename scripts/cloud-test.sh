#!/bin/bash
# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0
#
# Builds and deploys standalone Nstance cloud test clusters from local source.

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
PROVIDER=${1:-}
ACTION=${2:-}

case "$PROVIDER" in
  aws)
    ARCH=arm64
    WORK_DIR="$ROOT_DIR/temp/aws-test"
    ;;
  google)
    ARCH=amd64
    WORK_DIR="$ROOT_DIR/temp/google-test"
    ;;
  *)
    echo "Usage: $0 (aws|google) (prepare|upload|apply|destroy)" >&2
    exit 1
    ;;
esac

ENV_FILE="$WORK_DIR/test.env"
ARTIFACT_VARS_FILE="$WORK_DIR/artifacts.auto.tfvars.json"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    exit 1
  fi
}

require_variable() {
  if [[ -z ${!1:-} ]]; then
    echo "Required environment variable is not set: $1" >&2
    exit 1
  fi
}

load_environment() {
  if [[ ! -f "$ENV_FILE" ]]; then
    echo "Test deployment is not prepared. Run 'make $PROVIDER-prepare' first." >&2
    exit 1
  fi
  # shellcheck disable=SC1090
  source "$ENV_FILE"
}

write_environment() {
  mkdir -p "$WORK_DIR"
  {
    printf 'TEST_CLUSTER_ID=%q\n' "$TEST_CLUSTER_ID"
    printf 'ARTIFACT_BUCKET=%q\n' "$ARTIFACT_BUCKET"
    printf 'ARTIFACT_BUCKET_OWNED=%q\n' "$ARTIFACT_BUCKET_OWNED"
    if [[ "$PROVIDER" == aws ]]; then
      printf 'AWS_PROFILE=%q\n' "$AWS_PROFILE"
      printf 'AWS_REGION=%q\n' "$AWS_REGION"
      printf 'AWS_ZONE=%q\n' "$AWS_ZONE"
    else
      printf 'GOOGLE_PROJECT=%q\n' "$GOOGLE_PROJECT"
      printf 'GOOGLE_REGION=%q\n' "$GOOGLE_REGION"
      printf 'GOOGLE_ZONE=%q\n' "$GOOGLE_ZONE"
      printf 'GOOGLE_SIGNER=%q\n' "$GOOGLE_SIGNER"
    fi
  } > "$ENV_FILE"
}

write_deployment_variables() {
  if [[ "$PROVIDER" == aws ]]; then
    cat > "$WORK_DIR/deployment.auto.tfvars.json" <<EOF
{
  "profile": "$AWS_PROFILE",
  "region": "$AWS_REGION",
  "zone": "$AWS_ZONE",
  "cluster_id": "$TEST_CLUSTER_ID",
  "ipv4_enabled": true,
  "ipv6_enabled": true,
  "nat_mode": "nstance"
}
EOF
  else
    cat > "$WORK_DIR/deployment.auto.tfvars.json" <<EOF
{
  "project": "$GOOGLE_PROJECT",
  "region": "$GOOGLE_REGION",
  "zone": "$GOOGLE_ZONE",
  "cluster_id": "$TEST_CLUSTER_ID",
  "ipv4_enabled": true,
  "ipv6_enabled": true,
  "nat_mode": "nstance"
}
EOF
  fi
}

prepare_aws() {
  require_command aws
  require_variable AWS_REGION
  require_variable AWS_ZONE
  AWS_PROFILE=${AWS_PROFILE:-default}
  TEST_CLUSTER_ID=${NSTANCE_TEST_CLUSTER_ID:-nstance-test}

  local account_id
  account_id=$(aws sts get-caller-identity --profile "$AWS_PROFILE" --query Account --output text)
  if [[ -n ${NSTANCE_TEST_ARTIFACT_BUCKET:-} ]]; then
    ARTIFACT_BUCKET=$NSTANCE_TEST_ARTIFACT_BUCKET
    ARTIFACT_BUCKET_OWNED=false
  else
    ARTIFACT_BUCKET="nstance-dev-$account_id-$TEST_CLUSTER_ID"
    ARTIFACT_BUCKET_OWNED=true
  fi

  if ! aws s3api head-bucket --bucket "$ARTIFACT_BUCKET" --profile "$AWS_PROFILE" 2>/dev/null; then
    if [[ "$AWS_REGION" == us-east-1 ]]; then
      aws s3api create-bucket --bucket "$ARTIFACT_BUCKET" --profile "$AWS_PROFILE" >/dev/null
    else
      aws s3api create-bucket --bucket "$ARTIFACT_BUCKET" --region "$AWS_REGION" \
        --create-bucket-configuration "LocationConstraint=$AWS_REGION" --profile "$AWS_PROFILE" >/dev/null
    fi
  fi
}

prepare_google() {
  require_command gcloud
  require_variable GOOGLE_PROJECT
  require_variable GOOGLE_REGION
  require_variable GOOGLE_ZONE
  TEST_CLUSTER_ID=${NSTANCE_TEST_CLUSTER_ID:-nstance-test}
  if [[ -n ${NSTANCE_TEST_ARTIFACT_BUCKET:-} ]]; then
    ARTIFACT_BUCKET=$NSTANCE_TEST_ARTIFACT_BUCKET
    ARTIFACT_BUCKET_OWNED=false
  else
    ARTIFACT_BUCKET="nstance-dev-$GOOGLE_PROJECT-${TEST_CLUSTER_ID:0:12}"
    ARTIFACT_BUCKET_OWNED=true
  fi
  GOOGLE_SIGNER="nstance-dev-artifacts@$GOOGLE_PROJECT.iam.gserviceaccount.com"

  gcloud auth application-default print-access-token >/dev/null
  gcloud services enable iamcredentials.googleapis.com storage.googleapis.com \
    --project "$GOOGLE_PROJECT" >/dev/null
  if ! gcloud storage buckets describe "gs://$ARTIFACT_BUCKET" --project "$GOOGLE_PROJECT" >/dev/null 2>&1; then
    gcloud storage buckets create "gs://$ARTIFACT_BUCKET" --project "$GOOGLE_PROJECT" \
      --location "$GOOGLE_REGION" --uniform-bucket-level-access >/dev/null
  fi
  if ! gcloud iam service-accounts describe "$GOOGLE_SIGNER" --project "$GOOGLE_PROJECT" >/dev/null 2>&1; then
    gcloud iam service-accounts create nstance-dev-artifacts --project "$GOOGLE_PROJECT" \
      --display-name "Nstance development artifact signer" >/dev/null
  fi

  local account member
  account=$(gcloud config get account 2>/dev/null)
  if [[ "$account" == *gserviceaccount.com ]]; then
    member="serviceAccount:$account"
  else
    member="user:$account"
  fi
  gcloud iam service-accounts add-iam-policy-binding "$GOOGLE_SIGNER" --project "$GOOGLE_PROJECT" \
    --member "$member" --role roles/iam.serviceAccountTokenCreator >/dev/null
  gcloud storage buckets add-iam-policy-binding "gs://$ARTIFACT_BUCKET" \
    --member "serviceAccount:$GOOGLE_SIGNER" --role roles/storage.objectViewer >/dev/null
}

prepare() {
  require_command tofu
  mkdir -p "$WORK_DIR"
  if [[ "$PROVIDER" == aws ]]; then
    prepare_aws
  else
    prepare_google
  fi

  sed "s|../../../$PROVIDER/|../../deploy/tf/$PROVIDER/|g" \
    "$ROOT_DIR/deploy/tf/examples/$PROVIDER/single-shard/main.tf" > "$WORK_DIR/main.tf"
  write_environment
  write_deployment_variables
  tofu -chdir="$WORK_DIR" init
  echo "Prepared $PROVIDER test deployment in $WORK_DIR"
}

build_archives() {
  require_command go
  require_command make
  require_command mise
  require_command tar
  local build_dir go_command
  build_dir="$WORK_DIR/build"
  go_command=$(command -v go)
  rm -rf "${build_dir:?}"
  mkdir -p "$build_dir"
  mise install zig@0.17.0
  mise exec zig@0.17.0 -- make -C "$ROOT_DIR" GO="$go_command" BINARYDIR="$build_dir/" \
    GOOS=linux GOARCH="$ARCH" nstance-server nstance-agent
  mkdir -p "$WORK_DIR/artifacts"
  tar -C "$build_dir" -czf "$WORK_DIR/artifacts/nstance-server.tar.gz" nstance-server
  tar -C "$build_dir" -czf "$WORK_DIR/artifacts/nstance-agent.tar.gz" nstance-agent
}

upload_aws() {
  local key_prefix
  key_prefix="$TEST_CLUSTER_ID/builds/$(git -C "$ROOT_DIR" rev-parse --short HEAD)-$(date -u +%Y%m%d%H%M%S)"
  aws s3 cp "$WORK_DIR/artifacts/nstance-server.tar.gz" "s3://$ARTIFACT_BUCKET/$key_prefix/nstance-server.tar.gz" \
    --region "$AWS_REGION" --profile "$AWS_PROFILE" >/dev/null
  aws s3 cp "$WORK_DIR/artifacts/nstance-agent.tar.gz" "s3://$ARTIFACT_BUCKET/$key_prefix/nstance-agent.tar.gz" \
    --region "$AWS_REGION" --profile "$AWS_PROFILE" >/dev/null
  SERVER_URL=$(aws s3 presign "s3://$ARTIFACT_BUCKET/$key_prefix/nstance-server.tar.gz" \
    --expires-in 604800 --region "$AWS_REGION" --profile "$AWS_PROFILE")
  AGENT_URL=$(aws s3 presign "s3://$ARTIFACT_BUCKET/$key_prefix/nstance-agent.tar.gz" \
    --expires-in 604800 --region "$AWS_REGION" --profile "$AWS_PROFILE")
}

upload_google() {
  local key_prefix
  key_prefix="$TEST_CLUSTER_ID/builds/$(git -C "$ROOT_DIR" rev-parse --short HEAD)-$(date -u +%Y%m%d%H%M%S)"
  gcloud storage cp "$WORK_DIR/artifacts/nstance-server.tar.gz" \
    "gs://$ARTIFACT_BUCKET/$key_prefix/nstance-server.tar.gz" --project "$GOOGLE_PROJECT" >/dev/null
  gcloud storage cp "$WORK_DIR/artifacts/nstance-agent.tar.gz" \
    "gs://$ARTIFACT_BUCKET/$key_prefix/nstance-agent.tar.gz" --project "$GOOGLE_PROJECT" >/dev/null
  SERVER_URL=$(gcloud storage sign-url "gs://$ARTIFACT_BUCKET/$key_prefix/nstance-server.tar.gz" \
    --duration=12h --impersonate-service-account="$GOOGLE_SIGNER" --format='value(signed_url)')
  AGENT_URL=$(gcloud storage sign-url "gs://$ARTIFACT_BUCKET/$key_prefix/nstance-agent.tar.gz" \
    --duration=12h --impersonate-service-account="$GOOGLE_SIGNER" --format='value(signed_url)')
}

upload() {
  load_environment
  build_archives
  if [[ "$PROVIDER" == aws ]]; then
    require_command aws
    upload_aws
  else
    require_command gcloud
    upload_google
  fi
  cat > "$ARTIFACT_VARS_FILE" <<EOF
{
  "nstance_server_binary_url": "$SERVER_URL",
  "nstance_agent_binary_url": "$AGENT_URL"
}
EOF
  echo "Built and uploaded $PROVIDER test binaries for $ARCH"
}

apply() {
  load_environment
  if [[ ! -f "$ARTIFACT_VARS_FILE" ]]; then
    echo "Test binaries have not been uploaded. Run 'make $PROVIDER-upload' first." >&2
    exit 1
  fi
  tofu -chdir="$WORK_DIR" apply
}

destroy_aws() {
  tofu -chdir="$WORK_DIR" destroy -auto-approve -target=module.shard.aws_autoscaling_group.server

  local instance_ids bucket_name
  instance_ids=$(aws ec2 describe-instances --profile "$AWS_PROFILE" --region "$AWS_REGION" \
    --filters "Name=tag:nstance:managed,Values=true" "Name=tag:nstance:cluster-id,Values=$TEST_CLUSTER_ID" \
      "Name=instance-state-name,Values=running,stopped,pending,stopping" \
    --query 'Reservations[].Instances[].InstanceId' --output text)
  if [[ -n "$instance_ids" ]]; then
    # shellcheck disable=SC2086
    aws ec2 terminate-instances --profile "$AWS_PROFILE" --region "$AWS_REGION" --instance-ids $instance_ids >/dev/null
    # shellcheck disable=SC2086
    aws ec2 wait instance-terminated --profile "$AWS_PROFILE" --region "$AWS_REGION" --instance-ids $instance_ids
  fi

  aws ssm delete-parameters --profile "$AWS_PROFILE" --region "$AWS_REGION" --names \
    "/$TEST_CLUSTER_ID/ca.key" "/$TEST_CLUSTER_ID/registration-nonce.key" >/dev/null

  bucket_name=$(tofu -chdir="$WORK_DIR" state show 'module.cluster.aws_s3_bucket.nstance[0]' 2>/dev/null |
    awk -F'"' '/^[[:space:]]*bucket[[:space:]]*=/ { print $2 }')
  if [[ -n "$bucket_name" ]]; then
    aws s3 rb "s3://$bucket_name" --force --profile "$AWS_PROFILE"
    tofu -chdir="$WORK_DIR" state rm 'module.cluster.aws_s3_bucket.nstance[0]'
  fi
  tofu -chdir="$WORK_DIR" destroy
  aws s3 rm "s3://$ARTIFACT_BUCKET/$TEST_CLUSTER_ID/" --recursive --profile "$AWS_PROFILE"
  if [[ "$ARTIFACT_BUCKET_OWNED" == true ]]; then
    aws s3 rb "s3://$ARTIFACT_BUCKET" --profile "$AWS_PROFILE"
  fi
}

destroy_google() {
  tofu -chdir="$WORK_DIR" destroy -auto-approve \
    -target=module.shard.google_compute_instance_group_manager.server

  gcloud compute instances list --project "$GOOGLE_PROJECT" \
    --filter="labels.nstance-managed=true AND labels.nstance-cluster-id=$TEST_CLUSTER_ID" \
    --format='value(name,zone)' | while read -r name zone; do
      [[ -z "$name" ]] || gcloud compute instances delete "$name" --zone "$zone" \
        --project "$GOOGLE_PROJECT" --quiet
    done

  gcloud secrets list --project "$GOOGLE_PROJECT" --format='value(name)' |
    while read -r secret; do
      case "$secret" in
        "$TEST_CLUSTER_ID-ca.key" | "$TEST_CLUSTER_ID-registration-nonce.key")
          gcloud secrets delete "$secret" --project "$GOOGLE_PROJECT" --quiet
          ;;
      esac
    done

  local bucket_name
  bucket_name=$(tofu -chdir="$WORK_DIR" state show 'module.cluster.google_storage_bucket.nstance[0]' 2>/dev/null |
    awk -F'"' '/^[[:space:]]*name[[:space:]]*=/ { print $2 }')
  if [[ -n "$bucket_name" ]]; then
    gcloud storage rm --recursive "gs://$bucket_name/**" --project "$GOOGLE_PROJECT" 2>/dev/null || true
    gcloud storage buckets delete "gs://$bucket_name" --project "$GOOGLE_PROJECT" --quiet
    tofu -chdir="$WORK_DIR" state rm 'module.cluster.google_storage_bucket.nstance[0]'
  fi
  tofu -chdir="$WORK_DIR" destroy
  gcloud storage rm --recursive "gs://$ARTIFACT_BUCKET/$TEST_CLUSTER_ID/**" \
    --project "$GOOGLE_PROJECT" 2>/dev/null || true
  if [[ "$ARTIFACT_BUCKET_OWNED" == true ]]; then
    gcloud storage buckets delete "gs://$ARTIFACT_BUCKET" --project "$GOOGLE_PROJECT" --quiet
  fi
}

destroy() {
  load_environment
  if [[ "$PROVIDER" == aws ]]; then
    require_command aws
    destroy_aws
  else
    require_command gcloud
    destroy_google
  fi
  rm -rf "$WORK_DIR"
}

case "$ACTION" in
  prepare) prepare ;;
  upload) upload ;;
  apply) apply ;;
  destroy) destroy ;;
  *)
    echo "Usage: $0 (aws|google) (prepare|upload|apply|destroy)" >&2
    exit 1
    ;;
esac
