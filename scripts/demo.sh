#!/usr/bin/env bash
#
# Boot a throwaway imvault, seeded with a small but representative instance, so
# you can look at everything without registering by hand.
#
# Nothing here touches your real ./data directory: it wipes and uses
# ./demo-data instead (gitignored), which `make clean-demo` removes.
#
#   make demo
#   PORT=9000 make demo
#
# The administrator is provisioned with the local CLI. Members and content are
# seeded through the signup form, API, report form, and admin pages.
#
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

PORT="${PORT:-8080}"
DATA_DIR="${DATA_DIR:-$PWD/demo-data}"
BASE="http://127.0.0.1:$PORT"
PASSWORD="${PASSWORD:-demo-password}"

ADMIN="demo"
MEMBER="freya"
MODERATOR="mod"

JAR="$(mktemp)"
WORK="$(mktemp -d)"
BINARY="$WORK/imvault"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -f "$JAR"
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

command -v go >/dev/null 2>&1 || die "go is not on PATH"

# Do not stomp on something already listening.
if curl -fsS -o /dev/null "$BASE/healthz" 2>/dev/null; then
  die "something is already serving $BASE; try PORT=9000 make demo"
fi

say "Resetting the demo data directory ($DATA_DIR)"
rm -rf "$DATA_DIR"

say "Building imvault"
go build -o "$BINARY" ./cmd/imvault

say "Provisioning the demo administrator"
printf '%s\n' "$PASSWORD" | IMVAULT_DATA_DIR="$DATA_DIR" \
  "$BINARY" create-admin --username "$ADMIN" --password-stdin \
  || die "could not provision the demo administrator"

say "Starting the server on $BASE"
IMVAULT_ADDR="127.0.0.1:$PORT" \
IMVAULT_DATA_DIR="$DATA_DIR" \
IMVAULT_BASE_URL="$BASE" \
IMVAULT_LOG_LEVEL=warn \
  "$BINARY" &
SERVER_PID=$!

for _ in $(seq 1 50); do
  curl -fsS -o /dev/null "$BASE/healthz" 2>/dev/null && break
  sleep 0.2
done
curl -fsS -o /dev/null "$BASE/healthz" 2>/dev/null || die "the server did not start"

# --- the small helpers a person would be doing in a browser ------------------

csrf() { awk '/imvault_csrf/ {print $7}' "$1" | tail -1; }

# account_form signs in or registers and leaves the session in the named jar.
account_form() { # account_form <login|register> <username> <jar>
  local action="$1" user="$2" jar="$3"
  rm -f "$jar"
  curl -sS -c "$jar" -b "$jar" -o /dev/null "$BASE/"
  curl -sS -c "$jar" -b "$jar" -o /dev/null -X POST "$BASE/$action" \
    -d "csrf_token=$(csrf "$jar")" --data-urlencode "username=$user" --data-urlencode "password=$PASSWORD"
}

# mint_key creates an API key through the settings page, the way a person does.
mint_key() { # mint_key <jar> <label>
  curl -sS -c "$1" -b "$1" -o "$WORK/panel.html" -X POST "$BASE/settings/api-keys" \
    -H 'HX-Request: true' -d "csrf_token=$(csrf "$1")" -d "name=$2"
  grep -o 'value="imv_[A-Za-z0-9]*_[A-Za-z0-9]*"' "$WORK/panel.html" | head -1 \
    | sed 's/value="//;s/"//'
}

# me returns the id of whichever account owns a key.
me() { # me <api-key>
  curl -sS -H "Authorization: Bearer $1" "$BASE/api/v1/me" \
    | tr -d ' ' | grep -oE '"id":[0-9]+' | grep -oE '[0-9]+' | head -1
}

# upload posts one file and returns the response.
upload() { # upload <api-key> <file> <visibility> [extra form fields...]
  local key="$1" file="$2" visibility="$3"
  shift 3
  local extra=()
  for field in "$@"; do extra+=(-F "$field"); done
  curl -sS -X POST "$BASE/api/v1/upload" \
    -H "Authorization: Bearer $key" \
    -F "files=@$file" -F "visibility=$visibility" "${extra[@]}"
}

# id_of pulls the file or album id out of an API response. The encoder indents,
# so the space after the colon is real.
id_of() { printf '%s' "$1" | grep -o '"id": "[a-z0-9]*"' | head -1 | sed 's/.*: "//;s/"//'; }
slug_of() { printf '%s' "$1" | grep -o '"slug": "[a-z0-9-]*"' | head -1 | sed 's/.*: "//;s/"//'; }

add_to_album() { # add_to_album <api-key> <album-slug> <file-id>
  curl -sS -o /dev/null -X POST "$BASE/api/v1/albums/$2/files" \
    -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
    -d "{\"files\":[\"$3\"]}"
}

