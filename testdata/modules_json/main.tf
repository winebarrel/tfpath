module "remote" {
  source  = "acme/remote/aws"
  version = "1.0.0"
  name    = "from-registry"
}

module "missing" {
  source = "git::https://example.com/missing.git"
}
