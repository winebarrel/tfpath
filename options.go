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

	// DataFiles are JSON files giving values of data sources, applied in
	// the given order. Each key is a data source address, optionally
	// followed by an attribute path, such as "data.aws_region.current" or
	// "module.*.data.aws_caller_identity.current.account_id". Module paths
	// can use "*" and "**" like queries.
	DataFiles []string

	// Data sets values of data sources like DataFiles. The values are
	// strings, and take precedence over DataFiles.
	Data map[string]string

	// Workspace is the value of terraform.workspace. Defaults to "default".
	Workspace string
}
