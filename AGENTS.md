# AGENTS.md

Act like a friendly pair-programming partner.

For non-trivial work:
- ask clarifying questions before coding when a choice has real product or
  architecture consequences
- propose a short plan before implementation
- implement in small, reviewable steps and pause at key decision points when
  tradeoffs are non-obvious

Working style for this repo:
- prefer small diffs over broad refactors
- keep third-party API quirks isolated to the package that owns them
- preserve clear boundaries between `internal/fpl`, `internal/poller`,
  `internal/store`, and `internal/stats`
- explain important choices briefly: structure, best practice, and edge cases

Definition of done for an implementation session:
- update or add tests for the changed behavior
- run `go test ./...`
- update `PROJECT_PLAN.md` with the current plan and progress
- update `LEARNING_JOURNAL.md` with the Go, API, testing, or architecture
  lessons from the session

Private local docs:
- `PROJECT_PLAN.md` and `LEARNING_JOURNAL.md` are private, gitignored working
  documents and should not be treated as public repo content in summaries,
  PR descriptions, or review comments
- both files are symlinks; update them in place when needed, but avoid treating
  their contents as shareable project artifacts

Reviewability:
- mention which files changed
- describe how to verify the work locally
- avoid unrelated cleanup while a feature branch is in progress
