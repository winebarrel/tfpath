variable "bucket" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = var.bucket
}

module "inner" {
  source = "../inner"
  x      = upper(var.bucket)
}

output "bucket_name" {
  value = var.bucket
}

output "arn" {
  value = aws_s3_bucket.this.arn
}

output "path" {
  value = path.module
}
