#!/usr/bin/env bash
# Assert that each platform in a pushed multi-arch image carries THAT
# platform's binary.
#
# A wrong-arch binary is invisible to every other check: the manifest still
# says linux/arm64, the push succeeds, and the image only fails at `docker run`
# on real hardware nobody in CI has. So read the bytes -- pull each platform's
# layers, find the entrypoint binary, and compare its ELF machine to the
# platform the manifest claims.
#
# Usage: verify-image-arch.sh <registry/repo> <tag>
set -euo pipefail

repo="${1:?usage: verify-image-arch.sh <registry/repo> <tag>}"
tag="${2:?usage: verify-image-arch.sh <registry/repo> <tag>}"
registry="${repo%%/*}"
path="${repo#*/}"
binary=usr/local/bin/api-cli

# e_machine, 16-bit LE at offset 18 of the ELF header.
declare -A elf_machine=( [3e00]=amd64 [b700]=arm64 )

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

token_url="https://${registry}/token?scope=repository:${path}:pull&service=${registry}"
token=""
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    token="$(curl -fsS -u "${GITHUB_ACTOR:-x}:${GITHUB_TOKEN}" "$token_url" 2>/dev/null | jq -r .token || true)"
    [[ -n "$token" ]] || echo "verify-image-arch: credentialed pull token refused, retrying anonymously" >&2
fi
if [[ -z "$token" ]]; then
    token="$(curl -fsS "$token_url" | jq -r .token)"
fi
accept='application/vnd.oci.image.index.v1+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.docker.distribution.manifest.v2+json'
fetch() { curl -fsS -H "Authorization: Bearer $token" -H "Accept: $accept" "$@"; }

fetch "https://${registry}/v2/${path}/manifests/${tag}" > "$work/index.json"

platforms="$(jq -r '.manifests[]? | select(.platform.architecture != "unknown")
                    | "\(.platform.architecture) \(.digest)"' "$work/index.json")"
if [[ -z "$platforms" ]]; then
    echo "verify-image-arch: ${repo}:${tag} advertises no platforms; expected a multi-arch index" >&2
    exit 1
fi

failed=0
while read -r arch digest; do
    fetch "https://${registry}/v2/${path}/manifests/${digest}" > "$work/manifest.json"

    found=""
    while read -r layer; do
        fetch -L "https://${registry}/v2/${path}/blobs/${layer}" -o "$work/layer.tgz"
        rm -rf "$work/x" && mkdir "$work/x"
        tar -xzf "$work/layer.tgz" -C "$work/x" "$binary" 2>/dev/null || continue
        machine="$(od -An -tx1 -j18 -N2 "$work/x/${binary}" | tr -d ' \n')"
        found="${elf_machine[$machine]:-unknown(e_machine=$machine)}"
        break
    done < <(jq -r '.layers[].digest' "$work/manifest.json")

    if [[ -z "$found" ]]; then
        echo "FAIL  linux/${arch}: no ${binary} in any layer" >&2
        failed=1
    elif [[ "$found" != "$arch" ]]; then
        echo "FAIL  linux/${arch}: image carries ${found}" >&2
        failed=1
    else
        echo "ok    linux/${arch}: ${found} binary"
    fi
done <<< "$platforms"

exit "$failed"
