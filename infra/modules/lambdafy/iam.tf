// All resources here are global across a single AWS account.

resource "aws_iam_user" "ci" {
  count = var.create_iam_resources ? 1 : 0
  name  = "lambdafy-ci"
}

data "aws_iam_policy_document" "ci" {
  count   = var.create_iam_resources ? 1 : 0
  version = "2012-10-17"

  statement {
    effect = "Allow"
    actions = [
      "lambda:*",
    ]
    resources = [
      "arn:aws:lambda:*:*:function:lambdafy-*",
    ]
  }

  statement {
    effect = "Allow"
    actions = [
      "iam:GetRole",
      "iam:GetRolePolicy",
      "iam:ListAttachedRolePolicies",
      "iam:ListRolePolicies",
      "iam:PassRole",
      "iam:SimulatePrincipalPolicy",
    ]
    resources = [
      aws_iam_role.fn[0].arn,
    ]
  }

  statement {
    effect    = "Allow"
    actions   = ["ecr:*"]
    resources = ["arn:aws:ecr:*:*:repository/lambdafy"]
  }

  statement {
    effect    = "Allow"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    effect = "Allow"
    actions = [
      "logs:*",
    ]
    resources = [
      "arn:aws:logs:*:*:log-group:/aws/lambda/lambdafy-*",
    ]
  }
}

resource "aws_iam_user_policy" "ci" {
  count  = var.create_iam_resources ? 1 : 0
  name   = aws_iam_user.ci[0].name
  user   = aws_iam_user.ci[0].name
  policy = data.aws_iam_policy_document.ci[0].json
}

data "aws_iam_policy_document" "fn_assume" {
  version = "2012-10-17"

  statement {
    effect = "Allow"

    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }

    actions = [
      "sts:AssumeRole",
    ]
  }
}

data "aws_iam_policy_document" "fn" {
  version = "2012-10-17"

  statement {
    effect = "Allow"
    actions = [
      "logs:*",
    ]
    resources = ["arn:aws:logs:*:*:log-group:/aws/lambda/lambdafy-*"]
  }

  statement {
    effect = "Allow"
    actions = [
      "ssm:GetParameter",
    ]
    resources = [
      "arn:aws:ssm:*:*:parameter/lambdafy/*",
    ]
  }

  statement {
    effect = "Allow"
    actions = [
      "kms:Decrypt",
    ]
    resources = [
      "*", // TODO tighten up
    ]
  }
}

resource "aws_iam_policy" "fn" {
  count  = var.create_iam_resources ? 1 : 0
  name   = "lambdafy"
  policy = data.aws_iam_policy_document.fn.json
}

resource "aws_iam_role" "fn" {
  count              = var.create_iam_resources ? 1 : 0
  name               = "lambdafy" // this is referenced as default value for LAMBDAFY_DEFAULT_ROLE
  assume_role_policy = data.aws_iam_policy_document.fn_assume.json
}

resource "aws_iam_role_policy_attachment" "fn" {
  count      = var.create_iam_resources ? 1 : 0
  role       = aws_iam_role.fn[0].name
  policy_arn = aws_iam_policy.fn[0].arn
}
