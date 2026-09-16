# Contributing to Dreamer

Thanks for helping improve Dreamer.

## Before you start

- Use Go 1.26.8 or the version declared in `go.mod`.
- For provider-specific work, install and authenticate the provider CLI you are testing.
- On Linux, sandbox integration work requires `bwrap`, unprivileged user namespaces, and the privileges described in [docs/SANDBOX.md](docs/SANDBOX.md).
- Read [docs/TESTING.md](docs/TESTING.md) before changing tests or concurrent code.

## Set up

```bash
git clone https://github.com/codewarnab/dreamer.git
cd dreamer
go mod download
make install-hooks
make build
make test
```

## Issues and pull requests

1. Search existing issues and pull requests before opening a new one.
2. Use an issue for bugs or changes that need design discussion. Do not report vulnerabilities publicly; follow [SECURITY.md](SECURITY.md).
3. Keep each pull request focused. Explain the problem, the approach, tests run, and user-visible changes.
4. Add or update tests and docs with behavior changes. Platform-specific changes should be tested on the affected platform when possible, and any untested behavior must be stated in the pull request.
5. Run the relevant checks before requesting review:

```bash
make fmt
make vet
make test
make test-race   # required for concurrency changes
make lint
make vulncheck
make quality
```

CI, reviewer feedback, and unresolved security or correctness concerns must be clear before merge. Maintainers may ask for changes or split an oversized pull request.

## Sign-off and licensing

Dreamer does not currently require a CLA or DCO sign-off. By contributing, you agree that your contribution is licensed under the repository's [MIT License](LICENSE).

## Conduct

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
