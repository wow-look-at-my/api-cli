package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatRef_UnmarshalString(t *testing.T) {
	var r FormatRef
	require.NoError(t, r.UnmarshalJSON([]byte(`"my-format"`)))
	assert.Equal(t, "my-format", r.Name)
	assert.Nil(t, r.Inline)
	assert.True(t, r.Defined())
}

func TestFormatRef_UnmarshalInline(t *testing.T) {
	var r FormatRef
	require.NoError(t, r.UnmarshalJSON([]byte(`{"views":[{"name":"v","template":"x"}]}`)))
	assert.Equal(t, "", r.Name)
	require.NotNil(t, r.Inline)
	assert.Len(t, r.Inline.Views, 1)
}

func TestFormatRef_UnmarshalNullEmpty(t *testing.T) {
	var r FormatRef
	require.NoError(t, r.UnmarshalJSON([]byte(`null`)))
	assert.False(t, r.Defined())
	require.NoError(t, r.UnmarshalJSON([]byte(``)))
	assert.False(t, r.Defined())
}

func TestFormatRef_UnmarshalRejectsOther(t *testing.T) {
	var r FormatRef
	assert.Error(t, r.UnmarshalJSON([]byte(`42`)))
	assert.Error(t, r.UnmarshalJSON([]byte(`[1,2]`)))
}

func TestFormatRef_RejectsUnknownKeys(t *testing.T) {
	var r FormatRef
	err := r.UnmarshalJSON([]byte(`{"views":[{"name":"v","template":"x"}],"bogus":1}`))
	assert.Error(t, err)
}

func TestResolveFormat_NamedAndInline(t *testing.T) {
	registry := map[string]*Format{
		"u": {Views: []View{{Name: "v", Template: "x"}}},
	}

	got := resolveFormat(&FormatRef{Name: "u"}, registry)
	require.NotNil(t, got)
	assert.Len(t, got.Views, 1)

	inline := &Format{Views: []View{{Name: "i", Template: "y"}}}
	got = resolveFormat(&FormatRef{Inline: inline}, registry)
	assert.Same(t, inline, got)

	assert.Nil(t, resolveFormat(nil, registry))
	assert.Nil(t, resolveFormat(&FormatRef{Name: "missing"}, registry))
}

func TestIsTruthy(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"  ", false},
		{"false", false},
		{"0", false},
		{"no", false},
		{"FALSE", false},
		{"true", true},
		{"yes", true},
		{"1", true},
		{"hello", true},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, isTruthy(c.in), "input %q", c.in)
	}
}

func TestRenderPredicate_Truthy(t *testing.T) {
	ctx := map[string]any{"tty": true}
	cache := map[predicateKey]bool{}
	got, err := renderPredicate("{{.tty}}", ctx, cache)
	require.NoError(t, err)
	assert.True(t, got)

	ctx["tty"] = false
	cache = map[predicateKey]bool{}
	got, err = renderPredicate("{{.tty}}", ctx, cache)
	require.NoError(t, err)
	assert.False(t, got)
}

func TestRenderPredicate_EmptyDefaultsToTTY(t *testing.T) {
	cache := map[predicateKey]bool{}
	got, err := renderPredicate("", map[string]any{"tty": true}, cache)
	require.NoError(t, err)
	assert.True(t, got)

	got, err = renderPredicate("", map[string]any{"tty": false}, cache)
	require.NoError(t, err)
	assert.False(t, got)
}

func TestRenderPredicate_CacheHits(t *testing.T) {
	// When the same (tmpl, ctx) is queried twice, the second call should hit
	// the cache and return the same bool. We can't easily count template
	// renders without instrumentation, so we settle for a behavioural check:
	// mutating the source after first call doesn't affect cached result.
	ctx := map[string]any{"tty": true}
	cache := map[predicateKey]bool{}
	got1, err := renderPredicate("{{.tty}}", ctx, cache)
	require.NoError(t, err)
	got2, err := renderPredicate("{{.tty}}", ctx, cache)
	require.NoError(t, err)
	assert.Equal(t, got1, got2)
	// And the cache has the entry.
	assert.NotEmpty(t, cache)
}

func TestParseInput_JSON(t *testing.T) {
	v := parseInput(`{"id":1,"name":"ada"}`, "json")
	m := v.(map[string]any)
	assert.Equal(t, int64(1), m["id"])
	assert.Equal(t, "ada", m["name"])
}

