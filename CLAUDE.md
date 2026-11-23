# CLAUDE.md

Launch subagents for each task and use the cheapest adequate model each time.

Before starting any task the user gives, ask: "Is that it, or do you have more for this commit?" Wait for the answer, then start, and put everything the user listed into one commit.

## Release numbers

- The next release is the latest released `vX.Y.Z` tag with the patch number plus one (after v2.0.2, the next is 2.0.3), unless the owner says otherwise (e.g. 2.1.0).
- Put new CHANGELOG.md entries straight under `## [X.Y.Z] - YYYY-MM-DD` for that next release, never under "Unreleased", so the release workflow finds the notes when the owner tags. Set the date to the day of the release.
