// There are two IAM resources in this example:
//
// 1. The IAM user that lambdafy tool uses to create and manage functions.

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
        "iam:PassRole",
        "iam:SimulatePrincipalPolicy"
      ],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "logs:DescribeLogGroups"
      ],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "logs:CreateLogGroup",
        "logs:PutRetentionPolicy"
      ],
      "Resource": [
        "arn:aws:logs:*:*:log-group:/aws/lambda/*"
      ]
    },
    {
      "Effect": "Allow",
      "Action": [
        "scheduler:DeleteScheduleGroup",
        "scheduler:CreateScheduleGroup",
        "scheduler:CreateSchedule",
        "scheduler:DeleteSchedule"
      ],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "ecr:*"
      ],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeSubnets",
        "ec2:DescribeVpcs"
      ],
      "Resource": ["*"]
    }
  ]
}
EOF
}

// 2. The role for the Lambda function itself (fn) - this is not needed if
//    you already have an equivalent execution role.

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
