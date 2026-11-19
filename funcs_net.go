package tfpath

import (
	"math/big"
	"net"

	"github.com/apparentlymart/go-cidr/cidr"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/gocty"
)

func parseCIDR(v cty.Value) (*net.IPNet, error) {
	_, network, err := net.ParseCIDR(v.AsString())
	if err != nil {
		return nil, function.NewArgErrorf(0, "invalid CIDR expression: %s", err)
	}
	return network, nil
}

var cidrHostFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "prefix", Type: cty.String},
		{Name: "hostnum", Type: cty.Number},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		var hostNum *big.Int
		if err := gocty.FromCtyValue(args[1], &hostNum); err != nil {
			return cty.UnknownVal(cty.String), function.NewArgError(1, err)
		}

		network, err := parseCIDR(args[0])
		if err != nil {
			return cty.UnknownVal(cty.String), err
		}

		ip, err := cidr.HostBig(network, hostNum)
		if err != nil {
			return cty.UnknownVal(cty.String), function.NewArgError(1, err)
		}

		return cty.StringVal(ip.String()), nil
	},
})

var cidrNetmaskFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "prefix", Type: cty.String}},
	Type:   function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		network, err := parseCIDR(args[0])
		if err != nil {
			return cty.UnknownVal(cty.String), err
		}

		if network.IP.To4() == nil {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(0, "IPv6 addresses cannot have a netmask: %s", args[0].AsString())
		}

		return cty.StringVal(net.IP(network.Mask).String()), nil
	},
})

var cidrSubnetFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "prefix", Type: cty.String},
		{Name: "newbits", Type: cty.Number},
		{Name: "netnum", Type: cty.Number},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		var newbits int
		if err := gocty.FromCtyValue(args[1], &newbits); err != nil {
			return cty.UnknownVal(cty.String), function.NewArgError(1, err)
		}

		var netnum *big.Int
		if err := gocty.FromCtyValue(args[2], &netnum); err != nil {
			return cty.UnknownVal(cty.String), function.NewArgError(2, err)
		}

		network, err := parseCIDR(args[0])
		if err != nil {
			return cty.UnknownVal(cty.String), err
		}

		subnet, err := cidr.SubnetBig(network, newbits, netnum)
		if err != nil {
			return cty.UnknownVal(cty.String), err
		}

		return cty.StringVal(subnet.String()), nil
	},
})

var cidrSubnetsFunc = function.New(&function.Spec{
	Params:   []function.Parameter{{Name: "prefix", Type: cty.String}},
	VarParam: &function.Parameter{Name: "newbits", Type: cty.Number},
	Type:     function.StaticReturnType(cty.List(cty.String)),
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		network, err := parseCIDR(args[0])
		if err != nil {
			return cty.UnknownVal(retType), err
		}

		start, _ := network.Mask.Size()
		if len(args) == 1 {
			return cty.ListValEmpty(cty.String), nil
		}

		vals := make([]cty.Value, 0, len(args)-1)
		current, _ := cidr.PreviousSubnet(network, start)

		for i, arg := range args[1:] {
			var n int
			if err := gocty.FromCtyValue(arg, &n); err != nil {
				return cty.UnknownVal(retType), function.NewArgError(i+1, err)
			}

			if n < 1 {
				return cty.UnknownVal(retType), function.NewArgErrorf(i+1, "must extend prefix by at least one bit")
			}

			if n > 32 {
				return cty.UnknownVal(retType), function.NewArgErrorf(i+1, "may not extend prefix by more than 32 bits")
			}

			length := start + n
			if length > len(network.IP)*8 {
				return cty.UnknownVal(retType), function.NewArgErrorf(i+1, "would extend prefix to %d bits, which is too long for this address", length)
			}

			next, rollover := cidr.NextSubnet(current, length)
			if rollover || !network.Contains(next.IP) {
				return cty.UnknownVal(retType), function.NewArgErrorf(i+1, "not enough remaining address space for a subnet with a prefix of %d bits after %s", length, current)
			}

			current = next
			vals = append(vals, cty.StringVal(current.String()))
		}

		return cty.ListVal(vals), nil
	},
})
