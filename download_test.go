package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseXML_Download(t *testing.T) {
	cfg := mustParse(t, `<config name="x">
		<downloads concurrency="8" retries="1" dir="./out" log_lines="6"/>
		<command name="get">
			<download over="result.list.assets" when="{{.flag.save}}">
				<url><value name="browser_download_url"/></url>
				<to><value name="name"/></to>
				<header name="Accept">application/octet-stream</header>
				<cookie name="sid"><value name="var.session"/></cookie>
				<if test="var.token">
					<header name="Authorization">Bearer <value name="var.token"/></header>
				</if>
			</download>
		</command>
	</config>`)

	require.NotNil(t, cfg.Downloads)
	assert.Equal(t, &Downloads{Concurrency: 8, Retries: 1, Dir: "./out", LogLines: 6}, cfg.Downloads)

	require.Len(t, cfg.Commands[0].Downloads, 1)
	d := cfg.Commands[0].Downloads[0]
	assert.Equal(t, "result.list.assets", d.Over)
	assert.Equal(t, "{{.flag.save}}", d.When)
	assert.Equal(t, "{{ .browser_download_url }}", d.URL)
	assert.Equal(t, "{{ .name }}", d.To)
	require.Len(t, d.Headers, 2)
	assert.Equal(t, Header{Name: "Accept", Value: "application/octet-stream"}, d.Headers[0])
	assert.Equal(t, Header{Name: "Authorization", Value: "Bearer {{ .var.token }}", When: "var.token"}, d.Headers[1])
	require.Len(t, d.Cookies, 1)
	assert.Equal(t, Header{Name: "sid", Value: "{{ .var.session }}"}, d.Cookies[0])
}

func TestParseXML_DownloadRejectsBadShapes(t *testing.T) {
	cases := map[string]string{
		"unknown child":    `<download><url>u</url><body>x</body></download>`,
		"unknown attr":     `<download speed="fast"><url>u</url></download>`,
		"if with non-auth": `<download><url>u</url><if test="a"><url>u</url></if></download>`,
	}
	for name, frag := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseConfigXML([]byte(`<config name="x"><command name="c">` + frag + `</command></config>`))
			assert.Error(t, err)
		})
	}
}

func TestParseXML_DownloadsSettingsRejectsBadValues(t *testing.T) {
	_, err := parseConfigXML([]byte(`<config name="x"><downloads concurrency="lots"/><command name="c"><run>x</run></command></config>`))
	assert.ErrorContains(t, err, "must be an integer")

	_, err = parseConfigXML([]byte(`<config name="x"><downloads><url>u</url></downloads><command name="c"><run>x</run></command></config>`))
	assert.ErrorContains(t, err, "takes no children")
}

func TestValidate_DownloadRules(t *testing.T) {
	t.Run("leaf needs no run", func(t *testing.T) {
		_, err := loadStr(t, `<config name="x"><command name="g"><download><url>https://h/f</url></download></command></config>`)
		assert.NoError(t, err)
	})
	t.Run("url required", func(t *testing.T) {
		_, err := loadStr(t, `<config name="x"><command name="g"><download><to>f</to></download></command></config>`)
		assert.ErrorContains(t, err, "requires a <url>")
	})
	t.Run("leaves only", func(t *testing.T) {
		_, err := loadStr(t, `<config name="x"><command name="g"><download><url>u</url></download><command name="c"><run>x</run></command></command></config>`)
		assert.ErrorContains(t, err, "only allowed on leaves")
	})
	t.Run("not with fields", func(t *testing.T) {
		_, err := loadStr(t, `<config name="x"><command name="g"><download><url>u</url></download><fields><field name="A">a</field></fields></command></config>`)
		assert.ErrorContains(t, err, "cannot shape it")
	})
	t.Run("leaf without run or download", func(t *testing.T) {
		_, err := loadStr(t, `<config name="x"><command name="g"></command></config>`)
		assert.ErrorContains(t, err, "no command/request/download")
	})
	t.Run("settings bounds", func(t *testing.T) {
		err := validateDownloadSettings(&Downloads{Concurrency: -2})
		assert.ErrorContains(t, err, "concurrency=-2 must be >= 1")
		assert.NoError(t, validateDownloadSettings(nil))
		assert.NoError(t, validateDownloadSettings(&Downloads{Concurrency: 1, Retries: 0}))
	})
}

