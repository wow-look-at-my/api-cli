url="$1"; state="$2"
id="${url##*/}"
case "$url" in */file/*) printf 'data-%s' "$id"; exit 0;; esac
case "$id" in
flaky)
	n=$(cat "$state/flaky" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "$state/flaky"
	if [ "$n" -le 2 ]; then printf '<html>502 Bad Gateway</html>'; echo "proxy said 502" >&2; exit 1; fi
	printf '{"status":"done","url":"https://jobs.example/file/flaky"}';;
stuck) printf '{"status":"pending"}';;
login) printf '<!DOCTYPE html><html><title>Sign in</title></html>';;
*) printf '{"status":"done","url":"https://jobs.example/file/%s"}' "$id";;
esac
