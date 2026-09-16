# Support and platform status

Dreamer is under active development and has not published a stable release. The latest commit on `main` is the supported development version.

## Platform matrix

| Platform | Release binary | CI validation | Current limits |
|---|---:|---:|---|
| Linux amd64 | Built | Tests, race detector, lint, and vulnerability scan | Sandbox requires `bwrap` and unprivileged user namespaces; seccomp filtering is amd64-only. |
| Linux arm64 | Built | Compile/test matrix | No seccomp filtering. Sandbox still requires `bwrap` and unprivileged user namespaces. |
| Windows amd64 | Built | Compile/test matrix | Network isolation is not available. |
| Windows arm64 | Built | Compile/test matrix | Uses the documented no-sandbox fallback; network isolation is not available. |
| macOS amd64 | Built | Compile/test matrix | Sandbox uses Apple's deprecated `sandbox-exec`; daemon auto-start is not installed. |
| macOS arm64 | Built | Compile/test matrix | Sandbox uses Apple's deprecated `sandbox-exec`; daemon auto-start is not installed. |

A built binary is not a promise that every sandbox or service-management feature has the same guarantees on every platform. See [docs/SANDBOX.md](docs/SANDBOX.md) and [docs/SECURITY.md](docs/SECURITY.md) for details.

## Getting help

Use [GitHub Issues](https://github.com/codewarnab/dreamer/issues) for reproducible bugs and focused feature requests. Include the Dreamer version or commit, operating system and architecture, provider, relevant logs with secrets removed, and minimal reproduction steps.

Do not report vulnerabilities in a public issue. Follow [SECURITY.md](SECURITY.md).
