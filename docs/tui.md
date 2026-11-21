# The interactive browser: `tui`

`api-cli tui` turns a config into a terminal app. It needs no extra markup: the tree, the forms, the result tables and the links between commands all come from what the config already says. A few hints in the XML then fix whatever it guessed incorrect.

```bash
api-cli tui --config github.xml
```

The screen has panes. The command tree is on the left. The form for the selected command is on the right, with the command line it runs under it. The result of the last run fills the bottom.

## What you get for free

- **Commands that change things are marked.** A command that asks for confirmation. It sends a POST, PUT, PATCH or DELETE request, carries a `!` in the tree, and its form says what it sends.
- **A form per command.** Each arg and flag is a field. A bool flag is a checkbox, and a flag with `choices=` is a picker.
- **Shared values.** A value typed into one command fills the arg of the same name in every other command. Type an owner one time, and every command that takes an owner has it.
- **A table of the result.** A JSON body becomes a table you can move through. A list of records gets columns: the identity keys first, then the short scalars. A key that holds a URL is left out. A nested record that names itself, such as `owner.login`, gets a column of its own.
- **Envelopes open at the records.** A body like `{"total_count": 2, "items": [...]}` opens at `items`. The envelope is one step back.
- **Step into anything.** Enter on a nested value opens it as its own table. Backspace steps back out.
- **Links between commands.** A record links to every command whose required inputs it can fill. An arg called `owner` takes the record's `owner` field, or `owner.login` when `owner` is itself a record. Enter follows the first link, and `a` lists them all. A link runs its target on top of a history, and backspace returns to the record you came from, with its form as it was.
- **A listing names its rows.** A command named for a plural, such as `tags` or `user repos`, lists things of that kind. Its row fills an arg named for one of them, `tag` or `repo`, with the row's identity.
- **Three views.** `v` cycles the result between the data view, the text the command prints on a terminal, and the raw body with its JSON indented. A leaf with `<fields>` shows its own table in the text view. A leaf with `<tml>` opens in the text view, which draws its screen.
- **A refreshing screen.** A command with `watch=` reruns on its interval while it is on screen, and keeps the selected row.
- **Filters.** `/` in the tree narrows the commands. `/` in a result narrows the rows.
- **A session that picks up where it stopped.** The browser saves the shared values, each command's form and the picked command to a file under the user cache directory, one file per config. An input whose name looks like a secret is never saved. A saved file that does not parse is reported on the status line. The session starts empty.
- **Sort and mark.** `s` sorts the rows by a column you pick, numbers as numbers. A second pick of the same column flips the order. Space marks a row, and `*` marks every row the filter shows. With rows marked, enter or `a` runs a link once per marked row, after one confirmation, and shows a table of the runs. Enter on a row of that table opens what that run returned.
- **A run you can watch and stop.** While a command runs, the result pane shows what it printed so far, so a stream or a download shows progress. Ctrl+X cancels the run: its requests stop, and its processes are killed.
- **A record's web page.** The `a` menu lists each field of the row that holds a web address, such as `html_url`. A pick opens that page in the browser. `$BROWSER` names the program, and without it the platform's own opener runs.
- **Pages.** A command with an int flag named `page` pages with `]` and `[`. The result reruns in place on the next or the previous page.

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

The same hint drives shell completion. A tab on `repo get` runs the source and offers its values, each with the record's title. The source takes its inputs from the words already typed, then from the values the browser saved. A source that asks for confirmation never runs from a tab. Neither does one that still lacks a required input. A hidden command can be a source.

## Keys

| Where | Keys |
| --- | --- |
| Tree | Up and down pick. Enter opens the form at the first empty required input, and runs a command that takes no input. `r` runs the command as its form stands. Left shuts a group. `/` filters. `q` quits. |
| Form | Type to edit. Tab moves on. Enter runs. Left and right cycle a choice. Ctrl+O lists suggestions. Esc returns to the tree. |
| Result | Up and down move. Enter opens the row. `a` lists every action, a web page included. `i` steps into the row. Backspace steps back. `/` filters rows. `r` reruns. `]` and `[` turn the page. `s` sorts. Space marks a row, `*` marks them all, and esc clears the marks. `y` copies a cell, the row as JSON, the command line or the whole result. |
| Anywhere | `v` cycles the view. Ctrl+R runs. Ctrl+X cancels a run. PgUp and PgDn scroll. `?` shows the keys. Ctrl+C quits. |

A click picks a row, and a second click on the selected row opens it. The wheel scrolls whatever is under the pointer.

## How a run works

A run is the command line under the form, with `--yes`. A command with `confirm=` asks in a popup first. An empty field is left out. The declared default applies. A variadic arg and a passthrough command split their field on spaces. A `string-slice` flag splits on commas.

Stdin is empty, because the terminal belongs to the browser.

The browser runs each command in the browser's own process, through the same code path as the command line. It reads the body before any formatting for the data view, and what the command printed for the text view.
