locals {
  prefix = "acme"
}

module "web" {
  source = "./modules/web"
  bucket = "${local.prefix}-web"
}

module "svc" {
  source   = "./modules/svc"
  for_each = { api = 1, batch = 2 }
  name     = "${local.prefix}-${each.key}"
  size     = each.value
}

resource "aws_s3_bucket_policy" "p" {
  bucket = module.web.bucket_name
  policy = module.web.arn
}

output "queues" {
  value = [for k, m in module.svc : m.queue_name]
}

module "cnt" {
  source = "./modules/svc"
  count  = 2
  name   = "cnt${count.index}"
}

locals {
  api_label   = module.svc["api"].label
  svc_labels  = { for k, m in module.svc : k => m.label }
  cnt_label   = module.cnt[1].label
  cnt_labels  = module.cnt[*].label
  web_all     = module.web
  path_module = module.web.path
}
