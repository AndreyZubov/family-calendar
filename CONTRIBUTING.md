# Contributing to Family Calendar

Thanks for your interest in contributing! This document explains how to propose
changes.

By participating in this project you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Reporting bugs and requesting features

- Search [existing issues](../../issues) before opening a new one.
- Use the issue templates: they ask for the information needed to act on a report.
- **Security vulnerabilities must not be reported in public issues.** See
  [SECURITY.md](SECURITY.md).

For larger changes, please open an issue first so the approach can be discussed
before you spend time on code.

## Development setup

See [Getting started](README.md#getting-started) in the README. In short:

```sh
make db-up
make migrate-up
make dev
```

## Making changes

1. Fork the repository and create a branch from `main`:
   `git checkout -b feat/short-description`
2. Make your change. Keep pull requests focused on one thing.
3. Add or update tests for the behavior you changed.
4. Make sure everything passes locally:
   ```sh
   make lint
   make test
   ```
5. If you changed SQL queries or the OpenAPI spec, regenerate code with
   `make generate` and commit the result.
6. Open a pull request and fill in the template.

### Code style

- **Go:** `gofmt`/`goimports` formatting, checked by `golangci-lint`.
  Keep packages under `backend/internal/`.
- **TypeScript/React:** formatted with Prettier, checked by ESLint.
- Database changes go in a new migration (`make migrate-create name=...`).
  Never edit a migration that has already been released.

### Commit messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/).
Commit messages drive the changelog and version numbers.

```
<type>(<optional scope>): <description>

feat(events): allow adding participants to an event
fix(auth): reject callback with mismatched state
docs: describe deployment on a VPS
```

Common types: `feat`, `fix`, `docs`, `refactor`, `test`, `build`, `ci`, `chore`.
Mark breaking changes with `!` after the type (`feat!: ...`) or a
`BREAKING CHANGE:` footer.

## Pull request review

- CI must pass before a pull request can be merged.
- A maintainer will review your pull request. Please respond to feedback by
  pushing new commits; the branch is squash-merged at the end.

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE).
