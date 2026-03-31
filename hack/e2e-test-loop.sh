#!/usr/bin/env bash
# Run `make e2e-test` ten times sequentially; save one log per run.
#
# Survive terminal disconnect (run from repo root):
#   nohup ./hack/e2e-test-loop.sh > ./e2e-loop.out 2>&1 &
#   disown
#
# Optional: E2E_RUNS=10 (default 10) to change count.

set -u

readonly TOTAL_RUNS="${E2E_RUNS:-10}"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
readonly SESSION_DIR="${REPO_ROOT}/e2e-loop-reports/session-$(date +%Y%m%d-%H%M%S)"

mkdir -p "${SESSION_DIR}"

summary_file="${SESSION_DIR}/summary.txt"
{
  echo "repo: ${REPO_ROOT}"
  echo "started: $(date -Iseconds)"
  echo "total_runs: ${TOTAL_RUNS}"
  echo "---"
} | tee "${summary_file}"

for ((i = 1; i <= TOTAL_RUNS; i++)); do
  report="${SESSION_DIR}/run-$(printf '%02d' "${i}").log"
  {
    echo "=============================================="
    echo "e2e loop run ${i}/${TOTAL_RUNS}"
    echo "started: $(date -Iseconds)"
    echo "=============================================="
  } | tee -a "${report}"

  (
    cd "${REPO_ROOT}" && make e2e-test
  ) 2>&1 | tee -a "${report}"
  rc=${PIPESTATUS[0]}

  {
    echo "finished: $(date -Iseconds)"
    echo "exit_code: ${rc}"
    echo ""
  } | tee -a "${report}"

  echo "run ${i}/${TOTAL_RUNS} exit_code=${rc} report=${report}" | tee -a "${summary_file}"
done

{
  echo "---"
  echo "all_done: $(date -Iseconds)"
  echo "session_dir: ${SESSION_DIR}"
} | tee -a "${summary_file}"

echo "Finished. Reports under: ${SESSION_DIR}"
