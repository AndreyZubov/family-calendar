#!/usr/bin/env bash
# Enables the repository's security features on GitHub. Run once after
# creating the repository (needs `gh` logged in with admin rights):
#
#   scripts/github-security-settings.sh AndreyZubov/family-calendar
#   scripts/github-security-settings.sh AndreyZubov/family-calendar --dry-run
#
# What it turns on (all free for public repositories):
#   - Secret scanning: alerts when a token/key is committed.
#   - Push protection: rejects pushes that contain a recognized secret.
#   - Dependabot alerts: notices about vulnerable dependencies.
#   - Dependabot security updates: automatic PRs that fix them.
#   - Private vulnerability reporting: the "Report a vulnerability" button
#     that SECURITY.md points to.
#   - Merge hygiene: squash merges only (PR title becomes the commit message,
#     keeping Conventional Commits for the changelog), delete merged branches.
#
# Branch protection for main is set up separately (deployment step).
set -euo pipefail

REPO="${1:?usage: $0 owner/repo [--dry-run]}"
DRY_RUN="${2:-}"

run() {
  if [[ "$DRY_RUN" == --dry-run ]]; then
    printf '[dry-run] %s\n' "$*"
  else
    "$@" >/dev/null
    printf '  ✓ %s\n' "${*: -1}"
  fi
}

echo "Repository: $REPO"

echo "Secret scanning and push protection"
run gh api -X PATCH "repos/$REPO" --input - <<'JSON'
{
  "security_and_analysis": {
    "secret_scanning": { "status": "enabled" },
    "secret_scanning_push_protection": { "status": "enabled" }
  }
}
JSON

echo "Dependabot alerts and security updates"
run gh api -X PUT "repos/$REPO/vulnerability-alerts"
run gh api -X PUT "repos/$REPO/automated-security-fixes"

echo "Private vulnerability reporting (SECURITY.md)"
run gh api -X PUT "repos/$REPO/private-vulnerability-reporting"

echo "Merge settings"
run gh api -X PATCH "repos/$REPO" \
  -F allow_squash_merge=true -F allow_merge_commit=false -F allow_rebase_merge=false \
  -f squash_merge_commit_title=PR_TITLE -f squash_merge_commit_message=PR_BODY \
  -F delete_branch_on_merge=true -F allow_auto_merge=true

if [[ "$DRY_RUN" != --dry-run ]]; then
  echo
  echo "Current state:"
  gh api "repos/$REPO" --jq '"  secret scanning: \(.security_and_analysis.secret_scanning.status)\n  push protection: \(.security_and_analysis.secret_scanning_push_protection.status)\n  squash only: \(.allow_squash_merge and (.allow_merge_commit|not) and (.allow_rebase_merge|not))\n  delete merged branches: \(.delete_branch_on_merge)"'
fi
