# Releases and installation

## Supported targets

Release archives contain a static `freeagent` binary for:

- Linux amd64 and arm64.
- macOS amd64 and arm64.
- Windows amd64 and arm64.

Linux and macOS use `tar.gz`; Windows uses ZIP. Each GitHub release includes a
SHA-256 checksum file and an SPDX JSON SBOM for each archive.

Each archive also contains the repository-scoped agent skill at
`.agents/skills/freeagent-cli`. Copy that directory to
`.agents/skills/freeagent-cli` in the agent's repository when the skill is not
already present.

## Install from GitHub

1. Open the repository's [Releases](https://github.com/timbrinded/freeagent-cli/releases).
2. Download the archive for the operating system and architecture.
3. Download `checksums.txt` and the matching SBOM.
4. Verify the archive's SHA-256 digest.
5. Extract the binary and put it on `PATH`.
6. Run `freeagent version --output json`.

Set `ARCHIVE_NAME` to the exact archive filename that you downloaded. Verify
only that entry from the aggregate checksum file.

On Linux:

```sh
grep "  ${ARCHIVE_NAME}$" checksums.txt | sha256sum --check -
```

On macOS:

```sh
grep "  ${ARCHIVE_NAME}$" checksums.txt | shasum -a 256 -c -
```

On Windows PowerShell:

```powershell
$ArchiveName = "freeagent_0.1.0_windows_amd64.zip"
$Pattern = '^(?<hash>[0-9a-fA-F]{64})  ' + [regex]::Escape($ArchiveName) + '$'
$ChecksumLines = @(Get-Content checksums.txt | Select-String -Pattern $Pattern)
if ($ChecksumLines.Count -ne 1) { throw "Expected one checksum entry" }
$Expected = $ChecksumLines[0].Matches[0].Groups['hash'].Value.ToLowerInvariant()
$Actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $ArchiveName).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw "Checksum mismatch" }
```

Require one matching checksum line. Then verify the archive's GitHub build
provenance with GitHub CLI:

```sh
gh attestation verify "$ARCHIVE_NAME" --repo timbrinded/freeagent-cli
```

## Install from source

With Go 1.26.7 or later:

```sh
go install github.com/timbrinded/freeagent-cli/cmd/freeagent@v0.1.0
```

Replace the version with the required release. Avoid `@latest` in repeatable
automation.

## Version policy

Tags use Semantic Versioning and have the form `vMAJOR.MINOR.PATCH`.

Before version 1.0:

- Patch releases fix compatible defects.
- Minor releases can add commands and optional schema or capability fields.
- Release notes identify migrations.

Protocol version 1 starts with CLI version 0.1.0. Incompatible machine-output,
exit-code, or retry-semantics changes require a new protocol major and a CLI
major release.

## Maintainer release process

1. Confirm that the worktree is clean and the tag commit is on the release
   branch.
2. Update `CHANGELOG.md` with a date and migration notes.
3. Run all CI, race, vulnerability, synthetic HTTP contract, skill, and
   snapshot release checks. If a dedicated sandbox is available, also run and
   record the opt-in live checks with their API version.
4. Inspect every generated archive and its `version` output.
5. Create and push an annotated `vMAJOR.MINOR.PATCH` tag.
6. Let the tag-triggered GitHub Actions workflow run GoReleaser.
7. Verify the public release assets, checksums, SBOMs, and provenance.
8. Test one downloaded archive independently.

Do not publish a release that embeds OAuth credentials or contains real API
fixtures.
