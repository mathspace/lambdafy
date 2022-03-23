data "aws_iam_policy_document" "fn_assume" {
  version = "2012-10-17"
  statement {
    effect = "Allow"
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
    actions = ["sts:AssumeRole"]
  }
}

data "aws_iam_policy_document" "fn" {
  version = "2012-10-17"
  // Required statements for things to work
  statement {
    effect    = "Allow"
    actions   = ["logs:*"]
    resources = ["arn:aws:logs:*:*:log-group:/aws/lambda/lambdafy-*"]
  }
  statement {
    effect    = "Allow"
    actions   = ["ssm:GetParameter"]
    resources = ["arn:aws:ssm:*:*:parameter/lambdafy/*"]
  }
  statement {
    effect    = "Allow"
    actions   = ["kms:Decrypt"]
    resources = ["*"]
  }
  // TODO Your custom statements go here
}

resource "aws_iam_policy" "fn" {
  name   = "my-custom-function" // TODO modify before use
  policy = data.aws_iam_policy_document.fn.json
}

// NOTE: ARN of the following resource needs to be specified in the spec file
resource "aws_iam_role" "fn" {
  name               = "my-custom-function" // TODO modify before use
  assume_role_policy = data.aws_iam_policy_document.fn_assume.json
}

resource "aws_iam_role_policy_attachment" "fn" {
  role       = aws_iam_role.fn.name
  policy_arn = aws_iam_policy.fn.arn
}
