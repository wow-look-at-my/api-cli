package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/tml"
)

const browseGolden = "testdata/browse-screens.golden"

// TestBrowseScreens draws the browser at each step of a session and compares the screens with the golden file.
// A missing golden is written from this run, and the test then fails, so a broken screen never blesses itself.
func TestBrowseScreens(t *testing.T) {
	h := newDataHarness(t)
	h.m.now = func() time.Time { return time.Time{} }
	h.m.update(h, tml.Resize{Width: 100, Height: 30})
	var screens []string
	shot := func(name string) {
		screens = append(screens, "=== "+name+"\n"+trimScreen(h.m.frame()))
	}

	shot("start")
	listAcme(h)
	shot("repos list acme")
	h.press("a")
	shot("actions menu")
	h.press("esc", "enter")
	shot("link to repos view")
	h.press("backspace", "?")
	shot("help")

	got := strings.Join(screens, "\n")
	want, err := os.ReadFile(browseGolden)
	if os.IsNotExist(err) {
		require.NoError(t, os.WriteFile(browseGolden, []byte(got), 0o644))
		t.Fatalf("wrote %s from this run; read it, then run again", browseGolden)
	}
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// trimScreen drops the trailing blanks of every row, so the golden does not carry the padding of a fixed-size screen.
func trimScreen(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(stripSGR(l), " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

// stripSGR removes colour codes, so the golden reads as plain text.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
