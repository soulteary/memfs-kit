# Releasing memfs-kit

The release version is stored in the root `VERSION` file, without a `v` prefix.
Release notes live in `.github/releases/v<version>.md`. For example, version
`1.1.0` uses `.github/releases/v1.1.0.md` and the Git tag `v1.1.0`.

## Prepare the release pull request

Choose the next version for the public API changes. For example, v1.1.0 adds
the `Stat` and `ReadDir` methods and their standard optional interfaces, so it
is a minor release. The Go module path stays `github.com/soulteary/memfs-kit`.
Performance changes that preserve the existing API and behavior use a patch
version, such as v1.1.1.

Update `VERSION` and create its matching release-notes file. Describe new
capabilities, compatibility considerations, requirements, and validation. Update
both READMEs to match, and link performance claims to their benchmark conditions.
Keep previously published release notes unchanged. Generated quality reports
are maintained by CI rather than edited during release preparation.

Before merging, verify that the version, notes filename, and versioned install
command agree and that the pull request's CI passes. Preparing the branch and
pull request does not create a tag or release.

## Publish through main CI

Merging the release-preparation pull request to `main` is the publication
trigger. The Release workflow runs after the main-branch CI workflow completes
successfully. Pull-request CI never publishes a release. A preparation request
can stop at the reviewed pull request; merge only when ready to publish.
Only successful CI for a push to the default branch triggers publication;
manually dispatching CI refreshes reports but does not publish a version.

The publisher checks that the default branch still matches the tested source.
It also accepts one direct report-only commit from `github-actions[bot]`, so
the release can include the verified README badges. Any unrelated branch
advance skips publishing until its own CI succeeds.

The workflow creates a new tag at that exact commit and publishes the matching
GitHub Release using the checked-in notes. Existing releases, including drafts,
are left unchanged. An existing tag can be reused only when it already points
at the selected commit; tags are never moved or overwritten.

The release job uses the repository's `GITHUB_TOKEN` with `contents: write` and
`actions: read`. It does not run project code or require a separate personal
access token. Release jobs are serialized. If a publish attempt fails, inspect
its logs and rerun the failed Release workflow; do not delete or move a public
version tag to retry it.

This project is a Go library. GitHub's source archives and the Go module tag
are its release artifacts; no application binaries are built. Keep `LICENSE`,
`LICENSES/memfs-BSD-3-Clause.txt`, and `THIRD_PARTY_NOTICES.md` in the released
source. The v1 module path remains `github.com/soulteary/memfs-kit`.

## Verify publication

A successful Release workflow can have skipped publication because the default
branch advanced or the release already exists. Verify the actual public Release
and tag before announcing completion:

- The requested tag is published as a stable, non-draft Release and is latest.
- Peel the tag to its commit. It must be the successful main push CI source or
  its permitted report-only bot child, as described above.
- Read `VERSION`, `go.mod` and the release-notes file from that tag. The version,
  installation command and published release body must agree.
- Confirm that the Go module can be downloaded by its versioned module path.
  A newly published version can take time to appear on the public proxy.
