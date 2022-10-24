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
        // TODO your custom statements go here. E.g.:
        // {
        //   Action   = ["ssm:GetParameter"]
        //   Effect   = "Allow"
        //   Resource = ["arn:aws:ssm:*:*:parameter/my_fn/*"]
        // },
      ]
    })
  }
}
