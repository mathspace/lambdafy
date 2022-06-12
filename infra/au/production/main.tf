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
      name = "lambdafy-au-production"
    }
  }
}

locals {
  env_name = "au-production"
  tags = {
    TerraformWorkspace  = "lambdafy-${local.env_name}"
    TerraformProjectURL = "https://github.com/mathspace/lambdafy/tree/main/infra/au/production"
  }
}

provider "aws" {
  allowed_account_ids = ["132893848241"] // Production
  region              = "ap-southeast-2"
  default_tags {
    tags = local.tags
  }
}

data "aws_region" "current" {}

module "lambdafy" {
  source = "../../modules/lambdafy"
}
