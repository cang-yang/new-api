#!/usr/bin/env bash
set -Eeuo pipefail

# This script is the forced command for the GitHub Actions deploy key. The key
# cannot request a shell or choose a compose project; it may only name an image
# digest built by this repository's image workflow.
request="${SSH_ORIGINAL_COMMAND:-$*}"
if [[ ! "$request" =~ ^deploy[[:space:]]sha256:[0-9a-f]{64}$ ]]; then
  echo 'invalid deployment request' >&2
  exit 2
fi

digest="${request##* }"
image="ghcr.io/cang-yang/new-api@${digest}"
project_dir=/opt/1panel/docker/compose/newapi
compose="${project_dir}/docker-compose.yml"
service=new-api

exec 9>/var/lock/newapi-personal-lab-deploy.lock
flock -x 9
cd "$project_dir"

if [[ ! -f "$compose" ]]; then
  echo 'deployment compose file not found' >&2
  exit 1
fi
previous_image="$(awk '$1 == "image:" && $2 ~ /^ghcr[.]io\/cang-yang\/new-api(:|@)/ { print $2 }' "$compose")"
if [[ -z "$previous_image" || "$previous_image" == *$'\n'* ]]; then
  echo 'expected exactly one new-api image in the compose file' >&2
  exit 1
fi
if [[ "$previous_image" == "$image" ]]; then
  echo "already deployed: $digest"
  exit 0
fi

# Pull before touching the running service. The old image remains available
# locally for rollback even if the mutable personal-lab tag has moved.
docker pull "$image"
target_id="$(docker image inspect --format '{{.Id}}' "$image")"
backup="${compose}.bak-auto-$(date -u +%Y%m%dT%H%M%SZ)"
cp -p "$compose" "$backup"
draft="$(mktemp "${project_dir}/.docker-compose.deploy.XXXXXX")"
trap 'rm -f "$draft"' EXIT
awk -v image="$image" '
  $1 == "image:" && $2 ~ /^ghcr[.]io\/cang-yang\/new-api(:|@)/ {
    sub(/image: .*/, "image: " image)
    matches++
  }
  { print }
  END { if (matches != 1) exit 2 }
' "$compose" > "$draft"
chmod --reference="$compose" "$draft"
mv "$draft" "$compose"

healthy() {
  for _ in {1..30}; do
    if [[ "$(docker inspect --format '{{.Image}}' "$service" 2>/dev/null || true)" == "$target_id" ]] &&
      curl -fsS --max-time 3 http://172.17.0.1:3000/api/status | grep -Eq '"success"[[:space:]]*:[[:space:]]*true'; then
      return 0
    fi
    sleep 2
  done
  return 1
}

if docker compose -f "$compose" config -q &&
  docker compose -f "$compose" up -d --no-deps --force-recreate "$service" &&
  healthy; then
  echo "deployed and healthy: $digest"
  exit 0
fi

echo 'new deployment failed; restoring previous compose and container' >&2
cp -p "$backup" "$compose"
docker compose -f "$compose" up -d --no-deps --force-recreate "$service"
echo "rolled back to $previous_image" >&2
exit 1
