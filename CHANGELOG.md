# Changelog

This project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Before version 1.0, minor releases can add commands and optional schema or
capability fields. Protocol version 1 is stable from CLI version 0.1.0;
incompatible machine-output, exit-code, or retry-semantics changes require a
new protocol major and a CLI major release.

## Unreleased

No changes yet.

## 0.1.0 - 2026-08-28

The first public preview release.

### Added

- Initial Go CLI structure.
- Named sandbox and production profiles.
- OAuth authorization-code login with operating-system keyring storage.
- Human and machine-readable output modes.
- Runtime capability and schema discovery.
- Guarded mutation and unknown-outcome policies.
- Repository Agent Skill and public documentation.