// planData is a data context standing in for a leaf that ran its steps.
func planData() map[string]any {
	return map[string]any{
		"arg":  map[string]any{"user": "ada"},
		"flag": map[string]any{"save": true, "archive": false},
		"var":  map[string]any{"token": "t0k", "session": "sess1", "blank": ""},
		"result": map[string]any{
			"list": map[string]any{
				"assets": []any{
					map[string]any{"name": "a.zip", "url": "https://h/a?sig=1"},
					map[string]any{"name": "b.zip", "url": "https://h/b?sig=2"},
				},
				"urls":  []any{"https://h/one", "https://h/two"},
				"empty": []any{},
			},
		},
	}
}

func TestPlanDownloads_SingleURL(t *testing.T) {
	specs, err := planDownloads([]Download{{
		URL: "https://h/{{.arg.user}}.tar",
		To:  "out/{{.arg.user}}.tar",
		Headers: []Header{
			{Name: "Authorization", Value: "Bearer {{.var.token}}"},
			{Name: "X-Skip", Value: "no", When: "var.missing"},
		},
		Cookies: []Header{{Name: "sid", Value: "{{.var.session}}"}},
	}}, planData(), "/tmp/dl")
	require.NoError(t, err)

	require.Len(t, specs, 1)
	assert.Equal(t, "https://h/ada.tar", specs[0].URL)
	assert.Equal(t, filepath.FromSlash("/tmp/dl/out/ada.tar"), specs[0].Dest)
	assert.False(t, specs[0].DestIsDir)
	assert.Equal(t, []renderedHeader{
		{Name: "Authorization", Value: "Bearer t0k"},
		{Name: "Cookie", Value: "sid=sess1"},
	}, specs[0].Headers)
}

func TestPlanDownloads_OverExpandsRecords(t *testing.T) {
	specs, err := planDownloads([]Download{{
		Over: "result.list.assets",
		URL:  "{{.url}}",
		To:   "{{.name}}",
	}}, planData(), "dl")
	require.NoError(t, err)

	require.Len(t, specs, 2)
	assert.Equal(t, "https://h/a?sig=1", specs[0].URL)
	assert.Equal(t, filepath.Join("dl", "a.zip"), specs[0].Dest)
	assert.Equal(t, "https://h/b?sig=2", specs[1].URL)
	assert.Equal(t, filepath.Join("dl", "b.zip"), specs[1].Dest)
}

func TestPlanDownloads_OverListOfStrings(t *testing.T) {
	specs, err := planDownloads([]Download{{
		Over: "result.list.urls",
		URL:  "{{.item}}",
	}}, planData(), "dl")
	require.NoError(t, err)

	require.Len(t, specs, 2)
	assert.Equal(t, "https://h/one", specs[0].URL)
	assert.Equal(t, "dl", specs[0].Dest)
	assert.True(t, specs[0].DestIsDir, "no <to> means the server names the file")
}

func TestPlanDownloads_OverEmptyListPlansNothing(t *testing.T) {
	specs, err := planDownloads([]Download{{Over: "result.list.empty", URL: "{{.item}}"}}, planData(), "dl")
	require.NoError(t, err)
	assert.Empty(t, specs)
}

func TestPlanDownloads_OverMissingPathIsAnError(t *testing.T) {
	_, err := planDownloads([]Download{{Over: "result.list.typo", URL: "{{.item}}"}}, planData(), "dl")
	assert.ErrorContains(t, err, `over="result.list.typo" resolved to nothing`)
}

