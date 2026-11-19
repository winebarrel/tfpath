package tfpath

import (
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"golang.org/x/crypto/ssh"
	"golang.org/x/text/encoding/ianaindex"
)

var uuidV5Func = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "namespace", Type: cty.String},
		{Name: "name", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		var ns uuid.UUID

		switch s := args[0].AsString(); s {
		case "dns":
			ns = uuid.NameSpaceDNS
		case "oid":
			ns = uuid.NameSpaceOID
		case "url":
			ns = uuid.NameSpaceURL
		case "x500":
			ns = uuid.NameSpaceX500
		default:
			var err error
			if ns, err = uuid.Parse(s); err != nil {
				return cty.UnknownVal(cty.String), fmt.Errorf("uuidv5() doesn't support namespace %s (%w)", s, err)
			}
		}

		return cty.StringVal(uuid.NewSHA1(ns, []byte(args[1].AsString())).String()), nil
	},
})

var rsaDecryptFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "ciphertext", Type: cty.String},
		{Name: "privatekey", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		b, err := base64.StdEncoding.DecodeString(args[0].AsString())
		if err != nil {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(0, "failed to decode input: cipher text must be base64-encoded")
		}

		raw, err := ssh.ParseRawPrivateKey([]byte(args[1].AsString()))
		if err != nil {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(1, "invalid private key: %s", err)
		}

		key, ok := raw.(*rsa.PrivateKey)
		if !ok {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(1, "invalid private key type %T", raw)
		}

		// Terraform's rsadecrypt is defined as PKCS #1 v1.5 (e.g. for EC2
		// Windows passwords).
		out, err := rsa.DecryptPKCS1v15(nil, key, b) //nolint:staticcheck
		if err != nil {
			return cty.UnknownVal(cty.String), fmt.Errorf("failed to decrypt: %w", err)
		}

		return cty.StringVal(string(out)), nil
	},
})

var textEncodeBase64Func = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "string", Type: cty.String},
		{Name: "encoding", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		enc, err := ianaindex.IANA.Encoding(args[1].AsString())
		if err != nil || enc == nil {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(1, "%q is not a supported IANA encoding name or alias", args[1].AsString())
		}

		b, err := enc.NewEncoder().Bytes([]byte(args[0].AsString()))
		if err != nil {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(0, "the given string contains characters that cannot be represented in %s", args[1].AsString())
		}

		return cty.StringVal(base64.StdEncoding.EncodeToString(b)), nil
	},
})

var textDecodeBase64Func = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "source", Type: cty.String},
		{Name: "encoding", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		enc, err := ianaindex.IANA.Encoding(args[1].AsString())
		if err != nil || enc == nil {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(1, "%q is not a supported IANA encoding name or alias", args[1].AsString())
		}

		b, err := base64.StdEncoding.DecodeString(args[0].AsString())
		if err != nil {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(0, "the given value is not a valid base64 string")
		}

		out, err := enc.NewDecoder().Bytes(b)
		if err != nil {
			return cty.UnknownVal(cty.String), function.NewArgErrorf(0, "the given string contains symbols that are not defined for %s", args[1].AsString())
		}

		return cty.StringVal(string(out)), nil
	},
})

var timeCmpFunc = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "timestamp_a", Type: cty.String},
		{Name: "timestamp_b", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.Number),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		var ts [2]time.Time
		for i := range ts {
			t, err := time.Parse(time.RFC3339, args[i].AsString())
			if err != nil {
				return cty.UnknownVal(cty.Number), function.NewArgError(i, err)
			}
			ts[i] = t
		}

		return cty.NumberIntVal(int64(ts[0].Compare(ts[1]))), nil
	},
})
