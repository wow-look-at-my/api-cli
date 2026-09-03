package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/api-cli/fields"
	"github.com/wow-look-at-my/go-containers/set"
)

// The README and the XSD ship inside the binary (`api-cli docs`), and both
// restate lists the code owns. Nothing else compares the two, so a new sink or
// a new flag stays undocumented until a user finds the gap. These tests fail
// instead.

// readmeSection returns the body of one "## " section of the README.
func readmeSection(t *testing.T, heading string) string {
	t.Helper()
	i := strings.Index(readmeDoc, heading+"\n")
	require.GreaterOrEqual(t, i, 0, "README has no %q section", heading)
	body := readmeDoc[i+len(heading):]
	if j := strings.Index(body, "\n## "); j >= 0 {
		body = body[:j]
	}
	return body
}

// readmeLine returns the one README line that contains marker.
func readmeLine(t *testing.T, marker string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(readmeDoc, "\n") {
		if strings.Contains(line, marker) {
			found = append(found, line)
		}
	}
	require.Len(t, found, 1, "expected exactly one README line containing %q", marker)
	return found[0]
}

func TestDocs_ReadmeNamesEverySink(t *testing.T) {
	row := readmeLine(t, "| `--as <sink>`")
	forcing := readmeSection(t, "### Forcing a representation")
	for _, sink := range fields.Sinks() {
		assert.Contains(t, row, sink, "the --as row omits the %q sink", sink)
		assert.Contains(t, forcing, sink, "the forcing section omits the %q sink", sink)
	}
}

// The flag's own usage string is the only sink list a user sees without the
// README, so it carries the same obligation.
func TestDocs_AsFlagUsageNamesEverySink(t *testing.T) {
	t.Serial() // newRoot publishes the transport registry and the download settings.
	usage := newRoot(nil).PersistentFlags().Lookup("as").Usage
	for _, sink := range fields.Sinks() {
		assert.Contains(t, usage, sink, "--as usage omits the %q sink", sink)
	}
}

func TestDocs_HashAlgosAreDocumented(t *testing.T) {
	readme := readmeSection(t, "### Checking a download against a digest")
	schema := schemaDoc
	for algo := range hashAlgos {
		assert.Contains(t, readme, algo, "the README omits the %q digest algorithm", algo)
		assert.Contains(t, schema, fmt.Sprintf("<xs:enumeration value=%q/>", algo),
			"the XSD hashAlgo enumeration omits %q", algo)
	}
	assert.Contains(t, readme, defaultHashAlgo+"` (the default)")
}

func TestDocs_ReservedCommandNamesAreDocumented(t *testing.T) {
	row := readmeLine(t, "| `name=` (required) |")
	for _, name := range reservedCommandNames.Values() {
		assert.Contains(t, row, name, "the <command name=> row omits the reserved name %q", name)
	}
}

func TestDocs_DeclaredTypesAreDocumented(t *testing.T) {
	args := readmeLine(t, "`<arg name= type=")
	flags := readmeLine(t, "`<flag name= short= type=")
	formats := readmeLine(t, "`input=` is")
	for _, typ := range validArgTypes.Values() {
		if typ != "" {
			assert.Contains(t, args, typ, "the <arg> line omits the %q type", typ)
		}
	}
	for _, typ := range validFlagTypes.Values() {
		if typ != "" {
			assert.Contains(t, flags, typ, "the <flag> line omits the %q type", typ)
		}
	}
	for _, in := range validFormatInputs.Values() {
		if in != "" {
			assert.Contains(t, formats, in, "the <format input=> line omits %q", in)
		}
	}
}

// documentedNonPersistent are flags the README's table lists that cobra owns
// rather than newRoot: --version comes from root.Version.
var documentedNonPersistent = set.Of("version")

func TestDocs_GlobalFlagsTableMatchesTheRoot(t *testing.T) {
	t.Serial() // newRoot publishes the transport registry and the download settings.
	table := readmeSection(t, "## Global flags")
	documented := set.New[string]()
	rowFlag := regexp.MustCompile("^\\| `--([a-z-]+)")
	for _, line := range strings.Split(table, "\n") {
		if m := rowFlag.FindStringSubmatch(line); m != nil {
			documented.Add(m[1])
		}
	}
	require.False(t, documented.IsEmpty(), "the Global flags table lists no flags")

	newRoot(nil).PersistentFlags().VisitAll(func(f *pflag.Flag) {
		assert.True(t, documented.Contains(f.Name), "global flag --%s has no row in the README", f.Name)
	})
	root := newRoot(nil).PersistentFlags()
	for _, name := range documented.Values() {
		if documentedNonPersistent.Contains(name) {
			continue
		}
		assert.NotNil(t, root.Lookup(name), "the README documents --%s, which the root does not register", name)
	}
}
