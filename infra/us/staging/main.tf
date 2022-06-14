terraform {
  required_version = ">=1"

  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }

  backend "remote" {
    organization = "mathspace"

    workspaces {
      name = "lambdafy-us-staging"
    }
  }
}

locals {
  env_name = "us-staging"
  tags = {
    TerraformWorkspace  = "lambdafy-${local.env_name}"
    TerraformProjectURL = "https://github.com/mathspace/lambdafy/tree/main/infra/us/staging"
  }
}

provider "aws" {
  allowed_account_ids = ["744621673185"] // Staging
  region              = "us-west-2"
  default_tags {
    tags = local.tags
  }
}

data "aws_region" "current" {}

module "lambdafy" {
  source               = "../../modules/lambdafy"
  create_iam_resources = false // IAM resources exist in the au region
}
