# Release process

## Release workflow

The release workflow defined in [`.github/workflows/release.yaml`](./.github/workflows/release.yaml)
will be triggered whenever a tag that matches `v[0-9]+\.[0-9]+\.[0-9]+.*` (examples of valid tags:
`v0.3.0`, `v0.3.1-rc2`) is created. The workflow invokes the `make build-static` command to build a binary, creates and pushes Docker image.
## Make a Release

To make a release, create an tag at the commit you want. For example

```sh
git tag -a "v0.3.1"
git push upstsream HEAD # You must have admin permission for the repo.
```

Github Action will detect the newly created tags and trigger the workflow. Check [Action Runs](https://github.com/tetratelabs/istio-registry-sync/actions)
and watch for its completion.

Once workflow completes, go to [Release](https://github.com/tetratelabs/istio-registry-sync/releases)
page and draft the release notes, provide Docker image links.
