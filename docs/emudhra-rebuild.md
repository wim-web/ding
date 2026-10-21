# eMudhra CA mitigation: rebuild and distribution

Status checked on 2026-10-21. Go's [security announcement](https://groups.google.com/g/golang-announce/c/66pojueaQek)
identifies Go 1.27.3 and 1.26.10 as the patched releases for CVE-2026-111943.
The fix in `crypto/x509` rejects chains containing the affected intermediate
certificates. See the [incident report](https://emudhra.we-have-a-problem.com/).

## Exposure and current status

`ding` uses Go's standard HTTPS client to send webhooks and to download updates.
The latest published release at the time of this check is
[v0.2.0](https://github.com/wim-web/ding/releases/tag/v0.2.0). Its Linux amd64
archive was checked against the published checksum, and `go version -m` reports
`go1.26.3`. It predates the mitigation and needs replacement by a newly built
release. Do not assume that changing the source or updating the OS trust store
patches an existing executable.

[PR #14](https://github.com/wim-web/ding/pull/14) updates both the test and release
workflows from Go 1.27.0 to 1.27.3. The `toolchain go1.27.3` directive also selects
the patched compiler for source builds with automatic toolchain selection;
`go 1.22` remains the language minimum. Explicit toolchain overrides can still
select an older compiler, so verify the resulting executable's build information.

There are no third-party Go dependencies or container build definitions in this
repository. The remediation is to rebuild with the patched Go standard library.
Keep normal certificate verification enabled.

## Maintainer checklist

1. Land PR #14 and the source build guidance before creating the new release tag.
   Confirm both workflows select Go 1.27.3 or a later patched version at that tag.
2. Run `go test ./...` and `go vet ./...` with the selected patched compiler.
3. Build all six existing release targets: macOS, Linux, and Windows, each for
   amd64 and arm64. Use the release workflow's `CGO_ENABLED=0`, `-trimpath`, and
   version/commit/date linker flags. Inspect **each** executable with
   `go version -m <binary>` and confirm the patched Go version before packaging.
4. Publish a **new version tag** and new release archives/checksums after approval.
   The release workflow publishes immediately on a `v*` tag push or manual
   dispatch. Do not run it merely to validate a build. A manual dispatch targeting
   an existing tag can overwrite that release's assets via `--clobber`.
5. Mention the CA mitigation in the release notes and replace the README's
   temporary installation notice with the new fixed release version.
6. Have users download and replace their installed executable, then verify its
   build information. `ding version` shows the application version, commit, and
   date, but does not identify the Go compiler.

Until a patched release exists, `ding update` still resolves to v0.2.0 and is not
a remediation. After publication, a direct download using an updated trusted
client avoids bootstrapping the update through the old executable. Applications
that embed or redistribute `ding` must also replace their bundled executable.

## Source build before release

From a checkout containing the remediation changes:

```sh
export GOTOOLCHAIN=go1.27.3
go version
go test ./...
go vet ./...
go build -trimpath -o ding .
go version -m ./ding
```

Only install this newly built executable after the build information confirms
the patched compiler. These commands do not publish or replace release assets.
