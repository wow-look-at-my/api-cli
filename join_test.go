package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJoin_Parses(t *testing.T) {
	cfg, err := loadStr(t, `<config name="j"><command name="pull">
		<download over="var.parts" group="{{ .id }}" order="{{ .seq }}">
			<url>https://example.test/<value name="seq"/></url>
			<to><value name="id"/>/<value name="seq"/>.part</to>
			<join to="{{ .id }}.bin" cleanup="true" contiguous="warn"/>
		</download>
	</command></config>`)
	require.NoError(t, err)

	d := cfg.Commands[0].Downloads[0]
	assert.Equal(t, "{{ .id }}", d.Group)
	assert.Equal(t, "{{ .seq }}", d.Order)
	require.NotNil(t, d.Join)
	assert.Equal(t, "{{ .id }}.bin", d.Join.To)
	assert.True(t, d.Join.Cleanup)
	assert.Equal(t, joinGapWarn, d.Join.Contiguous)
}

func TestJoin_RejectsBadGrammar(t *testing.T) {
	cases := []struct {
		name string
		join string
		want string
	}{
		{"no to", `<join cleanup="true"/>`, "<join> requires to="},
		{"bad contiguous", `<join to="out.bin" contiguous="maybe"/>`, "must be \"warn\" or \"error\""},
		{"contiguous without order", `<join to="out.bin" contiguous="error"/>`, "so it needs order="},
		{"bad cleanup", `<join to="out.bin" cleanup="sure"/>`, "cleanup=\"sure\" must be true or false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadStr(t, `<config name="j"><command name="pull">
				<download over="var.parts" group="{{ .id }}">
					<url>https://example.test/x</url>
					<to>p.part</to>
					`+tc.join+`
				</download>
			</command></config>`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestJoin_GroupNeedsAJoin(t *testing.T) {
	_, err := loadStr(t, `<config name="j"><command name="pull">
		<download group="{{ .id }}"><url>https://example.test/x</url><to>p</to></download>
	</command></config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "group= and order= describe a <join>")
}

func TestJoin_PartNeedsItsOwnFile(t *testing.T) {
	_, err := loadStr(t, `<config name="j"><command name="pull">
		<download><url>https://example.test/x</url><join to="out.bin"/></download>
	</command></config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a joined part needs its own <to>")
}

// A join orders by number, so a key that is not one is a load-time answer
// rather than a file concatenated in the wrong order.
func TestJoin_OrderMustBeNumeric(t *testing.T) {
	d := Download{
		URL:   "https://example.test/x",
		To:    "p.part",
		Order: "part-{{ .n }}",
		Join:  &Join{To: "out.bin"},
	}
	_, err := planDownloads([]Download{d}, map[string]any{"n": 2}, ".")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a join orders its parts by number")
}

func TestJoin_PlansGroupAndOrder(t *testing.T) {
	d := Download{
		Over:  "var.parts",
		URL:   "https://example.test/{{ .seq }}",
		To:    "{{ .id }}/{{ .seq }}.part",
		Group: "{{ .id }}",
		Order: "{{ .seq }}",
		Join:  &Join{To: "{{ .id }}.bin", Cleanup: true},
	}
	data := map[string]any{"var": map[string]any{"parts": []any{
		map[string]any{"id": "a", "seq": 10},
		map[string]any{"id": "a", "seq": 2},
		map[string]any{"id": "b", "seq": 1},
	}}}

	specs, err := planDownloads([]Download{d}, data, "/out")
	require.NoError(t, err)
	require.Len(t, specs, 3)
	assert.Equal(t, "a", specs[0].Join.Group)
	assert.Equal(t, float64(10), specs[0].Join.Order)
	assert.Equal(t, "/out/a.bin", specs[0].Join.Dest)
	assert.Equal(t, "/out/b.bin", specs[2].Join.Dest)
	assert.True(t, specs[0].Join.Cleanup)
}

// Without order=, the queue's own order stands in for one.
func TestJoin_OrderDefaultsToQueueOrder(t *testing.T) {
	d := Download{
		Over: "var.parts",
		URL:  "https://example.test/{{ .item }}",
		To:   "{{ .item }}.part",
		Join: &Join{To: "out.bin"},
	}
	data := map[string]any{"var": map[string]any{"parts": []any{"a", "b", "c"}}}
	specs, err := planDownloads([]Download{d}, data, ".")
	require.NoError(t, err)
	require.Len(t, specs, 3)
	for i, spec := range specs {
		assert.False(t, spec.Join.HasOrder)
		assert.Equal(t, float64(i), spec.Join.Order)
	}
}

func TestMissingOrders(t *testing.T) {
	members := func(orders ...float64) []*downloadItem {
		out := make([]*downloadItem, 0, len(orders))
		for _, n := range orders {
			out = append(out, &downloadItem{spec: downloadSpec{Join: &joinPart{Order: n}}})
		}
		return out
	}
	assert.Empty(t, missingOrders(members(1, 2, 3)))
	assert.Equal(t, []string{"3"}, missingOrders(members(2, 4, 5)))
	assert.Equal(t, []string{"2", "3"}, missingOrders(members(1, 4)))
	assert.Empty(t, missingOrders(members(1.5, 2.5)), "a fractional order names no sequence")
}
