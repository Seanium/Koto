# Desktop development

See the [development guide](../docs/development.md) for prerequisites, frontend compilation, tests, synthetic sessions, and packaging boundaries.

The desktop and CLI share the Go core in this directory. `frontend/` contains the React UI. `build/` and `scripts/` contain platform build and packaging tasks. `env.sh` selects a project-local toolchain once it has been provisioned; the toolchain itself is not included in the source tree.

The application assets and their provenance are listed in [ASSETS.md](ASSETS.md).
