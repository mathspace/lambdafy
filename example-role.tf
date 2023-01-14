// There are two roles in this example:
//
// 1. The role that lambdafy tool requires to be able to create and manage the
//    functions (lambdafy).

resource "aws_iam_user" "lambdafy" {
  name = "lambdafy-cli"
}

data "aws_iam_policy_document" "lambdafy" {
  version = "2012-10-17"

  statement {
    effect    = "Allow"
    actions   = ["lambda:*"]
    resources = ["*"]
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
    resources = ["*"]
  }

  statement {
    effect = "Allow"
    actions = [
      "iam:CreateRole",
      "iam:PutRolePolicy",
      "iam:UpdateRole",
    ]
    resources = [
      "arn:aws:iam::*:role/lambdafy-*",
    ]
  }

  statement {
    effect = "Allow"
    actions = [
      "ecr:CreateRepository",
      "ecr:GetAuthorizationToken",
    ]
    resources = ["*"]
  }

}

resource "aws_iam_user_policy" "lambdafy" {
  name   = aws_iam_user.lambdafy.name
  user   = aws_iam_user.lambdafy.name
  policy = data.aws_iam_policy_document.lambdafy.json
}

// 2. The role for the Lambda function itself (fn) - this is not needed if
//    'role: generate' is used in the lambdafy spec.

resource "aws_iam_role" "fn" {
  name               = "my-custom-function"
  assume_role_policy = <<EOF
{{.AssumeRolePolicy -}}
  EOF
  inline_policy {
    name = "main"
    // TODO add your custom statements after the main one below. E.g.:
    // {
    //   "Effect": "Allow",
    //   "Action": ["ssm:GetParameter"],
    //   "Resource": ["arn:aws:ssm:*:*:parameter/my_fn/*"]
    // }
    policy = <<EOF
{{.InlinePolicy -}}
    EOF
  }
}
