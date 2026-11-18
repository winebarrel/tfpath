package tfpath

// Options configures how a Terraform configuration is loaded.
type Options struct {
	// Dir is the root module directory. Defaults to ".".
	Dir string

	// VarFiles are .tfvars / .tfvars.json files applied after the
	// automatically loaded terraform.tfvars(.json) and *.auto.tfvars(.json)
	// files in Dir, in the given order.
	VarFiles []string

	// Vars sets root module variables like -var on the terraform CLI. They
	// take precedence over VarFiles. A value is used as a string for
	// primitive types and parsed as an HCL expression otherwise.
	Vars map[string]string

	// Workspace is the value of terraform.workspace. Defaults to "default".
	Workspace string
}
