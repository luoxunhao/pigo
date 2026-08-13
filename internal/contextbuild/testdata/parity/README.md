# contextbuild parity corpus

This directory is pigo's self-held golden corpus for the contextbuild pipeline
(issue-029, SPEC 07). Each fixture freezes the expected provider-visible output
for a deterministic scenario so future changes are diffable:

- `projection.json`: compaction retained-tail, stopReason filtering, custom
  EntryProjector, lane.config state, and active-tool resolution.
- `convert.json`: provider-independent role conversion (compaction,
  branch_summary, custom -> user) and image downgrade.
- `system-prompt.json`: section order and byte-exact system-prompt assembly
  (base -> tools -> guidelines -> append -> context files -> skills ->
  environment/cwd, no date).

The corpus is anchored to the pi behavior described in
`tasks/spec-contextbuild-pi-alignment.md`. `piCommit` records the pigo commit the
fixtures were generated at; `deviations` registers deliberate differences
(Go provider abstraction, extension registration mechanics, storage metadata).

## Refresh

Regenerate the expected outputs from current behavior:

```powershell
powershell -ExecutionPolicy Bypass -File internal/contextbuild/testdata/parity/refresh.ps1
```

or directly:

```powershell
go test ./internal/contextbuild -run TestParity -update
```

After a refresh, run `go test ./internal/contextbuild -run TestParity` to verify
the corpus is green. Unregistered deviations fail the test; registered ones are
documented in the fixture files.