web() { # web <jar> <path> [form fields...]
  local jar="$1" path="$2"
  shift 2
  local fields=(-d "csrf_token=$(csrf "$jar")")
  for field in "$@"; do fields+=(-d "$field"); done
  curl -sS -c "$jar" -b "$jar" -o /dev/null -X POST "$BASE$path" \
    -H 'HX-Request: true' "${fields[@]}"
}

# --- the instance -----------------------------------------------------------

say "Signing in as $ADMIN, the administrator"
account_form login "$ADMIN" "$JAR"
ADMIN_KEY="$(mint_key "$JAR" 'demo script')"
[[ -n "$ADMIN_KEY" ]] || die "could not mint an API key for $ADMIN"

say "Creating $MEMBER, an ordinary member"
MEMBER_JAR="$WORK/member.jar"
account_form register "$MEMBER" "$MEMBER_JAR"
MEMBER_KEY="$(mint_key "$MEMBER_JAR" 'demo script')"
[[ -n "$MEMBER_KEY" ]] || die "could not mint an API key for $MEMBER"

say "Creating $MODERATOR, who will be given the moderator role"
MOD_JAR="$WORK/mod.jar"
account_form register "$MODERATOR" "$MOD_JAR"
MOD_KEY="$(mint_key "$MOD_JAR" 'demo script')"
[[ -n "$MOD_KEY" ]] || die "could not mint an API key for $MODERATOR"

MOD_ID="$(me "$MOD_KEY")"
[[ -n "$MOD_ID" ]] || die "could not look $MODERATOR up"
web "$JAR" "/admin/users/$MOD_ID/role" "role=moderator"

say "Generating sample media"
mkdir -p "$WORK/media"
go run ./scripts/genmedia -dir "$WORK/media"

# A clip makes the video path visible, but it is entirely optional.
if command -v ffmpeg >/dev/null 2>&1; then
  if ffmpeg -hide_banner -loglevel error \
      -f lavfi -i "testsrc=size=480x360:rate=15" -t 3 \
      -c:v libvpx -pix_fmt yuv420p -y "$WORK/media/clip.webm" 2>/dev/null; then
    printf '  %s\n' "$WORK/media/clip.webm"
  else
    warn "ffmpeg could not encode a sample clip; skipping it"
  fi
else
  warn "ffmpeg not found; skipping the sample clip (clips would get a placeholder poster)"
fi

say "Uploading, as each account, at each visibility"

# The administrator's own, all public. holiday.jpg carries a location, so its
# details are worth opening on the file page and worth comparing with a
# stranger's view of the same page.
GRADIENT_ID=""
HOLIDAY_ID=""
for spec in "gradient.jpg:public" "holiday.jpg:public"; do
  file="${spec%%:*}"; visibility="${spec##*:}"
  response="$(upload "$ADMIN_KEY" "$WORK/media/$file" "$visibility")"
  id="$(id_of "$response")"
  [[ -n "$id" ]] || die "upload of $file failed: $(printf '%s' "$response" | head -c 200)"
  case "$file" in
    gradient.jpg) GRADIENT_ID="$id" ;;
    holiday.jpg) HOLIDAY_ID="$id" ;;
  esac
done

if [[ -f "$WORK/media/clip.webm" ]]; then
  upload "$ADMIN_KEY" "$WORK/media/clip.webm" public >/dev/null || warn "the clip did not upload"
fi

upload "$ADMIN_KEY" "$PWD/internal/web/static/img/mascot.png" public >/dev/null \
  || warn "the mascot upload did not take"

# The member's own, members-only, so the badges and the feed have something that
# is not public in them.
ANIMATION_ID=""
TRANSPARENT_ID=""
for spec in "transparent.png:members" "animation.gif:members"; do
  file="${spec%%:*}"; visibility="${spec##*:}"
  response="$(upload "$MEMBER_KEY" "$WORK/media/$file" "$visibility")"
  id="$(id_of "$response")"
  [[ -n "$id" ]] || die "upload of $file failed: $(printf '%s' "$response" | head -c 200)"
  case "$file" in
    transparent.png) TRANSPARENT_ID="$id" ;;
    animation.gif) ANIMATION_ID="$id" ;;
  esac
done

# One private upload of the same bytes as a public one, so the gallery shows the
# third level and the de-duplication behind it at the same time.
upload "$ADMIN_KEY" "$WORK/media/gradient.jpg" private >/dev/null \
  || warn "the private upload did not take"

say "Making an album two accounts contribute to"
SUMMER="$(slug_of "$(curl -sS -X POST "$BASE/api/v1/albums" \
  -H "Authorization: Bearer $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d '{"title":"Summer","description":"Everybody who was there put something in","visibility":"members","access":"members"}')")"
