# Result reuse across calls (steps)

A leaf can declare `<steps>`, which are stages that run before the leaf's own run. Each step's output is captured and exposed at `.result.<name>`, for a later step and for the leaf's own `entry` and run. That gives you indirection, such as a name resolved to an ID and then used. It also gives you joins and fan-out pipelines.

A step runs a command or a request, the same fork as `<run>`. A step that declares neither one inherits the leaf's effective run. Chained calls against an inherited `<request>` therefore need nothing but a second `<entry>`.

```xml
<command name="user-posts" description="List posts for a user looked up by username.">
	<arg name="username" required="true"/>
	<steps>
		<step name="user">
			<entry>
				<path>/users</path>
				<query><param name="username"><value name="arg.username"/></param></query>
			</entry>
		</step>
	</steps>
	<entry>
		<path>/posts</path>
		<query><param name="userId"><value expr="{{ (index .result.user 0).id }}"/></param></query>
	</entry>
</command>
```

Mix the two freely. A `<step><run>` can be a shell command while the leaf makes a request, or the reverse.

- Steps run in declaration order. Each `entry` renders against the current context, and that context includes `.result.*` from the prior steps.
- Step output parses as JSON, with `UseNumber`. Output that is not JSON stays a string. A request step stores what the leaf prints, and that includes the `<response jq=>` shaping.
- A non-zero step aborts the run with that exit code, after its `retries=`. See [Retrying a step](#retrying-a-step-retries-and-on-error).
- A `when` attribute is a Go-template predicate. It skips the step on a falsy render (empty, `false`, `0` or `no`), and `.result.<name>` then stays unset.
- More than one command in a run prints `N executions` to stderr. Suppress that line with `--quiet` or `-q`.
- A step's `when` is evaluated **before** the step renders anything. A step that must not run therefore cannot fail on a value it never had.
- `<preconditions>` run before the first step, so `.result` is empty there. A precondition that reads it is a load error, not a surprise at run time. Put the check in a `<step when=>` instead.

## One guard for a whole subtree

`<preconditions>` is the setting that accumulates instead of overriding. Declare it on `<config>` and every run in the tree carries it. Declare it on a group node and its subtree carries it.

```xml
<config name="github">
	<preconditions>
		<precondition>{{ if not (safeSegments .arg.owner .arg.repo) }}owner and repo must each be one path segment{{ end }}</precondition>
	</preconditions>
	<command name="repo" description="Show a repository.">
		<arg name="owner" required="true"/>
		<arg name="repo" required="true"/>
		...
	</command>
</config>
```

- **A node runs the ancestors' guards first, then its own.** A broader rule therefore reports before a narrower one.
- **A group node holds a guard without running itself.** The guard belongs to the nodes below it.
- **The MCP path carries the same guards.** A tool call is gated exactly as the CLI invocation is.
- **An arg the leaf never declares reads as absent.** Pair the guard with a helper that skips an absent value.

## One call per element: `<step over=>`

A step with `over="result.builds"` runs once per element of that list. The element rides in the context as `.item`, and its position as `.index`. The step's own `entry` then names the part of it that says what to fetch.

```xml
<step name="detail" over="result.running.updates">
	<entry>
		<path>/updates/<value name="item.id"/></path>
	</entry>
</step>
```

`.result.detail` is then a list of `{"item": element, "result": response}`, in the source order. That pairing is the point: a screen that draws a card per build walks one list, rather than reaching across two of them by position. A repeated step is also how a list endpoint that carries no detail becomes one that does. Most CI and queue APIs take that shape.

A failing element fails the whole step, with that element's exit code. A board missing one build reads as a shorter queue rather than as a broken run. The run stops instead. `on-error="skip"` is the explicit way to keep the other elements. See [Retrying a step](#retrying-a-step-retries-and-on-error).

An error inside a repeated step names the step, the element's position, the total and the element itself.

```
error: step "jobs" [7/20] JOB-7: transport "corp" exited 1
```

`over=` walks a list the context holds: a step result, a `<var>`, or a `variadic` arg, whose Go slice needs no JSON detour. It also takes a template that renders a JSON list, for a list the context does not hold in that shape. See [`over=` on a download](#downloads) for the `collect` helper that flattens a fan-out result.

## Waiting for a job: `<step until=>`

An API that answers `status: pending` needs a poll, not a call. A step with `until=` repeats its own run until that predicate holds. An async listing therefore needs no shell loop around the program.

```xml
<step name="listing" until="{{ eq .status &quot;done&quot; }}" interval="1s" attempts="120">
	<run><request><url><value name="var.api"/>/jobs/<value name="result.submit.job"/></url></request></run>
</step>
```

- **The predicate sees the last response**, with its keys promoted to the top level. An async job's own `status` field is therefore `.status`. The whole body stays at `.body`, and the rest of the run context is there too.
- **`.result.<name>` is the body that satisfied the predicate**, not one of the answers before it.
- **`interval=`** is a duration such as `500ms` or `2s`. It defaults to one second, and it never grows: a slow job is not a reason to wait longer and longer for it.
- **`attempts=`** caps the poll at 60 by default. A poll that runs out fails the run and prints the last response. The reason is therefore the body itself rather than a bare timeout.
- **A non-zero exit ends the poll at once**, after the step's `retries=`. A job that reports a failure has answered, and asking again cannot change it.
- **`over=` and `until=` compose.** A repeated step polls each element in turn, which is how one invocation lists N items whose listings are all jobs.

## Retrying a step: `retries=` and `on-error=`

A flaky endpoint needs a second try, not a shell loop. `retries="N"` runs a failed step again, up to N more times, before the failure counts.

```xml
<step name="jobs" over="result.items" until="{{ eq .status &quot;done&quot; }}" retries="3" on-error="skip">
	<run><request transport="corp"><url><value name="var.api"/>/jobs/<value name="item.id"/></url></request></run>
</step>
```

- **A failure is any failed run.** A command fails on a non-zero exit. A request fails on an HTTP error status or a network fault. It also fails on a `<transport>` that exits non-zero, or a body that is not JSON under `jq=`.
- **The retries run one second apart, at a fixed cadence.** The delay never grows. The download queue uses the same delay.
- **The default is `0`**, which fails at once. A negative value is a load error.
- **A poll retries each attempt.** A retried attempt does not use up an `attempts=` slot. A poll that runs out of attempts is final, and nothing retries it.
- **With `over=`, each element gets its own retries.**

`on-error=` decides what a repeated step does with an element that still fails. It takes `fail` (the default) or `skip`. It needs `over=`. On a step without `over=`, it is a load error.

- **`skip` leaves the element out of the step's result list.** It prints that element's error and continues. A `<download over=>` therefore gets the elements that succeeded.
- **A poll that never satisfies `until=` counts as a failure**, so `skip` leaves that element out too.
- **A skip is never silent.** The run exits 1 at the end, and stderr names every element it left out.

```
jobs: 2 of 20 items skipped: JOB-3, JOB-11
```
