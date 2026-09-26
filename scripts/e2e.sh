#!/bin/sh
# End-to-end hook test in an isolated HOME with a stub audio player.
# usage: scripts/e2e.sh [path/to/faaa]
set -u
BIN=$(cd "$(dirname "${1:-./faaa}")" && pwd)/$(basename "${1:-./faaa}")
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
export HOME="$T" XDG_CONFIG_HOME= XDG_DATA_HOME= XDG_CACHE_HOME= GIT_CONFIG_NOSYSTEM=1
printf '#!/bin/sh\nfor a; do f=$a; done\necho "PLAYED $(basename "$f" .wav)" >> %s/log\n' "$T" > "$T/stub"
chmod +x "$T/stub"
export FAAA_PLAYER="$T/stub"
git config --global user.email t@t
git config --global user.name t
git config --global init.defaultBranch main

fail=0
check() { # name expected-plays [expected-sound]
	sleep 0.5
	n=$(grep -c PLAYED "$T/log" 2>/dev/null)
	if [ "${n:-0}" -eq "$2" ] && { [ -z "${3:-}" ] || grep -q "PLAYED $3\$" "$T/log"; }; then
		echo "ok   $1"
	else
		echo "FAIL $1 (plays=$n want $2 ${3:-}; log: $(tr '\n' ' ' < "$T/log" 2>/dev/null))"; fail=1
	fi
	: > "$T/log"
}

# user already has global hooks under ~ : must keep running after install
mkdir -p "$HOME/.githooks"
printf '#!/bin/sh\necho GLOBAL >> %s/global\ncat >/dev/null\n' "$T" > "$HOME/.githooks/pre-push"
chmod +x "$HOME/.githooks/pre-push"
git config --global core.hooksPath '~/.githooks'

"$BIN" install >/dev/null || exit 1
git init -q --bare "$T/remote.git"
git clone -q "$T/remote.git" "$T/a" 2>/dev/null
git clone -q "$T/remote.git" "$T/b" 2>/dev/null
cd "$T/a"
printf '#!/bin/sh\necho LOCAL >> %s/local\ncat >/dev/null\n' "$T" > .git/hooks/pre-push
chmod +x .git/hooks/pre-push

echo 1 > f && git add f && git commit -qm 1 && git push -q origin main
check "push plays" 1
# config mode leaves the user's global hooksPath in charge (which already
# disables .git/hooks); hookspath mode must chain .git/hooks itself.
if [ "$(git config --global --get core.hooksPath)" != '~/.githooks' ]; then
	[ -s "$T/local" ] && echo "ok   repo pre-push chained" || { echo "FAIL repo pre-push not chained"; fail=1; }
fi

[ -s "$T/global" ] && echo "ok   previous ~ global hook chained" || { echo "FAIL previous global hook not chained"; fail=1; }

git fetch -q
check "fetch silent" 0

(cd "$T/b" && git pull -q origin main && echo 2 > g && git add g && git commit -qm 2 && git push -q origin main)
check "other clone push plays" 1

echo 3 > h && git add h && git commit -qm 3 && git push -q origin main 2>/dev/null
check "rejected push silent" 0

"$BIN" fail bruh >/dev/null
git push -q origin main 2>/dev/null
check "rejected push plays fail sound" 1 bruh
"$BIN" fail off

git fetch -q
check "fetch after rejected push silent" 0

git pull -q --rebase origin main && git push -q origin main
check "push after rebase plays" 1

printf 'vine-boom\n' > .faaa
echo 5 > j && git add j && git commit -qm 5 && git push -q origin main
check "repo .faaa anthem plays" 1 vine-boom
rm .faaa

echo 6 > k && git add k && git commit -qm 6 && FAAA_MUTE=1 git push -q origin main
check "FAAA_MUTE=1 silences one push" 0

"$BIN" off && echo 4 > i && git add i && git commit -qm 4 && git push -q origin main
check "muted push silent" 0
"$BIN" on

# repo with its own core.hooksPath (husky) + tag push (unverifiable -> plays)
# (hookspath mode can't see these: faaa doctor warns instead)
if [ "${FAAA_HOOKSPATH:-}" != 1 ]; then
	git config core.hooksPath .husky
	git tag v1 && git push -q origin v1
	check "tag push in husky-style repo plays" 1
	git config --unset core.hooksPath
fi

"$BIN" uninstall >/dev/null
[ "$(git config --global --get core.hooksPath)" = '~/.githooks' ] && echo "ok   uninstall restores config" || { echo "FAIL uninstall"; fail=1; }
exit $fail
