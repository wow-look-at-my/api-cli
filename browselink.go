// Links from a record of one result to another command: declared ones, and the ones the browser infers from names.

package main

import (
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/wow-look-at-my/api-cli/fields"
	"github.com/wow-look-at-my/go-containers/set"
)

// browseLink opens target with values filled in from a record.
type browseLink struct {
	label    string
	target   *browseEntry
	values   map[string]string
	explicit bool
	// fromRecord counts the inputs the record itself filled.
	fromRecord int
	missing    []string
}

// maxInferredLinks caps the links the browser offers on its own, so a large tree does not bury the declared ones.
const maxInferredLinks = 8

// linksFor lists what a record of src's result can open. carried holds the values the user already typed, by input name.
func (m *browseModel) linksFor(src *browseEntry, rec map[string]any, carried map[string]string) []browseLink {
	if rec == nil {
		return nil
	}
	var out []browseLink
	declared := set.New[*browseEntry]()
	for _, l := range src.hints.Links {
		target := m.byPath[l.To]
		if target == nil {
			continue
		}
		declared.Add(target)
		link := browseLink{label: l.Label, target: target, explicit: true, values: map[string]string{}}
		for _, w := range l.With {
			if v, ok := withValue(w, rec, carried); ok {
				link.values[w.Name] = v
				link.fromRecord++
			}
		}
		fillLink(&link, src, rec, carried)
		out = append(out, link)
	}

	var inferred []browseLink
	for _, target := range m.entries {
		if target == src || target.hidden || declared.Contains(target) || !target.runnable() || len(target.fields) == 0 {
			continue
		}
		link := browseLink{target: target, values: map[string]string{}}
		fillLink(&link, src, rec, carried)
		if link.fromRecord == 0 || len(link.missing) > 0 {
			continue
		}
		inferred = append(inferred, link)
	}
	sort.SliceStable(inferred, func(i, j int) bool {
		si, sj := sibling(src, inferred[i].target), sibling(src, inferred[j].target)
		if si != sj {
			return si
		}
		return inferred[i].fromRecord > inferred[j].fromRecord
	})
	if len(inferred) > maxInferredLinks {
		inferred = inferred[:maxInferredLinks]
	}
	for i := range out {
		if out[i].label == "" {
			out[i].label = out[i].target.pathString()
		}
	}
	for i := range inferred {
		inferred[i].label = inferred[i].target.pathString()
	}
	return append(out, inferred...)
}

// fillLink completes a link's values. An arg, and a required flag, take a record field of the same name.
// A row of a listing named for the plural of an arg is one of those things, so that arg takes the row's identity.
// Any input still empty takes the value the user typed for that name elsewhere.
func fillLink(link *browseLink, src *browseEntry, rec map[string]any, carried map[string]string) {
	for i := range link.target.fields {
		f := &link.target.fields[i]
		name := f.name()
		if name == "" || f.isBool() {
			continue
		}
		if _, ok := link.values[name]; ok {
			continue
		}
		if f.arg != nil || f.required {
			if v, ok := recordValue(rec, name); ok {
				link.values[name] = v
				link.fromRecord++
				continue
			}
		}
		if f.arg != nil && pluralOf(src.node.Name, name) {
			if v, ok := identityValue(rec); ok {
				link.values[name] = v
				link.fromRecord++
				continue
			}
		}
		if v, ok := carried[name]; ok {
			link.values[name] = v
		}
	}
	for i := range link.target.fields {
		f := &link.target.fields[i]
		if f.required && !f.isBool() && strings.TrimSpace(link.values[f.name()]) == "" {
			link.missing = append(link.missing, f.label)
		}
	}
}

// withValue is one <with> of a declared link, evaluated against the record.
func withValue(w TUIWith, rec map[string]any, carried map[string]string) (string, bool) {
	if w.Field != "" {
		v, ok := fields.Lookup(rec, w.Field)
		if !ok || !isScalar(v) || v == nil {
			return "", false
		}
		return fields.DisplayValue(v), true
	}
	data := map[string]any{"input": toAnyMap(carried)}
	maps.Copy(data, rec)
	s, err := renderString(w.Expr, data)
	if err != nil || strings.TrimSpace(s) == "" {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// recordValue reads the record field that answers to an input's name. Names match without regard to case, dashes or underscores.
// A nested record answers with its own name: an "owner" arg takes owner.login.
func recordValue(rec map[string]any, name string) (string, bool) {
	want := normName(name)
	keys := fields.SortedKeys(rec)
	for _, k := range keys {
		if normName(k) != want {
			continue
		}
		v := rec[k]
		if sub, ok := v.(map[string]any); ok {
			for _, id := range []string{"login", "name", "id", "slug", "key"} {
				if s, ok := sub[id]; ok && isScalar(s) && s != nil {
					return fields.DisplayValue(s), true
				}
			}
			return "", false
		}
		if v == nil || !isScalar(v) {
			return "", false
		}
		s := fields.DisplayValue(v)
		return s, s != ""
	}
	return "", false
}

// pluralOf reports whether a command name is the plural of an input name: repos for repo, branches for branch.
func pluralOf(command, input string) bool {
	c, i := normName(command), normName(input)
	return c == i+"s" || c == i+"es" || (strings.HasSuffix(i, "y") && c == i[:len(i)-1]+"ies")
}

// identityKeysForLinks are the fields that name a record to another command, in order.
var identityKeysForLinks = []string{"full_name", "slug", "id", "key", "name"}

// identityValue is the value that names a record to another command.
func identityValue(rec map[string]any) (string, bool) {
	for _, k := range identityKeysForLinks {
		if v, ok := rec[k]; ok && isScalar(v) && v != nil {
			if s := fields.DisplayValue(v); s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func normName(s string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(s))
}

// sibling reports whether b sits in the same group as a.
func sibling(a, b *browseEntry) bool {
	return slices.Equal(a.path[:len(a.path)-1], b.path[:len(b.path)-1])
}
