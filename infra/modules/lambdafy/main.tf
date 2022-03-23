resource "aws_ecr_repository" "main" {
  name = "lambdafy" // this is referenced as default value for LAMBDAFY_ECR_REPO
}
