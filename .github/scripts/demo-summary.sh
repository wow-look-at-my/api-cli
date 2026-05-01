#!/usr/bin/env bash
# Generates a markdown showcase of github.example.json into
# $GITHUB_STEP_SUMMARY: for each public endpoint, the raw (URL-stripped)
# JSON response followed by the formatted (table/detail) view.
#
# CONFIDENTIALITY: every endpoint here is a *public* GitHub resource
# (octocat, golang/go, cli/cli, plus the rate-limit endpoint which only
# returns numeric counters). The script never echoes headers, env vars,
# or auth tokens; api-cli's command rendering only sends headers, never
# prints them.

set -euo pipefail

bin=./build/api-cli
cfg=github.example.json
sum="${GITHUB_STEP_SUMMARY:-/dev/stdout}"

if [[ ! -x "$bin" ]]; then
    echo "demo: binary not found at $bin (did go-toolchain run first?)" >&2
    exit 1
fi
if [[ ! -f "$cfg" ]]; then
    echo "demo: config not found at $cfg" >&2
    exit 1
fi

# demo TITLE -- ARGV...
# Renders one section: header, raw response inside a collapsed <details>,
# then the formatted view inside an open <details>.
demo() {
    local title="$1"
    shift
    {
        echo
        echo "### \`$bin $*\`"
        echo
        echo "$title"
        echo
        echo "<details><summary><strong>Raw response</strong> (URL keys stripped, otherwise unmodified JSON)</summary>"
        echo
        echo '```json'
        "$bin" --config "$cfg" "$@" --no-format
        echo '```'
        echo
        echo "</details>"
        echo
        echo "<details open><summary><strong>Formatted view</strong></summary>"
        echo
        echo '```'
        "$bin" --config "$cfg" "$@" --format=always
        echo '```'
        echo
        echo "</details>"
    } >> "$sum"
}

{
    echo "# api-cli + GitHub demo"
    echo
    echo "Live read-only run of [\`github.example.json\`](../blob/${GITHUB_SHA:-HEAD}/github.example.json) against public GitHub endpoints. Each section shows two things:"
    echo
    echo "1. **Raw response** — what arrives after the recursive \`jq\` filter strips every key ending in \`url\`. Still valid JSON; this is what \`--no-format\` (or piping output) gives you."
    echo "2. **Formatted view** — what you see on a terminal, rendered through the format/views system in \`api-cli\`."
    echo
    echo "All targets are public (octocat, golang/go, cli/cli). No private state, no auth tokens, no headers are echoed."
    echo
} >> "$sum"

demo "Single user (object → \`detail\` view)." \
    user get octocat

demo "Single repository." \
    repo get golang/go

demo "Open issues on \`cli/cli\` (slice → \`table\` view)." \
    repo issues cli/cli --state open -n 5

demo "Language byte-count breakdown for \`golang/go\` (object with custom percentages view)." \
    repo languages golang/go

demo "Recent commits on \`golang/go\`." \
    repo commits golang/go -n 5

demo "Recent releases on \`cli/cli\`." \
    repo releases cli/cli -n 3

demo "Repository search with sort + pagination." \
    search repos "language:go stars:>10000" --sort stars -n 5

demo "Rate limit (numeric counters only — no user identity)." \
    rate-limit
