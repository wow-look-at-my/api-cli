package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A named transport plus allow-status is a load error, because the two cannot
// both hold. The error names the two ways out.
func TestValidate_AllowStatusWithANamedTransport(t *testing.T) {
	_, err := loadStr(t, `<config name="x">
	<transports><transport name="corp"><run>corp-http</run></transport></transports>
	<command name="c"><run>
		<request transport="corp" allow-status="404"><url>https://e/x</url></request>
	</run></command>
</config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow-status needs the built-in client")
	assert.Contains(t, err.Error(), `transport="http"`)
}

// transport="http" is the documented way out, and it loads.
func TestValidate_AllowStatusWithTheBuiltinTransport(t *testing.T) {
	_, err := loadStr(t, `<config name="x">
	<transports><transport name="corp" default="true"><run>corp-http</run></transport></transports>
	<command name="c"><run>
		<request transport="http" allow-status="404"><url>https://e/x</url></request>
	</run></command>
</config>`)
	assert.NoError(t, err)
}
