variable "x" {}

resource "null_resource" "n" {
  triggers = {
    x = var.x
  }
}
