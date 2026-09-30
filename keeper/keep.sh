#!/usr/bin/env bash
# keep.sh: one keeper run, from a checkout of this repository. Fetches every
# slot in keeper.json inside a container on the podman network fovea-keeper
# (an IPv6 route to the IPv6-only stations, and none to the machine's
# localhost-only services), then commits and pushes what it kept. The
# scheduled timer and the manual workflow both run exactly this.
#
#   RUNTIME        podman (the timer) or docker (the workflow's runner shim)
#   CGROUP_PARENT  a slice for the container, if the runtime does not set one
#
# Exits non-zero when the keeper or the push fails, after committing whatever
# the run logged, so a failure is loud and its fetch log line is still kept.
set -euo pipefail
cd "$(dirname "$0")/.."
FOVEA_VERSION=v0.2.0
IMAGE=docker.io/library/golang:1.27.0@sha256:4013ae0f9e7994f8535c58c811f8f863fbed38b72e0d51e6592156f758d66146
runtime=${RUNTIME:-podman}
cgroup=()
[ -n "${CGROUP_PARENT:-}" ] && cgroup=(--cgroup-parent="$CGROUP_PARENT")

mkdir -p records endorsements
status=0
"$runtime" run --rm "${cgroup[@]}" --network fovea-keeper \
    -v "$PWD:/w" -v fovea-keeper-go:/go/pkg/mod -w /w -e FOVEA_VERSION="$FOVEA_VERSION" "$IMAGE" sh -ec '
      go install "github.com/macula-io/macula-fovea/cli/cmd/fovea@$FOVEA_VERSION"
      (cd keeper && go build -o /usr/local/bin/keeper .)
      git clone --quiet https://github.com/macula-io/mcl-fovea-assessments.git /tmp/assessments
      keeper -config keeper.json -root . -fovea "$(go env GOPATH)/bin/fovea" -assessments /tmp/assessments' \
  || status=$?

git -c user.name="fovea keeper" -c user.email="fovea-keeper@users.noreply.github.com" add -- records endorsements
if ! git diff --cached --quiet; then
  git -c user.name="fovea keeper" -c user.email="fovea-keeper@users.noreply.github.com" \
      commit --quiet -m "keep: $(date -u +%Y-%m-%dT%H:%MZ)"
  pushed=0
  for attempt in 1 2 3; do
    if git push --quiet; then pushed=1; break; fi
    git pull --quiet --rebase
  done
  [ "$pushed" = 1 ] || { echo "keep.sh: push failed after 3 attempts" >&2; status=1; }
fi
[ "$status" = 0 ] || echo "keep.sh: the run failed (exit $status); see the fetch logs" >&2
exit "$status"