[[ -n "$SUMMER" ]] || die "could not create the shared album"
# Each contributor adds their own: an album decides who may add, never whose
# files may be added.
[[ -n "$HOLIDAY_ID" ]] && add_to_album "$ADMIN_KEY" "$SUMMER" "$HOLIDAY_ID"
[[ -n "$ANIMATION_ID" ]] && add_to_album "$MEMBER_KEY" "$SUMMER" "$ANIMATION_ID"

say "Tagging a few uploads"
if [[ -n "$HOLIDAY_ID" ]]; then
  for tag in "holiday" "sunset"; do
    curl -sS -o /dev/null -X POST "$BASE/api/v1/files/$HOLIDAY_ID/tags" \
      -H "Authorization: Bearer $ADMIN_KEY" -H 'Content-Type: application/json' \
      -d "{\"name\":\"$tag\"}"
  done
fi
if [[ -n "$TRANSPARENT_ID" ]]; then
  curl -sS -o /dev/null -X POST "$BASE/api/v1/files/$TRANSPARENT_ID/tags" \
    -H "Authorization: Bearer $MEMBER_KEY" -H 'Content-Type: application/json' \
    -d '{"name":"graphic"}'
fi

say "Issuing an invitation"
curl -sS -c "$JAR" -b "$JAR" -o "$WORK/invite.html" -X POST "$BASE/admin/invites" \
  -H 'HX-Request: true' -d "csrf_token=$(csrf "$JAR")" \
  -d "label=for a friend" -d "max_uses=1" -d "expires_days=14"
INVITE="$(grep -o 'value="inv_[A-Za-z0-9]*_[A-Za-z0-9]*"' "$WORK/invite.html" | head -1 \
  | sed 's/value="//;s/"//')"

say "Filing a report, and dealing with another"
# The one the moderator deals with, filed first so it is the oldest in the
# queue and therefore the one "the first open report" refers to.
if [[ -n "$HOLIDAY_ID" ]]; then
  web "$MEMBER_JAR" "/reports" "target_kind=file" "target_id=$HOLIDAY_ID" \
    "reason=copyright" "note=pretty sure I took this one"

  REPORT_ID="$(curl -sS -b "$MOD_JAR" "$BASE/moderation" \
    | grep -o '/moderation/reports/[0-9]*/resolve' | head -1 | sed 's|.*/reports/||;s|/resolve||')"
  if [[ -n "$REPORT_ID" ]]; then
    web "$MOD_JAR" "/moderation/reports/$REPORT_ID/resolve" \
      "action=dismiss" "resolution=checked with both of them; it is fine"
  else
    warn "no report was waiting to resolve"
  fi
fi

# And one left waiting, so the queue has something in it.
if [[ -n "$GRADIENT_ID" ]]; then
  web "$MEMBER_JAR" "/reports" "target_kind=file" "target_id=$GRADIENT_ID" \
    "reason=spam" "note=this looks like an advert"
fi

# --- what to look at --------------------------------------------------------

bold() { printf '\033[1m%s\033[0m' "$*"; }

printf '\n  %s\n\n' "$(bold 'imvault is running')"
printf '    %-14s %s\n' \
  'Open'     "$BASE" \
  'Sign in'  "$ADMIN / $PASSWORD  (administrator)" \
  ''         "$MEMBER / $PASSWORD  (member)" \
  ''         "$MODERATOR / $PASSWORD  (moderator)"

printf '\n  %s\n\n' "$(bold 'Worth looking at')"
cat <<EOF
    /recent           what everybody shared, newest first
    /gallery          your own uploads
    /albums           yours, and the ones shared with you
    /a/summer         an album two accounts have put things into
    /tags             a per-account tag index

    /admin            accounts, roles, invitations, settings
    /admin/users      the role picker, and who is what
    /admin/settings   the three instance profiles
    /moderation       one report still waiting
    /moderation/log   a dismissal the moderator recorded

EOF
if [[ -n "$HOLIDAY_ID" ]]; then
cat <<EOF
    /f/$HOLIDAY_ID
                      open "Photo details": you see the location, and a
                      logged-out visitor sees the same photograph without it

EOF
fi
if [[ -n "$INVITE" ]]; then
  printf '    Invitation        %s\n' "$INVITE"
  printf '                      register with it at %s/register\n\n' "$BASE"
fi
printf '    API key           %s\n' "$ADMIN_KEY"
printf '\n  The admin was provisioned locally; members and content used HTTP.\n'
printf '  Data lives in %s; "make clean-demo" removes it.\n' "$DATA_DIR"
printf '\n  Press Ctrl-C to stop.\n\n'

wait "$SERVER_PID"