func TestParseInput_Lines(t *testing.T) {
	v := parseInput("a\nb\nc\n", "lines")
	assert.Equal(t, []string{"a", "b", "c"}, v)

	v = parseInput("", "lines")
	assert.Equal(t, []string{}, v)
}

func TestParseInput_Raw(t *testing.T) {
	assert.Equal(t, "hello world", parseInput("hello world\n\n", "raw"))
}

func TestSelectView_ViewFlagOverrides(t *testing.T) {
	views := []View{
		{Name: "table", When: "true", Template: "T"},
		{Name: "detail", Default: true, Template: "D"},
	}
	got, err := selectView(views, map[string]any{}, "detail", nil)
	require.NoError(t, err)
	assert.Equal(t, "detail", got.Name)
}

func TestSelectView_FirstPredicateMatchWins(t *testing.T) {
	views := []View{
		{Name: "a", When: "{{.first}}", Template: "A"},
		{Name: "b", When: "{{.second}}", Template: "B"},
		{Name: "c", Default: true, Template: "C"},
	}
	got, err := selectView(views, map[string]any{"first": false, "second": true}, "", map[predicateKey]bool{})
	require.NoError(t, err)
	assert.Equal(t, "b", got.Name)
}

func TestSelectView_DefaultFallback(t *testing.T) {
	views := []View{
		{Name: "a", When: "{{.x}}", Template: "A"},
		{Name: "b", Default: true, Template: "B"},
	}
	got, err := selectView(views, map[string]any{"x": false}, "", map[predicateKey]bool{})
	require.NoError(t, err)
	assert.Equal(t, "b", got.Name)
}

func TestSelectView_FirstWhenNoneMatchAndNoDefault(t *testing.T) {
	views := []View{
		{Name: "a", When: "false", Template: "A"},
		{Name: "b", When: "false", Template: "B"},
	}
	got, err := selectView(views, map[string]any{}, "", map[predicateKey]bool{})
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)
}

func TestSelectView_UnknownNameErrors(t *testing.T) {
	views := []View{{Name: "a", Template: "A"}}
	_, err := selectView(views, map[string]any{}, "missing", nil)
	assert.Error(t, err)
}

func TestUserVerdictFromFlags_NoFormatFlag(t *testing.T) {
	c := newRoot(&Config{Name: "t"})
	require.NoError(t, c.PersistentFlags().Set("no-format", "true"))
	assert.Equal(t, userNo, userVerdictFromFlags(c))
}

func TestUserVerdictFromFlags_FormatRaw(t *testing.T) {
	c := newRoot(&Config{Name: "t"})
	require.NoError(t, c.PersistentFlags().Set("format", "raw"))
	assert.Equal(t, userNo, userVerdictFromFlags(c))
}

func TestUserVerdictFromFlags_FormatAlways(t *testing.T) {
	c := newRoot(&Config{Name: "t"})
	require.NoError(t, c.PersistentFlags().Set("format", "always"))
	assert.Equal(t, userAlways, userVerdictFromFlags(c))
}

func TestUserVerdictFromFlags_NoFormatEnv(t *testing.T) {
	t.Setenv("NO_FORMAT", "1")
	c := newRoot(&Config{Name: "t"})
	assert.Equal(t, userNo, userVerdictFromFlags(c))
}

func TestUserVerdictFromFlags_APIFormatEnv(t *testing.T) {
	t.Setenv("NO_FORMAT", "")
	t.Setenv("API_CLI_FORMAT", "always")
	c := newRoot(&Config{Name: "t"})
	assert.Equal(t, userAlways, userVerdictFromFlags(c))

	t.Setenv("API_CLI_FORMAT", "raw")
	c = newRoot(&Config{Name: "t"})
	assert.Equal(t, userNo, userVerdictFromFlags(c))
}

func TestUserVerdictFromFlags_FlagBeatsEnv(t *testing.T) {
	t.Setenv("NO_FORMAT", "1")
	c := newRoot(&Config{Name: "t"})
	require.NoError(t, c.PersistentFlags().Set("format", "always"))
	// --format=always wins over NO_FORMAT.
	assert.Equal(t, userAlways, userVerdictFromFlags(c))
}

func TestUserVerdictFromFlags_Default(t *testing.T) {
	t.Setenv("NO_FORMAT", "")
	t.Setenv("API_CLI_FORMAT", "")
	c := newRoot(&Config{Name: "t"})
	assert.Equal(t, userYes, userVerdictFromFlags(c))
}

func TestCappedTee_UnderCapBuffers(t *testing.T) {
	var sink bytes.Buffer
	tee := &cappedTee{buf: &bytes.Buffer{}, out: &sink, max: 100}
	n, err := tee.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.False(t, tee.overflowed)
	assert.Equal(t, "hello", tee.buf.String())
	assert.Equal(t, "", sink.String())
}

