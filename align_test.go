package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDisplayWidth_ASCII(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello", 5},
		{"a b c", 5},
		{"\t\n", 0}, // tab and newline are control chars; width 0
	}
	for _, c := range cases {
		assert.Equal(t, c.want, displayWidth(c.in), "input %q", c.in)
	}
}

func TestDisplayWidth_StripsANSI(t *testing.T) {
	// SGR-bracketed text — only the visible glyphs count.
	assert.Equal(t, 5, displayWidth("\x1b[31mhello\x1b[0m"))
	assert.Equal(t, 5, displayWidth("\x1b[1;31;42mhello\x1b[0m"))
	// ANSI alone has zero width.
	assert.Equal(t, 0, displayWidth("\x1b[31m\x1b[0m"))
	// OSC sequence (e.g. setting window title).
	assert.Equal(t, 2, displayWidth("\x1b]0;title\x07OK"))
	// ESC + single intro byte.
	assert.Equal(t, 2, displayWidth("\x1bcOK"))
}

func TestDisplayWidth_CJKWide(t *testing.T) {
	// Each CJK wide char is 2 columns.
	assert.Equal(t, 4, displayWidth("日本"))
	assert.Equal(t, 6, displayWidth("a日b本"))
	// Fullwidth digits.
	assert.Equal(t, 6, displayWidth("１２３"))
}

func TestDisplayWidth_CombiningMarks(t *testing.T) {
	// "é" composed as e + combining acute: width 1, not 2.
	assert.Equal(t, 1, displayWidth("é"))
}

func TestStripANSI_KeepsText(t *testing.T) {
	assert.Equal(t, "hello", stripANSI("\x1b[31mhello\x1b[0m"))
	assert.Equal(t, "ab", stripANSI("\x1b]0;title\x07ab"))
	assert.Equal(t, "no escapes here", stripANSI("no escapes here"))
}

func TestAlignColumns_ASCII(t *testing.T) {
	rows := []string{
		"ID\tNAME\tEMAIL",
		"1\tAda\ta@x",
		"42\tHopper\thopper@example.org",
	}
	out := alignColumns(rows, 2)
	want := "ID  NAME    EMAIL\n" +
		"1   Ada     a@x\n" +
		"42  Hopper  hopper@example.org\n"
	assert.Equal(t, want, out)
}

func TestAlignColumns_WithANSIEscapes(t *testing.T) {
	// Coloured first cell — alignment must be by visible width, not by byte length.
	rows := []string{
		"ID\tNAME",
		"\x1b[31m1\x1b[0m\tAda",
		"42\tHopper",
	}
	out := alignColumns(rows, 2)
	// Display widths: ID=2, NAME=4. After alignment: 2-wide ID col, then NAME.
	want := "ID  NAME\n" +
		"\x1b[31m1\x1b[0m   Ada\n" +
		"42  Hopper\n"
	assert.Equal(t, want, out)
}

func TestAlignColumns_WithCJK(t *testing.T) {
	rows := []string{
		"NAME\tCITY",
		"Ada\t東京",
		"Bob\tNYC",
	}
	out := alignColumns(rows, 2)
	// NAME col width = 4. CITY col width = 4 (東京 = 4 cells).
	want := "NAME  CITY\n" +
		"Ada   東京\n" +
		"Bob   NYC\n"
	assert.Equal(t, want, out)
}

func TestAlignColumns_RaggedRows(t *testing.T) {
	rows := []string{
		"a\tb\tc",
		"x\ty",
		"only-one",
	}
	out := alignColumns(rows, 1)
	want := "a        b c\n" +
		"x        y \n" +
		"only-one   \n"
	assert.Equal(t, want, out)
}

func TestAlignColumns_EmptyInput(t *testing.T) {
	assert.Equal(t, "", alignColumns(nil, 2))
	assert.Equal(t, "", alignColumns([]string{}, 2))
}

func TestPadRight_ASCII(t *testing.T) {
	assert.Equal(t, "ab   ", padRight(5, "ab"))
	assert.Equal(t, "abc", padRight(2, "abc"))
	assert.Equal(t, "", padRight(0, ""))
}

func TestPadLeft_ASCII(t *testing.T) {
	assert.Equal(t, "   ab", padLeft(5, "ab"))
	assert.Equal(t, "abc", padLeft(2, "abc"))
}

func TestPadRight_DisplayWidthAware(t *testing.T) {
	// "日" is 2 cells wide, so pad to 5 means add 3 spaces.
	assert.Equal(t, "日   ", padRight(5, "日"))
	// ANSI: only visible chars count, so pad uses displayWidth("\x1b[31mab\x1b[0m") = 2.
	assert.Equal(t, "\x1b[31mab\x1b[0m   ", padRight(5, "\x1b[31mab\x1b[0m"))
}

func TestPadLeft_DisplayWidthAware(t *testing.T) {
	assert.Equal(t, "   日", padLeft(5, "日"))
}
