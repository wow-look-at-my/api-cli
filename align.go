package main

import (
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

// displayWidth returns the visual column width of s on a terminal.
//
//   - ANSI CSI / OSC / single-byte escape sequences contribute 0.
//   - East Asian Wide and Fullwidth runes contribute 2.
//   - Combining marks (Mn, Me) and format chars (Cf) contribute 0.
//   - C0 / C1 control chars contribute 0.
//   - Other printable runes contribute 1.
func displayWidth(s string) int {
	w := 0
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		r, size := decodeRune(s[i:])
		i += size
		w += runeWidth(r)
	}
	return w
}

// stripANSI returns s with all ANSI escape sequences removed.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// runeWidth returns the column count of a single rune.
func runeWidth(r rune) int {
	if r < 0x20 || r == 0x7f {
		return 0
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// skipEscape advances past an ANSI escape sequence beginning at s[i] (which
// must be ESC = 0x1b). Returns the index of the first byte after the
// sequence. Recognises CSI, OSC, and single-byte ESC + intro.
func skipEscape(s string, i int) int {
	// i points at ESC.
	if i+1 >= len(s) {
		return i + 1
	}
	switch s[i+1] {
	case '[':
		// CSI: ESC [ params final-byte (final in 0x40..0x7e).
		j := i + 2
		for j < len(s) {
			c := s[j]
			if c >= 0x40 && c <= 0x7e {
				return j + 1
			}
			j++
		}
		return j
	case ']':
		// OSC: ESC ] params terminator (BEL = 0x07, or ST = ESC \).
		j := i + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	default:
		// ESC + single intro byte (e.g. ESC =, ESC c, ESC M, etc.).
		return i + 2
	}
}

// decodeRune is a tiny UTF-8 decoder that returns (r, byteLen). Avoids the
// allocation overhead of utf8.DecodeRuneInString for large inputs and keeps
// this file self-contained against std behaviour changes.
func decodeRune(s string) (rune, int) {
	if len(s) == 0 {
		return 0, 0
	}
	b0 := s[0]
	switch {
	case b0 < 0x80:
		return rune(b0), 1
	case b0 < 0xc0:
		return 0xfffd, 1
	case b0 < 0xe0:
		if len(s) < 2 {
			return 0xfffd, 1
		}
		return (rune(b0&0x1f) << 6) | rune(s[1]&0x3f), 2
	case b0 < 0xf0:
		if len(s) < 3 {
			return 0xfffd, 1
		}
		return (rune(b0&0x0f) << 12) | (rune(s[1]&0x3f) << 6) | rune(s[2]&0x3f), 3
	default:
		if len(s) < 4 {
			return 0xfffd, 1
		}
		return (rune(b0&0x07) << 18) | (rune(s[1]&0x3f) << 12) | (rune(s[2]&0x3f) << 6) | rune(s[3]&0x3f), 4
	}
}

// alignColumns formats a slice of tab-separated rows so columns align by
// displayWidth. ASCII spaces pad to the maximum width per column, plus
// `padding` extra spaces between columns (minimum 1). Each row terminates with
// a newline. ANSI escape sequences pass through verbatim.
//
// Rows with fewer cells than the max are padded with empty trailing cells.
// Trailing newlines on input rows are stripped.
func alignColumns(rows []string, padding int) string {
	if padding < 1 {
		padding = 1
	}
	if len(rows) == 0 {
		return ""
	}
	cells := make([][]string, len(rows))
	maxCols := 0
	for i, r := range rows {
		r = strings.TrimRight(r, "\n")
		c := strings.Split(r, "\t")
		cells[i] = c
		if len(c) > maxCols {
			maxCols = len(c)
		}
	}

	widths := make([]int, maxCols)
	for _, row := range cells {
		for ci, cell := range row {
			if w := displayWidth(cell); w > widths[ci] {
				widths[ci] = w
			}
		}
	}

	var b strings.Builder
	for _, row := range cells {
		for ci := 0; ci < maxCols; ci++ {
			cell := ""
			if ci < len(row) {
				cell = row[ci]
			}
			b.WriteString(cell)
			if ci == maxCols-1 {
				continue
			}
			pad := widths[ci] - displayWidth(cell) + padding
			for j := 0; j < pad; j++ {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// padRight returns s padded with spaces on the right to reach displayWidth n.
// If s is already wider, it is returned unchanged.
func padRight(n int, s string) string {
	w := displayWidth(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// padLeft returns s padded with spaces on the left to reach displayWidth n.
// If s is already wider, it is returned unchanged.
func padLeft(n int, s string) string {
	w := displayWidth(s)
	if w >= n {
		return s
	}
	return strings.Repeat(" ", n-w) + s
}
