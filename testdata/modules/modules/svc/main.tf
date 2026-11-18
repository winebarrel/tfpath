variable "name" {
  type = string
}

variable "size" {
  type    = number
  default = 1
}

resource "aws_sqs_queue" "q" {
  name = var.name
}

output "queue_name" {
  value = aws_sqs_queue.q.name
}

output "label" {
  value = "${var.name}:${var.size}"
}
