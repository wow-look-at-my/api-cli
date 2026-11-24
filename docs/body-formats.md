# Body formats: `decode=`

A run produces bytes. `decode=` on the `<run>` names the format of those bytes, and api-cli turns them into data in that format. The config language is tied to no one format: a response in CSV, YAML or XML is records, exactly as a response in JSON is.

```xml
<command name="stations">
	<run decode="csv">
		<request>
			<url>https://example.com/stations.csv</url>
			<response jq="map(select(.state == &quot;open&quot;))"/>
		</request>
	</run>
	<fields>
		<field name="NAME">name</field>
		<field name="CITY">city</field>
	</fields>
</command>
```

The run can be a request, an argv list or shell text. The data that `decode=` produces is what `<response jq=>` reads.

| `decode=` | The data |
|-----------|----------|
| `auto` | The default. See [The default](#the-default). |
| `json` | One JSON value. |
| `ndjson` | A list, one JSON value per line. A blank line is skipped. |
| `csv` | A list of records. The first row names the columns, and every cell is a string. |
| `tsv` | The same, with tabs. The format has no quoting, so a quote is data. |
| `yaml` | One YAML document. A stream of several documents is a list. |
| `xml` | An object. See [XML](#xml). |
| `form` | An object of `application/x-www-form-urlencoded` pairs. A key that repeats holds a list. |
| `lines` | A list of strings, one per line. |
| `text` | One string, without the trailing line breaks. |

## The default

With no `decode=`, a request follows the `Content-Type` of its response.

Every other body is JSON when it parses, and the trimmed text when it does not. That covers `application/json`, `text/plain`, a response with no type, and the output of a command. A `<transport>` program hands back bytes with no type, so its body takes the same path.

The default is a guess. A guess never fails a run. A body that does not read as its `Content-Type` says is text, and `--verbose` reports it.

## A named format is a claim

A body that is not in the named format fails the run. The error shows the start of the body.

```
error: the output is not json: invalid character '<' looking for beginning of value; it starts with "<!DOCTYPE html><html><head><title>Sign in</title>..."
```

So `decode="json"` is the way to make an SSO page or a proxy error a failure instead of a string. An empty body is not a broken body: it reads as null, or as an empty list for the formats that hold a list.

Write `decode="text"` to keep a body as one string, even when it looks like JSON or arrives as `text/csv`.

## It belongs to the run

`decode=` travels with the `<run>` that carries it. A command that inherits a run inherits the format. A command that declares its own `<run>` takes the format of that run, and a run with no `decode=` is `auto`. A step with its own `<run>` follows the same rule.

Output that nothing shapes prints as it arrived. A leaf with no `<fields>`, and any leaf under `--no-format`, prints the CSV as CSV. `--as=json` on that leaf prints the decoded records as JSON, and `--as=table` draws them with the columns in header order.

## jq reads the data

`<response jq=>` runs over the decoded body in every format, and its result is JSON. A `<response/>` with no program pretty-prints a JSON body and passes any other format through unchanged.

```xml
<run decode="text">
	<request>
		<url>https://example.com/robots.txt</url>
		<response jq="split(&quot;\n&quot;) | map(select(startswith(&quot;Disallow&quot;)))"/>
	</request>
</run>
```

A request with `jq=`, no `decode=`, and a body that is neither JSON nor a known `Content-Type` fails. The error names `decode=` as the fix.

## XML

The root element is the key of an object. Under it:

- An attribute is `@name`.
- A child element is a key. A child that repeats is a list.
- An element with text only is that string.
- Text beside attributes or children is `#text`.
- A namespace prefix is dropped, and so is an `xmlns` declaration.

```xml
<rss version="2.0">
	<channel>
		<item><title>one</title><guid isPermaLink="false">a1</guid></item>
		<item><title>two</title><guid isPermaLink="false">a2</guid></item>
	</channel>
</rss>
```

`<fields over="rss.channel.item">` walks those items. `title` is a field path, and so are `guid.#text` and `guid.@isPermaLink`. A child that appears one time is an object and not a list of one. `<fields over=>` shows a lone object as one record. A jq program that needs a list either way writes `[.rss.channel.item] | flatten`.

## A legacy `<format>`

A `<format>` with no `input=` reads the body as the run's `decode=` says. An `input=` still overrides it with one of its own readings. `json` is JSON when the body parses and text when it does not, `lines` is a list of lines, and `raw` is one string.
