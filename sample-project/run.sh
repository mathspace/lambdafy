#!/usr/bin/env bash
# This script demonsrates the pieces needed to build, publish and deploy a
# lambda function using lambdafy. You can re-run the script to rebuild and
# update the function.

set -euo pipefail

echo "=> Create temporary files for stdout and stderr" >&2

_out="$(mktemp)"
_err="$(mktemp)"
trap 'rm -f "$_out" "$_err"' EXIT

echo "=> Get AWS account ID" >&2

_account="$(aws sts get-caller-identity --query Account --output text)"
if ! echo "$_account" | grep -q '^[0-9]\{12\}$'; then
  echo "Failed to get AWS account ID" >&2
  exit 1
fi

echo "=> Get effective AWS region by making an actual call to AWS" >&2

aws ec2 describe-availability-zones --query 'AvailabilityZones[0].[RegionName]' --output text > "$_out" 2> "$_err" || {
  echo "Failed to get AWS region" >&2
  cat "$_err" >&2
  exit 1
}
_region="$(cat "$_out")"

echo "=> Create ECR repo for docker image" >&2

_repo_name="lambdafy-sample-project"
_repo_uri="${_account}.dkr.ecr.${_region}.amazonaws.com/${_repo_name}"

aws ecr create-repository --repository-name $_repo_name > "$_out" 2> "$_err" || {
  if ! grep -qi "already exist" "$_err"; then
    echo "Failed to create ECR repo" >&2
    cat "$_err" >&2
    exit 1
  fi
}

echo "=> Login to ECR" >&2

eval "$(aws --region "$_region" ecr get-login | sed 's/ -e none//')"

echo "=> Build the docker image" >&2

docker build -t lambdafy-sample-project -t "$_repo_uri" .

echo "=> Lambdafy the docker image" >&2

lambdafy make "$_repo_uri"

echo "=> Push the docker image to ECR" >&2

docker push "$_repo_uri"

echo "=> Publish a new version of the function (create if needed)" >&2

AWS_DEFAULT_REGION="$_region" lambdafy publish spec.yaml -v IMG="$_repo_uri" > "$_out"

echo "=> Deploy the function" >&2

AWS_DEFAULT_REGION="$_region" lambdafy deploy lambdafy-sample-project "$(grep -i version "$_out" | cut -d: -f2)"

echo "=> Done!"

echo
echo -n "* Visit at "
AWS_DEFAULT_REGION="$_region" lambdafy info -k url lambdafy-sample-project

echo '* To view live logs, run `lambdafy logs --tail lambdafy-sample-project`'
echo '* To delete the function, run `lambdafy delete lambdafy-sample-project`'
echo '* To cleanup generated roles, run `lambdafy cleanup-roles`'
echo '* You will need to manually delete the other resources (e.g. ECR Repo)'
