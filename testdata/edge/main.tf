variable "names" {
  type = set(string)
}

variable "conf" {
  type = object({
    size = number
    zone = optional(string, "a")
  })
  default = { size = 1 }
}

locals {
  a     = local.b
  b     = local.a
  enc   = base64encode("hi")
  rep   = replace("a-b-c", "/-/", "_")
  hash  = md5("x")
  file  = trimspace(file("${path.module}/hello.txt"))
  has   = fileexists("hello.txt")
  ws    = terraform.workspace
  pfx   = startswith("prefix", "pre")
  tpl   = templatefile("x.tpl", {})
  dec   = base64decode("aGk=")
  bad   = base64decode("!!")
  sha1  = sha1("x")
  sha2  = sha256("x")
  ends  = endswith("abc", "bc")
  incl  = strcontains("abc", "z")
  rep2  = replace("a-b", "-", "+")
  nofile = file("nope.txt")
}

resource "aws_instance" "x" {
  for_each = var.names
  ami      = each.key
}

resource "aws_instance" "y" {
  count = var.unknown_count
  ami   = "ami-${count.index}"
}

resource "aws_instance" "z" {
  dynamic "ebs" {
    for_each = var.names
    iterator = d
    content {
      name = d.value
    }
  }

  provisioner "local-exec" {
    command = "echo"
  }
}

output "conf" {
  value = var.conf
}
