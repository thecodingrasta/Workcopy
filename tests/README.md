# Integration test fixtures

The tests use disposable local Git repositories. `fixtures/large-monorepo` is a checked-in source-only .NET monorepo fixture with 25 projects and more than 125 tracked paths. `internal/testutil.NewFixtureRepository` copies it to a temporary directory, initializes Git, and commits the source before a test invokes Workcopy. The checked-in fixture intentionally has no `.git` directory.

The fixture backs two different test seams:

- `internal/workspace/TestTenOfTwentyFiveProfileAndRepeatedExpansion` checks the committed daily profile, root files, Git-free lane contents, project expansion without losing edits, and generated solution-filter selection.
- `tests/acceptance/TestLargeMonorepoSupportsThreeIndependentAgentLanes` runs the compiled CLI against three independently selected projects, prepares and explicitly commits reviews, integrates them with fixture Git operations, synchronizes each lane, and destroys the clean lanes.

Run the tests with:

```sh
go test -count=1 ./internal/workspace -run '^TestTenOfTwentyFiveProfileAndRepeatedExpansion$'
go test -count=1 ./tests/acceptance -run '^TestLargeMonorepoSupportsThreeIndependentAgentLanes$'
```

These tests exercise Git and Workcopy's .NET project discovery/filtering. They do not restore packages or claim that a selected profile builds with a particular installed .NET SDK.
