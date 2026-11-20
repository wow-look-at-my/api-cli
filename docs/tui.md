# The interactive browser: `tui`

`api-cli tui` turns a config into a terminal app. It needs no extra markup: the tree, the forms, the result tables and the links between commands all come from what the config already says. A few hints in the XML then fix whatever it guessed incorrect.

```bash
api-cli tui --config github.xml
```

The screen has panes. The command tree is on the left. The form for the selected command is on the right, with the command line it runs under it. The result of the last run fills the bottom.

## What you get for free

- **A form per command.** Each arg and flag is a field. A bool flag is a checkbox, and a flag with `choices=` is a picker.
- **Shared values.** A value typed into one command fills the arg of the same name in every other command. Type an owner one time, and every command that takes an owner has it.
- **A table of the result.** A JSON body becomes a table you can move through. A list of records gets columns: the identity keys first, then the short scalars. A key that holds a URL is left out. A nested record that names itself, such as `owner.login`, gets a column of its own.
- **Envelopes open at the records.** A body like `{"total_count": 2, "items": [...]}` opens at `items`. The envelope is one step back.
- **Step into anything.** Enter on a nested value opens it as its own table. Backspace steps back out.
- **Links between commands.** A record links to every command whose required inputs it can fill. An arg called `owner` takes the record's `owner` field, or `owner.login` when `owner` is itself a record. Enter follows the first link, and `a` lists them all. A link runs its target on top of a history, and backspace returns to the record you came from, with its form as it was.
- **Three views.** `v` cycles the result between the data view, the text the command prints on a terminal, and the raw body with its JSON indented. A leaf with `<fields>` shows its own table in the text view. A leaf with `<tml>` opens in the text view, which draws its screen.
- **A refreshing screen.** A command with `watch=` reruns on its interval while it is on screen, and keeps the selected row.
- **Filters.** `/` in the tree narrows the commands. `/` in a result narrows the rows.

## Hints for the rest

Every hint is optional. Each replaces a guess.

### `<tui>` on a command

```xml
<command name="list" description="One owner's repositories.">
	<arg name="owner" required="true"/>
	<tui autorun="true" rows="items" columns="name,stargazers_count,language" title="full_name">
		<link to="repos view" label="Open">
			<with name="owner" field="owner.login"/>
			<with name="repo" field="name"/>
		</link>
		<link to="issues list">
			<with name="repo" expr="{{ .owner.login }}/{{ .name }}"/>
		</link>
	</tui>
	<run>...</run>
</command>
```

| Hint | Effect |
| --- | --- |
| `label="Repositories"` | The name the tree shows. |
| `hidden="true"` | Leave the node and its subtree out of the tree. It still runs, and a link can still open it. |
| `autorun="true"` | Run as soon as the command is picked, once every required input holds a value. Use it on a read-only listing. |
| `rows="data.items"` | Where the records sit inside the body. Without it, a plain `<fields over=>` path wins, then the envelope rule. |
| `columns="name,stars,owner.login"` | The record paths to show, in order. Without it, the first `<fields>` block's fields win, then inference. |
| `title="full_name"` | The record path that names a record in the breadcrumb. |
| `<link to="repos view">` | A command a record opens. The first link is the one enter follows. A declared link always shows, even when the record cannot fill every input. Following it then stops at the form. |
| `<with name="repo" field="name"/>` | Fill one input of the target from a record path. |
| `<with name="repo" expr="..."/>` | Fill it from a template. The record's keys are at the top level, and the source form's values are at `.input`. |

An input that no `<with>` names takes the record field of its own name, then the value typed for that name anywhere. A group takes only `label=` and `hidden=`, because the other hints describe a result.

### `choices=` on an arg or a flag

```xml
<flag name="state" choices="open,closed,all" default="open"/>
```

This hint is more than a TUI hint. The command line rejects any other value, shell completion offers the choices, and the MCP tool schema publishes them as an `enum`. In the browser the field is a picker: the left and right arrows cycle through the choices, and enter lists them.

### `suggest=` on an arg or a flag

```xml
<arg name="repo" required="true" suggest="repos list" suggest-field="name"/>
```

Ctrl+O in the field runs `repos list` and offers each record's `name`. The source takes its own inputs from the form and the shared values, so its `owner` comes from the owner you already typed. Without `suggest-field=`, the browser takes the field named like the input, then a `name` or an `id`. A suggestion is advice. The field still takes any value.

## Keys

| Where | Keys |
| --- | --- |
| Tree | Up and down pick. Enter runs the command, or fills in its first empty required input. Right moves into the form. Left shuts a group. `/` filters. `q` quits. |
| Form | Type to edit. Tab moves on. Enter runs. Left and right cycle a choice. Ctrl+O lists suggestions. Esc returns to the tree. |
| Result | Up and down move. Enter opens the row. `a` lists every action. `i` steps into the row. Backspace steps back. `/` filters rows. `r` reruns. |
| Anywhere | `v` cycles the view. Ctrl+R runs. PgUp and PgDn scroll. `?` shows the keys. Ctrl+C quits. |

A click picks a row, and a second click on the selected row opens it. The wheel scrolls whatever is under the pointer.

## How a run works

A run is the command line under the form, with `--yes`. A command with `confirm=` asks in a popup first. An empty field is left out. The declared default applies. A variadic arg and a passthrough command split their field on spaces. A `string-slice` flag splits on commas.

Stdin is empty, because the terminal belongs to the browser.

The browser runs each command in the browser's own process, through the same code path as the command line. It reads the body before any formatting for the data view, and what the command printed for the text view.
