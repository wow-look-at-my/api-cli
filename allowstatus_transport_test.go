package main

import _ "embed"

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A named transport plus allow-status is a load error, because both cannot
// both hold. The error names both ways out.
func TestValidate_AllowStatusWithANamedTransport(t *testing.T) {
	_, err := loadStr(t, testValidate_AllowStatusWithANamedTransportXml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow-status needs the built-in client")
	assert.Contains(t, err.Error(), `transport="http"`)
}

// transport="http" is the documented way out, and it loads.
func TestValidate_AllowStatusWithTheBuiltinTransport(t *testing.T) {
	_, err := loadStr(t, testValidate_AllowStatusWithTheBuiltinTransportXml)
	assert.NoError(t, err)
}

//go:embed testdata/test-validate-allow-status-with-a-named-transport.xml
var testValidate_AllowStatusWithANamedTransportXml string

//go:embed testdata/test-validate-allow-status-with-the-builtin-transport.xml
var testValidate_AllowStatusWithTheBuiltinTransportXml string