func TestPlanDownloads_WhenGatesTheDeclaration(t *testing.T) {
	dls := []Download{
		{When: "{{.flag.save}}", URL: "https://h/yes"},
		{When: "{{.flag.archive}}", URL: "https://h/no"},
	}
	specs, err := planDownloads(dls, planData(), ".")
	require.NoError(t, err)
	require.Len(t, specs, 1)
	assert.Equal(t, "https://h/yes", specs[0].URL)
}

func TestPlanDownloads_MultiLineURLBecomesManyDownloads(t *testing.T) {
	specs, err := planDownloads([]Download{{
		URL: "{{range .result.list.urls}}{{.}}\n{{end}}",
		To:  "bundle",
	}}, planData(), "dl")
	require.NoError(t, err)

	require.Len(t, specs, 2)
	assert.Equal(t, "https://h/one", specs[0].URL)
	assert.Equal(t, "https://h/two", specs[1].URL)
	for _, s := range specs {
		assert.Equal(t, filepath.Join("dl", "bundle"), s.Dest)
		assert.True(t, s.DestIsDir, "one <to> serving several URLs is a directory")
	}
}

func TestParseXML_DownloadHash(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="get">
		<download><url>https://h/a</url><hash><value name="sha256"/></hash></download>
		<download><url>https://h/b</url><hash algo="SHA512"><value name="d"/></hash></download>
	</command></config>`)

	assert.Equal(t, "{{ .sha256 }}", cfg.Commands[0].Downloads[0].Hash)
	assert.Equal(t, "sha256", cfg.Commands[0].Downloads[0].HashAlgo, "sha256 is the default")
	assert.Equal(t, "sha512", cfg.Commands[0].Downloads[1].HashAlgo, "algo= is case-insensitive")
}

func TestValidate_DownloadHashAlgo(t *testing.T) {
	_, err := loadStr(t, `<config name="x"><command name="g">
		<download><url>https://h/f</url><hash algo="crc32">abc</hash></download>
	</command></config>`)
	assert.ErrorContains(t, err, `<hash algo="crc32"> must be one of md5|sha1|sha256|sha512`)
}

func TestPlanDownloads_Hash(t *testing.T) {
	data := planData()
	good := strings.Repeat("a1", 32)
	data["result"].(map[string]any)["digest"] = good

	specs, err := planDownloads([]Download{{
		URL: "https://h/f", Hash: "{{.result.digest}}", HashAlgo: "sha256",
	}}, data, ".")
	require.NoError(t, err)
	assert.Equal(t, good, specs[0].Hash)
	assert.Equal(t, "sha256", specs[0].HashAlgo)
}

func TestPlanDownloads_HashNormalization(t *testing.T) {
	data := planData()
	digest := strings.Repeat("AB", 32)
	// The shape `sha256sum` writes: the digest, two spaces, the file name.
	data["result"].(map[string]any)["sumfile"] = "  " + digest + "  archive.tar.gz\n"

	specs, err := planDownloads([]Download{{
		URL: "https://h/f", Hash: "{{.result.sumfile}}", HashAlgo: "sha256",
	}}, data, ".")
	require.NoError(t, err)
	assert.Equal(t, strings.ToLower(digest), specs[0].Hash, "a sha256sum line and mixed case both normalize")
}

func TestPlanDownloads_MalformedHashIsAnError(t *testing.T) {
	// The third case is the one that matters: a renamed manifest field renders
	// as the template engine's placeholder, and must fail loudly rather than
	// quietly leave the file unverified.
	cases := map[string]string{
		"too short":     strings.Repeat("ab", 8),
		"not hex":       strings.Repeat("zz", 32),
		"missing field": "<no value>",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			data := planData()
			data["var"].(map[string]any)["digest"] = value
			_, err := planDownloads([]Download{{
				URL: "https://h/f", Hash: "{{.var.digest}}", HashAlgo: "sha256",
			}}, data, ".")
			assert.ErrorContains(t, err, "is not a 64-character hex digest")
		})
	}

	// And the placeholder really is what a missing field renders as, so the
	// case above is the real one and not a straw man.
	_, err := planDownloads([]Download{{
		URL: "https://h/f", Hash: "{{.result.list.typo}}", HashAlgo: "sha256",
	}}, planData(), ".")
	assert.ErrorContains(t, err, "is not a 64-character hex digest")
}

// A digest is optional per record: an <if> around the <hash> body renders empty
// for a record that has none, and that record simply is not verified.
func TestPlanDownloads_HashCanBeEmptyPerRecord(t *testing.T) {
	data := planData()
	data["result"].(map[string]any)["assets"] = []any{
		map[string]any{"url": "https://h/a", "sum": strings.Repeat("cd", 32)},
		map[string]any{"url": "https://h/b"},
	}

	specs, err := planDownloads([]Download{{
		Over:     "result.assets",
		URL:      "{{.url}}",
		Hash:     "{{ if truthy .sum }}{{ .sum }}{{ end }}",
		HashAlgo: "sha256",
	}}, data, ".")
	require.NoError(t, err)

	require.Len(t, specs, 2)
	assert.Equal(t, strings.Repeat("cd", 32), specs[0].Hash)
	assert.Empty(t, specs[1].Hash)
}

func TestPlanDownloads_ColLidingDestinationsAreAnError(t *testing.T) {
	// The <to> here forgot to vary, so every record would land on one file.
	_, err := planDownloads([]Download{{
		Over: "result.list.assets",
		URL:  "{{.url}}",
		To:   "asset.zip",
	}}, planData(), "dl")
	assert.ErrorContains(t, err, "would both write")

	// A shared directory is not a collision: the server names each file.
	_, err = planDownloads([]Download{{Over: "result.list.assets", URL: "{{.url}}", To: "into/"}}, planData(), "dl")
	assert.NoError(t, err)
}

func TestPlanDownloads_EmptyURLIsAnError(t *testing.T) {
	_, err := planDownloads([]Download{{URL: "{{.var.blank}}"}}, planData(), ".")
	assert.ErrorContains(t, err, "rendered empty")
}

func TestPlanDownloads_NonHTTPURLIsAnError(t *testing.T) {
	// A mistyped path renders to the template engine's placeholder rather than
	// to nothing, so the check has to be on the shape of the URL.
	_, err := planDownloads([]Download{{URL: "https://h/{{.result.list.typo}}/f"}}, planData(), ".")
	assert.NoError(t, err, "a missing leaf inside an otherwise valid URL is the author's business")

	_, err = planDownloads([]Download{{URL: "{{.result.list.typo}}"}}, planData(), ".")
	assert.ErrorContains(t, err, "not an http(s) URL")

	_, err = planDownloads([]Download{{URL: "ftp://h/f"}}, planData(), ".")
	assert.ErrorContains(t, err, "not an http(s) URL")
}

func TestPlanDownloads_ReportsRenderErrors(t *testing.T) {
	_, err := planDownloads([]Download{{URL: "{{"}}, planData(), ".")
	assert.ErrorContains(t, err, "render url")

	_, err = planDownloads([]Download{{URL: "https://h/f", To: "{{"}}, planData(), ".")
	assert.ErrorContains(t, err, "render to")

	_, err = planDownloads([]Download{{When: "{{", URL: "https://h/f"}}, planData(), ".")
	assert.ErrorContains(t, err, "render when")
}

func TestPlanDownloads_AuthorWrittenCookieHeaderMergesWithCookies(t *testing.T) {
	specs, err := planDownloads([]Download{{
		URL:     "https://h/f",
		Headers: []Header{{Name: "Cookie", Value: "consent=1"}},
		Cookies: []Header{{Name: "sid", Value: "{{.var.session}}"}, {Name: "blank", Value: ""}},
	}}, planData(), ".")
	require.NoError(t, err)

	require.Len(t, specs[0].Headers, 1)
	assert.Equal(t, renderedHeader{Name: "Cookie", Value: "consent=1; sid=sess1"}, specs[0].Headers[0])
}

func TestDownloadDest(t *testing.T) {
	existing := t.TempDir()
	abs := filepath.Join(existing, "abs.bin")

	dest, isDir := downloadDest("dl", "", false)
	assert.Equal(t, "dl", dest)
	assert.True(t, isDir)

	dest, isDir = downloadDest("dl", "sub/f.bin", false)
	assert.Equal(t, filepath.Join("dl", "sub", "f.bin"), dest)
	assert.False(t, isDir)

	dest, isDir = downloadDest("dl", "sub/", false)
	assert.Equal(t, filepath.Join("dl", "sub"), dest)
	assert.True(t, isDir, "a trailing slash names a directory")

	dest, isDir = downloadDest("dl", existing, false)
	assert.Equal(t, existing, dest)
	assert.True(t, isDir, "an existing directory names a directory")

	dest, isDir = downloadDest("dl", abs, false)
	assert.Equal(t, abs, dest, "an absolute <to> ignores the download dir")
	assert.False(t, isDir)
}

func TestResolveDownloadSettings(t *testing.T) {
	prev := downloadDefaults
	t.Cleanup(func() { downloadDefaults = prev })

	installDownloads(nil)
	assert.Equal(t, downloadSettings{Concurrency: 4, Retries: 3, Dir: "."}, resolveDownloadSettings(nil))

	installDownloads(&Config{Downloads: &Downloads{Concurrency: 8, Retries: 1, Dir: "out", LogLines: 5}})
	assert.Equal(t, downloadSettings{Concurrency: 8, Retries: 1, Dir: "out", LogLines: 5}, resolveDownloadSettings(nil))

	root := newRoot(nil)
	require.NoError(t, root.PersistentFlags().Set("concurrency", "2"))
	require.NoError(t, root.PersistentFlags().Set("download-dir", "elsewhere"))
	require.NoError(t, root.PersistentFlags().Set("log-lines", "9"))
	require.NoError(t, root.PersistentFlags().Set("no-tui", "true"))
	// newRoot(nil) cleared the registry; put the config settings back so the
	// test proves the flags win over them rather than over the defaults.
	installDownloads(&Config{Downloads: &Downloads{Concurrency: 8, Dir: "out"}})
	assert.Equal(t, downloadSettings{Concurrency: 2, Retries: 3, Dir: "elsewhere", LogLines: 9, NoTUI: true},
		resolveDownloadSettings(root))
}

func TestResolveDownloadSettings_UnsetFlagsLeaveConfigAlone(t *testing.T) {
	prev := downloadDefaults
	t.Cleanup(func() { downloadDefaults = prev })

	root := &cobra.Command{}
	root.PersistentFlags().Int("concurrency", defaultConcurrency, "")
	root.PersistentFlags().String("download-dir", ".", "")
	root.PersistentFlags().Int("log-lines", 0, "")
	root.PersistentFlags().Bool("no-tui", false, "")

	installDownloads(&Config{Downloads: &Downloads{Concurrency: 8, Dir: "out"}})
	assert.Equal(t, downloadSettings{Concurrency: 8, Retries: 3, Dir: "out"}, resolveDownloadSettings(root))
}

func TestSplitLines(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, splitLines("\n a \n\n b \n"))
	assert.Nil(t, splitLines("   \n\n"))
}

func TestDownloadCtx_PromotesRecordKeys(t *testing.T) {
	ctx := downloadCtx(map[string]any{"var": map[string]any{"t": 1}}, map[string]any{"url": "u"})
	assert.Equal(t, "u", ctx["url"])
	assert.Equal(t, map[string]any{"url": "u"}, ctx["item"])
	assert.NotNil(t, ctx["var"])

	scalar := downloadCtx(map[string]any{}, "https://h/x")
	assert.Equal(t, "https://h/x", scalar["item"])
}

func TestDownloadDest_TrailingSeparator(t *testing.T) {
	dest, isDir := downloadDest("dl", "sub"+string(os.PathSeparator), false)
	assert.Equal(t, filepath.Join("dl", "sub"), dest)
	assert.True(t, isDir)
}
