// There are two roles in this example:
//
// 1. The role that lambdafy tool requires to be able to create and manage the
//    functions (lambdafy).

resource "aws_iam_user" "lambdafy" {
  name = "lambdafy-cli"
}

resource "aws_iam_user_policy" "lambdafy" {
  name   = aws_iam_user.lambdafy.name
  user   = aws_iam_user.lambdafy.name
  policy = <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["lambda:*"],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "iam:GetRole",
        "iam:GetRolePolicy",
        "iam:ListAttachedRolePolicies",
        "iam:ListRolePolicies",
        "iam:PassRole",
        "iam:SimulatePrincipalPolicy"
      ],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "iam:CreateRole",
        "iam:PutRolePolicy",
        "iam:UpdateRole"
      ],
      "Resource": [
        "arn:aws:iam::*:role/lambdafy-*"
      ]
    },
    {
      "Effect": "Allow",
      "Action": [
        "ecr:CreateRepository",
        "ecr:GetAuthorizationToken"
      ],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeSecurityGroups"
      ],
      "Resource": ["*"]
    }
  ]
}
EOF
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
