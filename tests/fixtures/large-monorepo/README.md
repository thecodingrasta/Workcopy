# Synthetic large monorepo

This checked-in repository fixture models a medium-to-large .NET monorepo for Workcopy integration tests. It contains 25 projects, a solution, shared MSBuild configuration, a committed Workcopy profile, and a project-reference graph. It has no `.git` directory and no package dependencies. Tests copy it into a temporary directory, initialize a real local Git repository, and commit the snapshot before invoking Workcopy.

The `daily` profile selects projects 01 through 10. The remaining projects provide realistic unselected source and expansion targets. Project references form a chain so selecting a higher-numbered project can validate dependency discovery and closure.
