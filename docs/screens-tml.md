# Screens: `<tml>`

`<fields>` says what the records are, and the renderer picks a table or a list. A screen is the other shape of an answer: several numbers, a heading and one list, laid out at once. `<tml>` gives a leaf that shape. It names a component written in [TML](https://github.com/wow-look-at-my/tml), a declarative language for terminal layout. It then says which part of the response fills each of the component's properties.

```xml
<command name="dash" description="A repository on one screen.">
	<arg name="repo" type="string" required="true"/>
	<entry>
		<path>/repos/<value name="arg.repo"/></path>
	</entry>
	<tml src="ui/repo.tml" dark="true">
		<prop name="name" from="full_name"/>
		<prop name="stars" from="stargazers_count"/>
		<prop name="releases" over="result.releases">
			<field name="tag">tag_name</field>
			<field name="published">published_at</field>
		</prop>
	</tml>
</command>
```

The component is an ordinary `.tml` file next to the config. `src` resolves against the config's own directory, and an `<Import>` inside it resolves against the component's directory.

A `<prop>` fills one declared property. It takes exactly one source:

| Form | Value |
| --- | --- |
| `<prop name="title">Deployments</prop>` | The element's text, rendered as a template like any other content. |
| `<prop name="stars" from="stargazers_count"/>` | One value out of the response body, or out of the leaf context. |
| `<prop name="rows" over="services"><field name="id">id</field></prop>` | A list. Each `<field>` maps a path inside one element to one property of the item template. |

A `<field>` inside a repeated prop takes more than a path:

| Attribute | Effect |
| --- | --- |
| `expr="{{ ... }}"` | Compute the value. The element's own keys are promoted to the top level, `.item` and `.index` name the element and its position, and `$` reaches the whole run. |
| `lines="true"` | Cut the value into a list of strings, which is the `string[]` property a data template walks with `<For>`. |
| `last="4"` | Keep the last few of those lines. It is a template, so `last="{{ .flag.lines }}"` follows a flag. |
| `truncate="88"` | Clip each line to that many display cells, ellipsis included. Also a template. |

`lines` exists for a log. One field holds a blob of output. A card has room for the tail of it. TML does no wrapping of its own. A line wider than the card therefore wraps in Lip Gloss and pushes the card's border down a row. Clip it here, or give the component's `<Text>` an `overflow`.

Every value crosses as text, and the component re-reads it as the type it declared. So an `int` property takes `3` and a `color` property takes `#d97706` without the config naming a type of its own. A component rejects a property it never declared. A data template rejects a field it never declared. So one name on one side and a different name on the other fails the run, rather than drawing a blank cell.

`over=` reads the response body first and the whole context second, exactly as a `<fields>` projection does. That is how a step result reaches the screen: `over="result.releases"` is the list a `<step name="releases">` fetched.

A screen needs a terminal. Piped, the leaf falls through to whatever else it declared, which is the raw body or a `<format>` view. `--as=<sink>` names a representation the user wants instead, so it wins and the leaf goes through `<fields>`. `--format=always` draws the screen anyway, at 80 by 24, which is how a screen is testable without a terminal. A leaf declares `<tml>` or `<fields>`, not both.

A one-shot frame lays out in a tall viewport rather than the terminal's, because it prints into a terminal that scrolls. A board of cards is therefore never cut off at the last row. The blank rows under the content are trimmed. Under a watch the screen IS the height. The program owns it.

On its own the leaf draws one frame and exits. With `--watch` it becomes a terminal program on the alternate screen. A tick is one whole run of the leaf, the same as a watch frame. The program draws the component itself, so its controls take focus with tab, and the pointer hovers and clicks. A run that fails shows its error in place of the component until a run succeeds again.

State that the user drives, such as how far. A log scrolled or which row a list picked, comes from multiple prop sources that api-cli keeps between runs:

```xml
<tml src="ui/jobs.tml">
	<prop name="jobs" over="data.jobs"><field name="name">name</field></prop>
	<prop name="picked" select="jobs"/>
	<prop name="offset" scroll="log"/>
</tml>
```

`select="jobs"` is the row picked in the element with id `jobs`. The arrows move it while that element has focus, and a click picks a row. `scroll="log"` is how far the element with id `log` scrolled: the wheel moves it, and so do the page keys. Bind each prop to the element's `selected` or `offset`. Both start at 0, and both hold 0 in a one-shot frame. An element with `action="refresh"` runs the leaf again when it is activated.

The pointer is on only when the screen declares such a prop, so a screen without one still lets the terminal select text.
