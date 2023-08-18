data "aws_iam_policy_document" "lambda" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }

  // TODO: statement for pushing to ECR repo
}

resource "aws_iam_role" "lambda" {
  name               = "chainguard-lambda-role"
  assume_role_policy = data.aws_iam_policy_document.lambda.json
}

resource "aws_ecr_repository" "ecr_repo" {
  name                 = var.dst_repo
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = false
  }
}

resource "ko_build" "image" {
  repo        = aws_ecr_repository.ecr_repo.repository_url
  importpath  = "github.com/imjasonh/terraform-playground/image-copy-ecr/cmd/app"
  working_dir = path.module
  // Disable SBOM generation due to
  // https://github.com/ko-build/ko/issues/878
  sbom = "none"
}

locals {
  // Using a local for the lambda breaks a cyclic dependency between
  // chainguard_identity.aws and aws_lambda_function.lambda
  lambda_name = "chainguard-lambda"
}

data "aws_region" "current" {}

resource "aws_lambda_function" "lambda" {
  function_name = local.lambda_name
  role          = aws_iam_role.lambda.arn

  package_type = "Image"
  image_uri    = ko_build.image.image_ref

  environment {
    variables = {
      GROUP      = var.group
      IDENTITY   = chainguard_identity.aws.id
      ISSUER_URL = "https://issuer.enforce.dev"
      DST_REPO   = aws_ecr_repository.ecr_repo.repository_url
      REGION     = data.aws_region.current.name
    }
  }
}

resource "aws_lambda_function_url" "lambda" {
  function_name      = aws_lambda_function.lambda.function_name
  authorization_type = "NONE"
}