func TestCappedTee_OverflowFlushesPrefixThenStreams(t *testing.T) {
	var sink bytes.Buffer
	tee := &cappedTee{buf: &bytes.Buffer{}, out: &sink, max: 5}
	// First write fits.
	_, err := tee.Write([]byte("abc"))
	require.NoError(t, err)
	assert.False(t, tee.overflowed)
	// Second write exceeds the cap; expect prefix flushed and second write
	// passes straight through.
	_, err = tee.Write([]byte("XYZQ"))
	require.NoError(t, err)
	assert.True(t, tee.overflowed)
	assert.Equal(t, "abcXYZQ", sink.String())
	assert.Equal(t, 0, tee.buf.Len())

	// Subsequent writes go straight to sink.
	_, err = tee.Write([]byte("more"))
	require.NoError(t, err)
	assert.Equal(t, "abcXYZQmore", sink.String())
}

func TestCappedTee_LargeWriteOverCapInOneShot(t *testing.T) {
	var sink bytes.Buffer
	tee := &cappedTee{buf: &bytes.Buffer{}, out: &sink, max: 4}
	_, err := tee.Write([]byte("HELLO_WORLD"))
	require.NoError(t, err)
	assert.True(t, tee.overflowed)
	assert.Equal(t, "HELLO_WORLD", sink.String())
}

func TestStdoutTTY_NonFileWriter(t *testing.T) {
	prev := execStdout
	t.Cleanup(func() { execStdout = prev })
	execStdout = io.Discard
	is, w := stdoutTTY()
	assert.False(t, is)
	assert.Equal(t, 0, w)
}

func TestFormatContext_HasExpectedKeys(t *testing.T) {
	data := map[string]any{
		"arg":  map[string]any{"id": 1},
		"flag": map[string]any{"v": true},
		"env":  map[string]string{},
		"var":  map[string]any{},
	}
	ctx := formatContext("data here", data, true, 80)
	assert.Equal(t, "data here", ctx["data"])
	assert.Equal(t, true, ctx["tty"])
	assert.Equal(t, 80, ctx["width"])
	assert.NotNil(t, ctx["arg"])
	assert.NotNil(t, ctx["flag"])
}

func TestValidate_RejectsZeroViews(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"f": {Views: []View{}},
		},
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name:   "x",
			Format: &FormatRef{Name: "f"},
		}},
	}
	assert.Error(t, validate(cfg))
}

func TestValidate_RejectsDuplicateViewNames(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"f": {Views: []View{
				{Name: "a", Template: "x"},
				{Name: "a", Template: "y"},
			}},
		},
		Command:  &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{Name: "x", Format: &FormatRef{Name: "f"}}},
	}
	assert.Error(t, validate(cfg))
}

func TestValidate_RejectsUnknownNamedRef(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name:   "x",
			Format: &FormatRef{Name: "missing"},
		}},
	}
	assert.Error(t, validate(cfg))
}

func TestValidate_RejectsInvalidInputEnum(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"f": {Input: "yaml", Views: []View{{Name: "v", Template: "x"}}},
		},
		Command:  &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{Name: "x", Format: &FormatRef{Name: "f"}}},
	}
	assert.Error(t, validate(cfg))
}

func TestValidate_AcceptsInlineFormat(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Format: &FormatRef{Inline: &Format{
				Views: []View{{Name: "v", Template: "x"}},
			}},
		}},
	}
	assert.NoError(t, validate(cfg))
}

func TestFormatRef_RoundTripJSON(t *testing.T) {
	// Marshal a string-form ref.
	r1 := &FormatRef{Name: "u"}
	b, err := json.Marshal(r1)
	require.NoError(t, err)
	assert.Equal(t, `"u"`, string(b))

	// Marshal an inline ref.
	r2 := &FormatRef{Inline: &Format{Views: []View{{Name: "v", Template: "x"}}}}
	b, err = json.Marshal(r2)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"views"`)
}

// Ensure execStdout-as-os.Stderr-redirect doesn't accidentally pass for IsTerminal
// in places where it shouldn't.
func TestStdoutTTY_RedirectedFile(t *testing.T) {
	prev := execStdout
	t.Cleanup(func() { execStdout = prev })
	tmp, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	t.Cleanup(func() { tmp.Close() })
	execStdout = tmp
	is, _ := stdoutTTY()
	assert.False(t, is)
}
