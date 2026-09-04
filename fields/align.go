package fields

import (
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

// displayWidth returns the column width of s on a terminal. Escape sequences,
// combining marks and control characters take no columns; runeWidth decides
// the rest.
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

// skipEscape advances past the ANSI escape sequence at s[i], which must be an
// ESC, and returns the index just after it. It knows CSI, OSC, and ESC plus an
// intro byte.
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

// decodeRune reads the rune at the head of s and reports how many bytes it
// took.
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

// alignColumns pads tab-separated rows with spaces so their columns line up by
// displayWidth, leaving `padding` spaces of gutter. A short row gets empty
// trailing cells, and escape sequences pass through.
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

// oneLine folds a value into a single line, so a table row stays a row. A cell
// that keeps its newlines puts every later column at the left margin, and a tab
// opens a column nothing declared. A whitespace run becomes a space.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\n\r\t\v\f") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// indentBlock aligns the continuation lines of a value under its label, so a
// paragraph reads as a value rather than as more records.
func indentBlock(s string, indent int) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	pad := strings.Repeat(" ", indent)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines[1:] {
		lines[i+1] = pad + lines[i+1]
	}
	return strings.Join(lines, "\n")
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
