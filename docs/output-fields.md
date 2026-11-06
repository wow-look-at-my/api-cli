# Output: fields

A leaf declares the *shape* of its output records one time, in `<fields>`. The renderer then represents that one declaration by itself: a table, a `Label: value` list, JSON, Markdown, CSV, plain lines, or an [ASCII timeline](#timeline). It picks the default from the shape of the data. You never write "table" anywhere. A flag (`--as`) or a pipe forces any representation at run time.

```xml
<fields over="data.items" footer="{{.data.total_count}} total">
	<field name="name">full_name</field>            <!-- rename a field -->
	<field name="stars">stargazers_count</field>
	<field name="lang" default="-">language</field> <!-- fallback for empty -->
	<field name="sha" truncate="7">sha</field>      <!-- transform -->
	<field name="branch" expr="{{.head.ref}} -> {{.base.ref}}"/>  <!-- computed -->
</fields>
```

Automatic representation, by data shape:

| Data | Default | Other sinks |
|------|---------|-------------|
| array of records | `table` | `json`, `markdown`, `csv`, `timeline` |
| single record | `list` | `json` |
| map walked by `@key`/`@value` | `table` | `json`, `markdown`, `csv` |
| array of scalars | `lines` | `json` |
| scalar / non-JSON | `raw` | -- |

A table column carries spaces of gutter. A column wider than 50 columns carries three, because the eye loses a long run of prose against a two-space gap. The width that decides a column drop counts the same gutter.

**A `table` or `markdown` cell is one line.** A row is a line. A column is a position on that line. So a run of whitespace that holds a newline or a tab becomes one space. A value with neither is untouched. The `list` sink keeps the whole value and indents its later lines under the first.

`raw`, `json` and `csv` carry it exactly. Use `firstline="true"` or `truncate="N"` to show less.

A map is one record unless some field reads `@key` or `@value`. That is the signal to walk it entry by entry. An array of scalars is `lines` only when the leaf declares no `<field>`. One declared field keeps the table shape.

| `<field>` attribute | Meaning |
|---------------------|---------|
| body text | Record-relative source path (`login`, `user.login`). A numeric step indexes a list (`assets.0.name`). |
| `@key` / `@value` | The entry key/value when `over=` walks a map. |
| `expr=` | A virtual field: a Go template with the record as `.` and the whole context as `$` (`$.var`, `$.data`). Overrides the path. |
| `default=` | Substitute for an empty value. |
| `truncate="N"` | Cap the string to N characters. |
| `firstline="true"` | Keep only the first line. |
| `priority="N"` | The lowest priority column drops first when a table is too narrow. The default is 0. A tie drops the rightmost column first, and one column always survives. |
| `show_in=` | Gate the field per sink. `""` and `*` mean every sink. An allowlist (`json,csv`) shows it only there. A negated list (`!json`) shows it everywhere else. |

`<fields over="path"/>` selects where the records live, in place of the whole body: `data.items`, a map for `@key` and `@value`, or `data.names` for scalars. The path reads against the body first (`items`), then against the whole context (`data.items`), so either spelling reaches the same records. A numeric step indexes a list (`pages.0.rows`). `footer=` adds a trailing summary line to the human sinks.

**A path that names nothing is an error**, and so is one that names a scalar. The run exits non-zero and names the path. An empty table over the first exit reads as an API that returned nothing, which is how a renamed field costs an afternoon. An empty **list** is a real answer. It stays quiet.

A request leaf with **no** `<fields>` at all prints its JSON body, as jq shaped it. Add `--as=table` to project nothing and to table the raw keys.

## More than one shape on one leaf

A leaf can declare several `<fields>` blocks. Each block whose `when=` predicate holds renders, in document order, and a block with no `when=` always renders. That covers the cases one static block cannot.

```xml
<command name="thing" description="List things, or show one.">
	<arg name="id"/>
	<run><request><url><value name="var.api"/>/things/<value name="arg.id" as="urlpath"/></url></request></run>
	<fields when="{{ not .arg.id }}" over="items">   <!-- the no-id call: a table -->
		<field name="id">id</field>
		<field name="name">name</field>
	</fields>
	<fields when="{{ .arg.id }}">                    <!-- the with-id call: a detail view -->
		<field name="name">name</field>
		<field name="body">body</field>
	</fields>
</command>
```

The first case is an optional arg that changes the response shape. The second is a dashboard. One block reads the leaf body. A second block reads `.result.<step>`. Tables then share the screen, with a blank line between them.

`when=` is a Go-template predicate over the format context. It reads `.data`, `.tty` and `.width`, plus the leaf context (`.arg`, `.flag`, `.var`, `.entry`, `.result`). A leaf whose blocks all sit out prints the raw body, exactly as a leaf with no `<fields>` does. `--as=<sink>` applies to every block that renders.

## Forcing a representation

`--as=<sink>` forces `table | list | lines | raw | json | markdown | csv | timeline`. `--no-format` returns the raw body, and `--format=raw` and `NO_FORMAT=1` do the same. So `gh repo get x` is a list on a terminal, `gh repo get x --as=json | jq` is JSON, and `gh repo get x --no-format` is the unshaped response.

## Timeline

`--as=timeline` renders the records as a horizontal ASCII timeline with annotations. It runs the [`ascii-timeline`](https://github.com/wow-look-at-my/ascii-timeline) executable, which must be on `PATH`. The apt and brew packages of api-cli depend on it, so they install it too. Without it the run fails with an error that says so. Each record becomes one event, and the **field name** selects what that field contributes.

| Field name    | Event role                                              |
|---------------|---------------------------------------------------------|
| `date`        | A **point** event at this instant (a `●` marker).       |
| `start`+`end` | A **duration** event spanning the range (a `█` bar).    |
| `label`       | Text shown next to the marker/bar.                      |
| `description` | Dim text appended after the date.                       |
| `color`       | Style spec, for example `green` or `bold cyan`. The renderer assigns one when the field is absent. |

The renderer ignores every other field name, and `show_in=` still applies. Use `show_in="timeline"` or `!timeline` to scope a field. A date can take any of the many formats `ascii-timeline` reads (`2006-01-02`, `Jan 2, 2006`, RFC3339, ...). The renderer skips a record that resolves no `date` and no `start` and `end` pair, so partial data still draws. Color follows the terminal, and goes off in a pipe or under `NO_COLOR`. The axis uses the terminal width.

```xml
<fields>
	<field name="label">name</field>
	<field name="date">created_at</field>          <!-- point event -->
	<field name="start">window.opened</field>      <!-- or a duration: start -->
	<field name="end">window.closed</field>        <!--                + end   -->
	<field name="description" default="-">title</field>
</fields>
```

```text
$ ghr repo releases golang/go --as=timeline
# Timeline

Feb 8, 2026  →  Sep 2, 2026   (7 months)
 Mar 2026        May 2026        Jul 2026        Sep 2026
├──┴───────────────┴───────────────┴───────────────┴──────────┤
   ● go1.24.1 (Mar 4, 2026)
            ● go1.24.2 (Apr 1, 2026)
                            ● go1.25rc1 (Jun 10, 2026)
                                              ● go1.25 (Sep 2, 2026)
```

The sample's `repo commits` command maps `commit.author.date` to a timeline in the same way. When the upstream JSON already carries keys named `label`, `date`, `start` and `end`, you can leave the `<fields>` block out. `... --as=timeline` then derives them directly.
