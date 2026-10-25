# Go report policy

The report integration follows [health-kit's CI at commit `4c8be735`](https://github.com/soulteary/health-kit/blob/4c8be735ad9da47b2a46c3458071911578f51fb2/.github/workflows/ci.yml),
using **Go Test Report v1.1.2** and **Go Report Card v1.1.3**, with their Actions
pinned to the corresponding release commits. Reports are produced by the normal
CI workflow so successful changes to `main` update the README badges.

## Production and validation

- The Linux race job runs Go Test Report once to produce test results, atomic
  race-enabled coverage, the Markdown and JSON test reports, and the coverage
  badge. Coverage reporting reuses that run's profile without repeating tests.
  Toolchain auto-selection is allowed while the Action bootstraps its CLI; it
  then selects the project's `go.mod` before running tests.
- The existing formatting, module consistency, `go vet`, Linux/macOS/Windows
  tests, and bounded path fuzzing remain enabled. Fuzzing runs before Go Report
  Card, using the project's selected Go toolchain.
- Go Report Card runs with `GOTOOLCHAIN=auto` for its checker tools. All six
  checkers must be present: `gofmt`, `go_vet`, `ineffassign`, `gocyclo`, `license`,
  and `misspell`. A missing checker, tool error, or incomplete report fails
  validation and blocks publishing, even if a badge was generated.
- There is no new minimum coverage percentage or numerical Go Report Card
  threshold. Existing test failures still fail CI.
- Generated files from earlier commits are removed before producing new
  evidence. Stable reports and raw test evidence are retained as artifacts for
  **14 days**, including available diagnostics when a report step fails.

## Publishing

Report-producing jobs have read-only repository permissions. A separate writer
job waits for every required CI job to succeed and downloads the stable artifact
from the same workflow run and producer attempt; it does not rerun project tests.

Only a push to `main` or a manual workflow run selected on `main` may publish.
The writer checks that the tested source SHA still equals the default branch's
HEAD before writing. If the branch has advanced, it leaves the reports available
as artifacts without replacing newer repository contents. Pull requests and
manual runs on other refs never write reports.

The writer receives `contents: write` and `actions: read` permissions and may
commit only these five files:

- `.github/go-test-report.md`
- `.github/go-test-report.json`
- `.github/coverage.svg`
- `.github/goreportcard.svg`
- `.github/goreportcard-report.md`

Writeback is serialized and uses an ordinary push, never a force push. A branch
update after the SHA check rejects the push instead of overwriting concurrent
work. No tag or release is created by the report workflow.
