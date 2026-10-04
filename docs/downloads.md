# Downloads

A step can work a URL out: parse it from a listing, sign it, or follow a redirect. It then hands the URL to a shared downloader in place of printing it. One queue serves the whole run. It fetches files at a time by default. It also carries the auth the steps established.

```xml
<downloads concurrency="8" dir="./out"/>
...
<command name="grab" description="Fetch every asset in the release.">
	<steps>
		<step name="release">
			<run><request><url><value name="var.api"/>/releases/latest</url></request></run>
		</step>
	</steps>
	<download over="result.release.assets">
		<url><value name="browser_download_url"/></url>
		<to><value name="name"/></to>
		<hash algo="sha256"><value name="digest"/></hash>
		<header name="Authorization">Bearer <value name="var.token"/></header>
		<cookie name="session"><value name="result.login.sid"/></cookie>
	</download>
</command>
```

On a terminal this draws a block of slots at the bottom of the screen, in color. An in-flight transfer holds one slot, with a bar, its percentage, sizes, rate and ETA. An aggregate `TOTAL` row closes the block. A narrow terminal drops the bar first, then the rate, the ETA and the sizes. The block is a [tml](https://github.com/wow-look-at-my/tml) component, and `NO_COLOR` turns the color off.

A transfer that finishes gives up its slot and emits one `downloaded` line above the block. The output of the steps goes to the same place. Those lines are written one time and scroll away into the terminal's own scrollback. A long run therefore reads as the list of what landed.

```
downloaded CHUNK_01.data.message (76.5 MiB)
downloaded CHUNK_02.data.message (67.3 MiB)
downloads: 3 active, 9 queued, 2 done
  CHUNK_03.data.message    59%     3.6 MiB / 6.0 MiB      870.1 KiB/s  ETA 00:02
  CHUNK_04.data.message    55%     1.7 MiB / 3.0 MiB      406.0 KiB/s  ETA 00:03
  CHUNK_05.data.message    69%     5.5 MiB / 8.0 MiB      1.3 MiB/s    ETA 00:01
  TOTAL                    63%    10.8 MiB / 17.0 MiB+    2.6 MiB/s
```

The block comes off the screen when the queue drains, and the run's summary follows the emitted lines.

A start is never announced. The slot already says that a transfer runs. A separate `downloading` line only doubles the volume.

In a pipe there is no display. The `downloaded` lines stay on stderr, and the destination paths go to stdout, one per line, for whatever reads them next.

The steps that feed a `<download>` report their progress too. On a terminal, the block holds one live line for the step that runs. It names the element, the poll attempt, and the `.status` field of the last response when the body has one. A retry shows as `retry 1/3`.

```
jobs 7/20 JOB-7  attempt 4/30  status=pending
```

Without the display (`--no-tui`, or stdout in a pipe), stderr gets one line per element when it finishes, such as `jobs 7/20 JOB-7: done`. A long poll also adds one line for every ten attempts that leave `until=` false. No line appears per attempt.

- **`<download>` is the leaf's action.** It runs after the steps, and it stands in for the leaf's `<run>`. An inherited request therefore does not fire on the way.
- **`when=`** is a Go-template predicate. A falsy render (empty, `false`, `0` or `no`) skips that declaration, so one leaf can carry a conditional set.
- **`over=`** repeats the declaration per record of a list. It promotes the record's keys (`<value name="name"/>`) and puts the record itself at `.item`. An empty list downloads nothing. A path that resolves to nothing is an error.
- **`over=` also takes a template**, when the list it wants is not one the context already holds. The template renders and its output is read as JSON. It therefore ends in `toJson`. `collect` gathers one path out of every element of a fan-out result: `over="{{ toJson (collect &quot;result.parts&quot; .result.listings) }}"`.
- **`<url>` can render several lines**, which is what a `<for>` loop produces. Each line becomes its own download.
- **`<to>`** is a file path. It is a directory when it ends in `/`, when it names an existing directory, or when it serves several URLs. Leave it empty for the download directory, named by the URL or by the response's `Content-Disposition`.
- **A relative `<to>` resolves under the download directory** (`dir=`, or `--download-dir`). An absolute one stands as it is.
- **Auth rides along.** `<header>` and `<cookie>` render against the same context and take `<if test=>`. The cookies fold into one `Cookie` header.
- **Mistakes fail before the first byte.** The plan step reports a `<url>` that renders to anything but an `http(s)` URL. It also reports records whose `<to>` renders one file path. A mistyped path is therefore not a timeout minutes later, and a `<to>` that forgot to vary does not overwrite itself N times.
- **Failures are loud.** A 4xx is the answer. A 5xx or a network fault retries at a fixed one-second cadence. A transfer that still fails names its URL on stderr and exits non-zero, while the other files carry on.
- Bytes land in a `.part` sibling first, so an interrupted transfer never leaves a truncated file under the real name.
- **No resume.** Every attempt starts at byte zero. A leftover `.part` is overwritten rather than continued.

## Joining the parts back: `<join>`

An API that hands out a capture in numbered parts wants those parts back as one file. `<join>` says so in the config, so no wrapper script has to know the layout the parts landed in.

```xml
<download over="{{ toJson (collect &quot;result.listing.parts&quot; .result.listings) }}"
	group="{{ .item_id }}" order="{{ .sequence }}">
	<url><value name="url"/></url>
	<to><value name="run.tmpdir"/>/<value name="item_id"/>/<value name="sequence"/>.part</to>
	<join to="{{ .item_id }}.mp4" cleanup="true" contiguous="error"/>
</download>
```

- **`group=`** buckets the members. Every record that renders the same group becomes one output. Leave it out for a single bucket.
- **`order=`** is the position inside the bucket. It is read as a **number**. A capture that numbers its parts 2, 3 and 10 therefore joins in that order, not as 10, 2, 3. A value that is not a number fails the run before the first byte. Without `order=`, the queue order stands in for one.
- **`to=`** is the output file. It is a template over the same record, so one declaration writes one file per group. A relative path resolves under the download directory, exactly as `<to>` does.
- **A group joins as soon as its own last part lands.** A run of many items writes its first output while the rest still transfer.
- **The join streams.** A bucket larger than memory is fine.
- **A group short a part is not written at all.** The run reports which members failed and exits non-zero. Half a capture wearing the real name is worse than no file.
- **`cleanup="true"`** removes the parts after the output lands, and it removes the directories they left empty. A failed group keeps its parts.
- **`contiguous=`** reports a hole in the numbering: `warn` logs it and still writes, `error` fails the group and writes nothing. It needs `order=`, because the numbers are what it checks. It reads the whole numbers between the lowest and the highest. A listing that stops early therefore looks complete to it.
- Each member needs its own `<to>`. That `<to>` must name a file. The parts are separate files until the join makes them one.
- The output goes through a `.part` sibling too. An interrupted join therefore leaves no truncated file under the real name.

## A working directory for the parts: `.run.tmpdir`

A leaf that declares `<download>` gets a scratch directory of its own at `.run.tmpdir`. The directory is removed when the run ends, whatever the outcome. Parts that a `<join>` consumes belong there, so nothing outside the config has to make a directory and pass it in. Files you mean to keep go under the download directory instead, which this never touches.

## Downloading through a transport

A `<download>` reaches its URL as a `<request>` does. It goes over the built-in client, or over a [transport](#transports) program when the endpoint needs one.

```xml
<download transport="corp">…</download>
```

- **Selection matches requests**: the `transport=` attribute, then the registry's `default="true"` entry, then the built-in client. A config whose endpoints all need the program therefore needs it for its files too, and says nothing extra. `transport="http"` opts one download back to the built-in client.
- **The program gets the same `.request` context** -- `method`, `url`, `headers` and `header_lines` -- so one program serves requests and downloads alike. `method` is `GET`, and there is no body. A download adds `progress_fd`, the fd for [progress reports](#reporting-progress-from-a-transport).
- **Its stdout streams into the file** rather than into a buffered response body. That is the difference between the paths. It is also why a file larger than memory is fine. The `.part` sibling, the byte count and the digest check are the same code on both.
- **A non-zero exit fails the download, and the queue retries it.** A program owns its own exit codes. curl says 22 for a 404 and 7 for a refused connection. This path therefore cannot tell an answer from a hiccup, unlike the built-in client, and it lets the attempt limit end the transfer. Its stderr is emitted above the slots.
- The size is unknown at the start, because there is no `Content-Length`. That file's row drops the bar and the percentage. It reports the bytes and the rate. A file with no bytes yet reads `waiting mm:ss`, which is the time since its transfer started. A frame where no file reported a length drops both columns outright. The total is marked as a floor. The program can supply both numbers itself on the [progress fd](#reporting-progress-from-a-transport).

## Reporting progress from a transport

A program that holds the body until it finishes writes nothing to stdout mid-transfer. On Linux, api-cli then measures it from outside: the row shows the bytes the program and its child processes have read, from `/proc/<pid>/io`. That count includes protocol overhead and the program's own startup reads. As a result, it is close but not exact. Other systems have no `/proc`. The row reads `waiting` until the file lands.

The progress fd gives the exact number on every system. Wire it up whenever the program you delegate to does not stream its output. A report replaces the `/proc` estimate for that transfer.

api-cli gives every download transport a pipe on file descriptor 3. The variable `API_CLI_PROGRESS_FD` names it in the program's environment, and `.request.progress_fd` names it in the templates. The program writes one JSON object per line:

```
{"total":73400320}
{"done":1048576}
{"done":2097152,"total":73400320}
```

- **`done`** is the bytes received so far, and **`total`** is the file's size. Each is optional. A line needs at least one. A report replaces the previous one. It never adds to it.
- **Stdout is still the file.** The final byte count, the `.part` rename and the digest come from stdout alone. A report only drives the row while the transfer is open.
- **A bad line fails the download.** An unknown key, a missing value or a negative number stops the transfer with an error that names the line. The queue does not retry it, because the same program sends the same line again.
- **A program that never writes to the fd is fine.** The row falls back to the `/proc` estimate on Linux, and to the stdout count elsewhere.

Pass the fd to a program that takes it as a flag:

```xml
<transport name="corp">
	<run>
		<argv>corp-fetch</argv>
		<argv>--progress-fd</argv>
		<argv><value name="request.progress_fd"/></argv>
		<argv><value name="request.url"/></argv>
	</run>
</transport>
```

Only the program knows how far it got when it holds the body in memory. A wrapper around it sees nothing until the program writes. So the report has to come from the program: from its read loop, as each chunk arrives. In Go that is a counting `io.Writer` in the copy from the response body:

```go
type report struct {
	Done  *int64 `json:"done,omitempty"`
	Total *int64 `json:"total,omitempty"`
}
progress := json.NewEncoder(io.Discard)
if fd, err := strconv.Atoi(os.Getenv("API_CLI_PROGRESS_FD")); err == nil {
	progress = json.NewEncoder(os.NewFile(uintptr(fd), "progress"))
}
if resp.ContentLength >= 0 {
	progress.Encode(report{Total: &resp.ContentLength})
}
// on each chunk:
progress.Encode(report{Done: &received})
```

Write a report at most a few times a second. The display repaints every 100 ms, so more lines only cost the program time.

## Checking a download against a digest

`<hash>` is optional. With one present, the queue verifies the file as it streams. A file that does not match its digest is deleted rather than renamed into place.

```xml
<hash algo="sha256"><value name="digest"/></hash>
```

- `algo=` is `sha256` (the default), `sha512`, `sha1`, or `md5`.
- The body is a template like any other. The digest therefore usually comes from the same manifest record as the URL, or from a step that fetched a `.sha256` file.
- **A mismatch is final.** The bytes arrived intact, so a second fetch cannot change the digest. The run reports the expected value and the actual value, then exits non-zero.
- A success names the algorithm that passed: `downloaded x.iso (6.0 MiB, sha256 ok)`. A check you cannot see happen is one you cannot trust.
- **The digest must look like a digest.** A renamed manifest field renders as the template engine's placeholder. The plan step rejects that before it fetches anything, rather than leave the file unverified in silence. A `sha256sum` line (`<hex>  <name>`) is acceptable, in any capitalization.
- **To make the check optional per record**, render the body empty for a record that carries no digest: `<hash><if test="sha256"><value name="sha256"/></if></hash>`.

`<downloads>` sets the queue up one time for the config. It takes `concurrency` (default 4), `retries` (default 3, where `0` reports a failure immediately) and `dir` (default `.`). `--concurrency`, `--download-dir` and `--no-tui` override those values per invocation.
