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
      name = "lambdafy-us-production"
    }
  }
}

locals {
  env_name = "us-production"
  tags = {
    TerraformWorkspace  = "lambdafy-${local.env_name}"
    TerraformProjectURL = "https://github.com/mathspace/lambdafy/tree/main/infra/us/production"
  }
}

provider "aws" {
  allowed_account_ids = ["132893848241"] // Production
  region              = "us-west-2"
  default_tags {
    tags = local.tags
  }
}

data "aws_region" "current" {}

module "lambdafy" {
  source               = "../../modules/lambdafy"
  create_iam_resources = false // IAM resources are created in au region
}
