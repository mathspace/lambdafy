resource "aws_iam_role" "fn" {
  name = "my-custom-function" // TODO modify before use
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principals = [{
        Type        = "Service"
        Identifiers = ["lambda.amazonaws.com"]
      }]
      Actions = ["sts:AssumeRole"]
    }]
  })
  inline_policy {
    name = "main"
    policy = jsonencode({
      Version = "2012-10-17"
      Statement = [
        // Allow VPC access from lambda
        {
          Effect = "Allow"
          Action = [
            "logs:CreateLogGroup",
            "logs:CreateLogStream",
            "logs:PutLogEvents",
            "ec2:CreateNetworkInterface",
            "ec2:DescribeNetworkInterfaces",
            "ec2:DeleteNetworkInterface",
            "ec2:AssignPrivateIpAddresses",
            "ec2:UnassignPrivateIpAddresses",
          ]
          Resource = ["*"]
        },
        // TODO your custom statements go here. E.g.:
        // {
        //   Effect   = "Allow"
        //   Action   = ["ssm:GetParameter"]
        //   Resource = ["arn:aws:ssm:*:*:parameter/my_fn/*"]
        // },
      ]
    })
  }
}
