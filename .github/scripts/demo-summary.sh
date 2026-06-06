#!/usr/bin/env bash
# Generates a markdown showcase of samples/github/github.yaml into
# $GITHUB_STEP_SUMMARY. For each public endpoint, the script captures
# THREE outputs and emits them with byte counts so the savings are
# obvious:
#
#   1. RAW            — the unfiltered GitHub API response. Bloated with
#                       `*_url` template links and `url`/`html_url`
#                       self-links. This is what you get if you `curl`
#                       the API directly.
#   2. URL-STRIPPED   — same response after our `jq` walk drops every
#                       key ending in `url`. Still valid JSON; this is
#                       what `api-cli ... --no-format` (or piping the
#                       output) gives you.
#   3. FORMATTED      — the table/detail view rendered through the
#                       format/views system in `api-cli`.
#
# CONFIDENTIALITY: every endpoint is a *public* GitHub resource
# (octocat, golang/go, cli/cli, plus the rate-limit endpoint which only
# returns numeric counters). The script never echoes headers, env vars,
# or auth tokens; api-cli's command rendering only sends headers, never
# prints them.

set -euo pipefail

bin=./build/api-cli
# CI builds via `go-toolchain matrix`, which emits per-platform binaries
# (api-cli_<os>_<arch>) instead of a bare build/api-cli; fall back to the
# linux/amd64 binary the ubuntu runner uses.
[[ -x "$bin" ]] || bin=./build/api-cli_linux_amd64
cfg=samples/github/github.yaml
sum="${GITHUB_STEP_SUMMARY:-/dev/stdout}"

if [[ ! -x "$bin" ]]; then
    echo "demo: binary not found at $bin (did go-toolchain run first?)" >&2
    exit 1
fi
if [[ ! -f "$cfg" ]]; then
    echo "demo: config not found at $cfg" >&2
    exit 1
fi

# Returns the byte count of the file at $1, comma-separated for readability.
bytes_of() {
    LC_ALL=en_US.UTF-8 printf "%'d" "$(wc -c < "$1")" 2>/dev/null \
        || wc -c < "$1" | tr -d ' '
}

# Returns "X% smaller" relative to a baseline; e.g. pct_smaller 6103 2047.
pct_smaller() {
    awk -v big="$1" -v small="$2" 'BEGIN {
        if (big <= 0) { print "n/a"; exit }
        printf "%.0f%% smaller", (big - small) / big * 100
    }'
}

# demo TITLE -- ARGV...
# Renders one section: header, three output blocks (raw / stripped /
# formatted), each preceded by a byte count.
demo() {
    local title="$1"
    shift

    local raw stripped formatted
    raw=$(mktemp)
    stripped=$(mktemp)
    formatted=$(mktemp)

    GITHUB_RAW=1 "$bin" --config "$cfg" "$@" --no-format > "$raw"
    "$bin" --config "$cfg" "$@" --no-format > "$stripped"
    "$bin" --config "$cfg" "$@" --format=always > "$formatted"

    local raw_b strip_b fmt_b
    raw_b=$(bytes_of "$raw")
    strip_b=$(bytes_of "$stripped")
    fmt_b=$(bytes_of "$formatted")

    {
        echo
        echo "### \`api-cli $*\`"
        echo
        echo "$title"
        echo
        echo "| | bytes | vs raw |"
        echo "|---|---:|---:|"
        echo "| Raw API response | $raw_b | (baseline) |"
        echo "| URL-stripped JSON (\`--no-format\`) | $strip_b | $(pct_smaller "$(wc -c < "$raw")" "$(wc -c < "$stripped")") |"
        echo "| Formatted view (\`--format=always\`) | $fmt_b | $(pct_smaller "$(wc -c < "$raw")" "$(wc -c < "$formatted")") |"
        echo
        echo "<details><summary><strong>Raw API response</strong> &mdash; this is the unfiltered JSON GitHub returns. Notice how much of every object is just <code>*_url</code> noise.</summary>"
        echo
        echo '```json'
        cat "$raw"
        echo '```'
        echo
        echo "</details>"
        echo
        echo "<details><summary><strong>URL-stripped JSON</strong> &mdash; same data, with every key ending in <code>url</code> removed.</summary>"
        echo
        echo '```json'
        cat "$stripped"
        echo '```'
        echo
        echo "</details>"
        echo
        echo "<details open><summary><strong>Formatted view</strong> &mdash; what you actually see on a terminal.</summary>"
        echo
        echo '```'
        cat "$formatted"
        echo '```'
        echo
        echo "</details>"
    } >> "$sum"

    rm -f "$raw" "$stripped" "$formatted"
}

{
    echo "# api-cli + GitHub demo"
    echo
    repo="${GITHUB_REPOSITORY:-wow-look-at-my/api-cli}"
    sha="${GITHUB_SHA:-HEAD}"
    echo "Live read-only run of [\`samples/github/github.yaml\`](https://github.com/${repo}/blob/${sha}/samples/github/github.yaml) against public GitHub endpoints. Each section shows three things, side-by-side, so the response bloat is obvious:"
    echo
    echo "1. **Raw API response** &mdash; what \`curl\` returns straight from \`api.github.com\`. Roughly half the bytes (often more) are \`*_url\` template links you almost never want on the CLI. Reproduce with \`GITHUB_RAW=1 api-cli --config samples/github/github.yaml <cmd> --no-format\`."
    echo "2. **URL-stripped JSON** &mdash; the same response after a tiny \`jq\` walk drops every key ending in \`url\`. This is the default for \`api-cli --config samples/github/github.yaml\`; pass \`GITHUB_RAW=1\` to opt out. Reproduce with \`api-cli --config samples/github/github.yaml <cmd> --no-format\`."
    echo "3. **Formatted view** &mdash; rendered through the \`api-cli\` format/views system into a table or detail block. Reproduce with \`api-cli --config samples/github/github.yaml <cmd> --format=always\`."
    echo
    echo "The headers below show only \`<cmd>\` (the subcommand and its args); \`--config\`, \`--no-format\`, and \`--format=always\` are implicit per-section as listed above."
    echo
    echo "All targets are public (\`octocat\`, \`golang/go\`, \`cli/cli\`). No private state, no auth tokens, no headers are echoed."
    echo
} >> "$sum"

demo "Single user (object &rarr; \`detail\` view)." \
    user get octocat

demo "Single repository &mdash; the per-object bloat is most extreme here; a typical repo response carries ~30 \`*_url\` template links." \
    repo get golang/go

demo "Open issues on \`cli/cli\` (slice &rarr; \`table\` view)." \
    repo issues cli/cli --state open -n 5

demo "Language byte-count breakdown for \`golang/go\` (object &rarr; custom byte/percent table)." \
    repo languages golang/go

demo "Recent commits on \`golang/go\`." \
    repo commits golang/go -n 5

demo "Recent releases on \`cli/cli\`." \
    repo releases cli/cli -n 3

demo "Repository search with sort + pagination." \
    search repos "language:go stars:>10000" --sort stars -n 5

demo "Rate limit (numeric counters only &mdash; no user identity)." \
    rate-limit
