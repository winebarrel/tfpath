variable "env" {
  type    = string
  default = "dev"
}

variable "port" {
  type = number
}

variable "tags" {
  type    = map(string)
  default = {}
}

variable "suffix" {}

locals {
  name        = "${var.env}-app"
  common_tags = merge(var.tags, { Env = var.env })
  upper_name  = upper(local.name)
  zones       = ["a", "c"]
}

resource "aws_s3_bucket" "logs" {
  bucket = "${local.name}-logs"
  tags   = local.common_tags
}

resource "aws_iam_role_policy_attachment" "this" {
  role       = aws_iam_role.app.name
  policy_arn = "arn:aws:iam::aws:policy/${var.suffix}"
}

resource "aws_subnet" "private" {
  count             = length(local.zones)
  availability_zone = "ap-northeast-1${local.zones[count.index]}"

  tags = {
    Name = "private-${count.index}"
    Role = aws_iam_role.app.name
  }
}

resource "aws_instance" "web" {
  for_each      = toset(["a", "b"])
  instance_type = each.key == "a" ? "t3.micro" : "t3.small"

  root_block_device {
    volume_size = var.port
  }
}

resource "aws_security_group" "sg" {
  name = local.name

  dynamic "ingress" {
    for_each = [80, 443]
    content {
      from_port = ingress.value
      to_port   = ingress.value
    }
  }

  egress {
    from_port = 0
  }
}

data "aws_iam_policy_document" "p" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.logs.arn}/*"]
  }
}

output "bucket_arn" {
  value = aws_s3_bucket.logs.arn
}

output "name" {
  value = local.name
}
