resource "aws_iam_role" "fn" {
  name               = "my-custom-function" // TODO modify before use
  assume_role_policy = <<-EOF
  {
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principals": [{
        "Type": "Service",
        "Identifiers": ["lambda.amazonaws.com"]
      }],
      "Actions": ["sts:AssumeRole"]
    }]
  }
  EOF
  inline_policy {
    name = "main"
    // TODO add your custom statements after the main one below. E.g.:
    // {
    //   "Effect": "Allow",
    //   "Action": ["ssm:GetParameter"],
    //   "Resource": ["arn:aws:ssm:*:*:parameter/my_fn/*"]
    // }
    policy = <<-EOF
    {
      "Version": "2012-10-17",
      "Statement": [
        {
          "Effect": "Allow",
          "Action": [
            "logs:CreateLogGroup",
            "logs:CreateLogStream",
            "logs:PutLogEvents",
            "ec2:CreateNetworkInterface",
            "ec2:DescribeNetworkInterfaces",
            "ec2:DeleteNetworkInterface",
            "ec2:AssignPrivateIpAddresses",
            "ec2:UnassignPrivateIpAddresses"
          ],
          "Resource": ["*"]
        }
      ]
    }
    EOF
  }
}
