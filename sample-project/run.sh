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

eval "$(aws ecr get-login | sed 's/ -e none//')"

echo "=> Build the docker image" >&2

docker build -t lambdafy-sample-project -t "$_repo_uri" .

echo "=> Lambdafy the docker image" >&2

lambdafy make "$_repo_uri"

echo "=> Push the docker image to ECR" >&2

docker push "$_repo_uri"

echo "=> Create bare minimum IAM role for lambda" >&2

_role_name="lambdafy-sample-project"

aws iam create-role --role-name "$_role_name" --assume-role-policy-document '{
  "Version":"2012-10-17",
  "Statement":[{
    "Effect":"Allow",
    "Principal":{
      "Service":"lambda.amazonaws.com"
    },
    "Action":"sts:AssumeRole"
  }]
}' > "$_out" 2> "$_err" || {
  if ! grep -qi "already exist" "$_err"; then
    echo "Failed to create IAM role" >&2
    cat "$_err" >&2
    exit 1
  fi
}

echo "=> Attach cloudwatch logs policy to the role so the lambda function can write to logs" >&2

aws iam attach-role-policy --role-name "$_role_name" --policy-arn arn:aws:iam::aws:policy/CloudWatchLogsFullAccess

echo "=> Publish a new version of the function (create if needed)" >&2

sed "s|IMG|$_repo_uri|" spec.yaml | lambdafy publish - > "$_out"

echo "=> Deploy the function" >&2

lambdafy deploy lambdafy-sample-project "$(grep -i version "$_out" | cut -d: -f2)"

echo "=> Done!"

echo
echo -n "* Visit at "
lambdafy info lambdafy-sample-project | sed -n 's/^url:\(.*\)/\1/p'

echo '* To view live logs, run `lambdafy logs --tail lambdafy-sample-project`'
echo '* To delete the function, run `lambdafy delete lambdafy-sample-project`'