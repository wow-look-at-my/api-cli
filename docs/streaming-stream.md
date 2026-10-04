# Streaming: `<stream>`

A leaf that runs a source with no end -- a log that grows, an audio feed, a video stream -- cannot print it. A run that waits for the source to finish never finishes. `<stream>` says how to cut that source into chunks instead. Each chunk goes to stdout as soon as it is whole. Nothing upstream holds the source.

```xml
<command name="tail" description="Follow a log and keep the errors.">
	<arg name="path" required="true"/>
	<stream mode="lines">
		<run>grep -i error</run>
	</stream>
	<run>tail -F <value name="arg.path" as="shellquote"/></run>
</command>
```

The leaf's own `<run>` supplies the bytes. It may be a shell command, an argv command, or a request -- a request's response body streams off the socket. The `<stream>` declaration then says where a chunk ends:

| Attribute | Meaning |
|-----------|---------|
| `mode="bytes"` | The default. Each chunk is exactly `chunk=` bytes, and the last chunk is the remainder. |
| `mode="lines"` | Each newline-terminated line is one chunk, whatever its length. The newline belongs to the chunk it ends. |

| Child | Meaning |
|-------|---------|
| `<run>` | Optional. Runs once per chunk: the chunk is its stdin and its stdout replaces the chunk. A command, not a request. |
| `<cwd>` / `<stdin>` | Optional. The step's working directory, and its stdin when something other than the chunk should arrive. |

- **`chunk=` takes a size.** A plain byte count (`4096`) or a unit: `b`, `k`, `kb`, `m`, `mb`, `g`, `gb`, in any capitalization. `4kb`, `64k` and `4mb` all work. It is required in byte mode. An absent, unreadable or zero size is a load error that names the attribute. A boundary the author did not mean shows up much later as missing or doubled bytes.
- **A line longer than any buffer still arrives whole.** The chunker reads on until it reaches the newline. A final line with no newline is a chunk of its own.
- **The per-chunk step is how a stream is transformed.** It sees one chunk. A filter, a decoder, or a line marker therefore costs one chunk of memory rather than one source of memory. A step that exits non-zero fails the leaf with a non-zero exit and a message naming the chunk. The raw chunk is not emitted, because a stream that quietly drops a region reads as a shorter stream.
- **The step can name where it is.** `.stream.index` is the chunk's number, starting at 1. `.stream.offset` is the byte count before it. `.stream.size` is its own byte count.
- **Nothing is added.** The chunks concatenate to the source, byte for byte. Nothing is inserted between them, and no trailing newline is added. A binary source -- NUL bytes and invalid UTF-8 included -- comes back unchanged. The same declaration therefore carries a log, a PCM stream, or a video.
- **Peak memory follows the chunk size**, not the length of the source. A source that never ends is fine.
- **The source's exit code is the leaf's**, exactly as it is without `<stream>`.

```sh
# Every error line, as it lands.
api-cli tail /var/log/app.log | grep -i timeout

# Fixed-size parts of a stream, hashed as they arrive.
api-cli pull https://example.test/feed | sha256sum
```

- **`<stream>` is the leaf's output shape.** It therefore takes no `<fields>`, no `<format>` and no `<tml>`. The bytes are not records for a formatter to shape, and there is no whole body to render.
- **`--watch` does not apply**, because the stream already runs until its source ends.
- **`<download>` and `<stream>` cannot share a leaf.** Both are the leaf's action, and a run performs one action.
- **A `<transport>` program cannot carry it.** That path buffers the program's stdout. A request that travels over one therefore fails to load. Write `transport="http"` on that request, or drop `<stream>`.
- **`<response jq=>` cannot shape it either**, because that shapes a whole body at once. Leave `<response>` out so the body arrives as it is.
