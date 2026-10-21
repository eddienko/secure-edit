# Development

```sh
go vet ./...
go test ./...
```

## Project layout

`sedit` is one program with two front ends, the interface and the command line,
over a few small packages that know nothing about either:

| Package | What it does |
|---------|--------------|
| `cmd/sedit` | The program. It decides between the two front ends: a bare `sedit`, or one folder, opens the interface; anything else is the command line. |
| `internal/cli` | The command line: argument parsing, prompts, messages, and the commands (edit, share, import, keygen, ...). |
| `internal/tui` | The interface: file list, locked preview, the dialogs, the filter, and folders. |
| `internal/vault` | The file format (XChaCha20-Poly1305 with an Argon2id key), titles and descriptions (files', and folders' hidden label file), the per-file edit lock, atomic writes, backups. Knows nothing about prompts, editors or the Keychain. |
| `internal/keystore` | Remembering passwords outside the files: the macOS Keychain, or nothing elsewhere. |
| `internal/unlock` | Opening a file with whichever password is available: its own stored one, then the default, then one the caller asks the user for. |
| `internal/editor` | Running your editor on a private temp file, with the extra protection and cues for vim. |
| `internal/fileops` | Moving, deleting and re-keying a file together with its backup and remembered password; making, moving and deleting folders. |
| `internal/clip` | The system clipboard (`pbcopy` and friends), and copying for a limited time. |
| `internal/version` | The version string, set by the release build. |

`vault`, `keystore`, `editor` and `clip` don't depend on each other; `unlock` and
`fileops` use `keystore` and `vault`; `cli` and `tui` use them all, and never each
other. Both are tested without a terminal: `cli` through its functions, `tui` on a
simulated screen.

## Publishing a new version

Releases are built by [`.github/workflows/release.yml`](../.github/workflows/release.yml)
whenever a tag starting with `v` is pushed. To publish one:

1. Make sure everything is committed and pushed to `main`, and that the tests
   pass locally:

   ```sh
   go vet ./... && go test ./...
   ```

2. Pick the next version number using [semantic versioning](https://semver.org):
   `vMAJOR.MINOR.PATCH`. Bump PATCH for fixes, MINOR for new features, and MAJOR
   for incompatible changes (including changes to the file format).

3. Tag the commit and push the tag:

   ```sh
   git tag -a v1.2.3 -m "sedit v1.2.3"
   git push origin v1.2.3
   ```

4. Watch the run on the repository's **Actions** tab. It runs `go vet` and the
   tests on Linux and macOS, then builds tarballs for macOS and Linux (arm64 and
   amd64), writes `SHA256SUMS`, and creates the GitHub release with generated
   notes. You can edit the notes afterwards on the release page.

A tag containing a hyphen, such as `v1.3.0-rc1`, is published as a pre-release.

To try the build without publishing anything, run the same script locally. It
writes the tarballs and checksums to `dist/` (git-ignored):

```sh
scripts/build-release.sh v0.0.0-test
```

The version printed by `sedit --version` comes from the tag. Don't reuse or move
a tag that has been published: the Go module proxy caches versions permanently,
so a moved tag causes checksum errors for anyone who already fetched it. Publish
a new version instead.
