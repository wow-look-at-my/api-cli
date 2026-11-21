// Opening a record's web page from the browser.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"github.com/wow-look-at-my/api-cli/fields"
)

// preferredURLKeys are the fields that hold a record's page for a person, ahead of any other URL it carries.
var preferredURLKeys = []string{"html_url", "web_url", "url", "link", "href"}

// maxURLActions caps the URL fields one record offers, so an API that links everything does not flood the menu.
const maxURLActions = 3

// recordURLs is the record's fields that hold a web address, the page fields first.
func recordURLs(rec map[string]any) []string {
	var keys []string
	for _, k := range fields.SortedKeys(rec) {
		if s, ok := rec[k].(string); ok && (strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")) {
			keys = append(keys, k)
		}
	}
	rank := func(k string) int {
		if i := slices.Index(preferredURLKeys, k); i >= 0 {
			return i
		}
		return len(preferredURLKeys)
	}
	slices.SortStableFunc(keys, func(a, b string) int { return rank(a) - rank(b) })
	if len(keys) > maxURLActions {
		keys = keys[:maxURLActions]
	}
	return keys
}

// openURL hands a web address to the system's browser. A test replaces it.
var openURL = func(url string) error {
	name, args := opener()
	if name == "" {
		return fmt.Errorf("no program opens a URL here; set BROWSER")
	}
	cmd := exec.Command(name, append(args, url)...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	// The browser outlives the call. Wait in the background, so the child does not linger as a zombie.
	go func() { _ = cmd.Wait() }()
	return nil
}

// opener is the program that opens a URL: $BROWSER when it is set, then the platform's own.
func opener() (string, []string) {
	if b := strings.TrimSpace(os.Getenv("BROWSER")); b != "" {
		return b, nil
	}
	switch runtime.GOOS {
	case "darwin":
		return "open", nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler"}
	}
	if path, err := exec.LookPath("xdg-open"); err == nil {
		return path, nil
	}
	return "", nil
}

// open sends one field of the current record to the browser, and says on the status line what happened.
func (m *browseModel) open(key, url string) {
	if err := openURL(url); err != nil {
		m.setStatus("could not open "+key+": "+err.Error(), "bad")
		return
	}
	m.setStatus("opened "+url, "ok")
}
