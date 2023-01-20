#!/usr/bin/env bash
# This script demonsrates the pieces needed to build, publish and deploy a
# lambda function using lambdafy. You can re-run the script to rebuild and
# update the function.

set -euo pipefail

echo "=> Build the docker image" >&2

docker build --platform=linux/amd64 -t lambdafy-sample-project .

echo "=> Lambdafy, push and publish a new version of the function (create if needed)" >&2

lambdafy publish --alias main --force-update-alias spec.yaml

echo "=> Deploy the function" >&2

lambdafy deploy lambdafy-sample-project main > /dev/null

echo "=> Done!"

echo
echo -n "* Visit at "
lambdafy info -o '{{.url}}' lambdafy-sample-project

echo
echo '* To view live logs, run `lambdafy logs --tail lambdafy-sample-project`'
echo '* To delete the function, run `lambdafy delete lambdafy-sample-project`'
echo '* To cleanup generated roles, run `lambdafy cleanup-roles`'
echo '* You will need to manually delete the other resources (e.g. ECR Repo)'