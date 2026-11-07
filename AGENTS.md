# Repository Agent Instructions

## Repository structure

- This repository contains reusable agent skills.
- `chnq/` is the Code Health and Quality skill.
- The Go module for `chnq` is `chnq/go.mod`; it is not at the repository root.
- Keep skill-specific implementation and documentation inside the relevant skill
  directory. Keep repository-wide documentation at the root.

## Development workflow

Use the top-level Makefile from the repository root:

```sh
make test
make vet
make build
```

The default Go project directory is `chnq`. Override it with
`PROJECT_DIR=/path/to/project` when needed.

## Go code changes

- Preserve the analyzer's deterministic, read-only behavior.
- Use the standard library unless a dependency is explicitly required.
- Follow [Effective Go](https://go.dev/doc/effective_go) and
  [Uber's Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)
  for all Go development.
- Run `make test` and `make vet` after Go changes.
- Keep the module path in `chnq/go.mod` consistent with package imports.
- Do not commit generated files from `chnq/.build/`.

## Documentation changes

- Update the root `README.md` for repository-level information.
- Update `chnq/SKILL.md` for the skill's agent-facing behavior.
- Keep usage examples accurate and runnable from the repository root.

## Git hygiene

- Do not commit secrets, local editor settings, build output, or profiling
  artifacts.
- Make focused changes and avoid unrelated formatting or refactoring.
