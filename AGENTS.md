# Working in this repository

1. Greenware depends on no game engine. Never import GoSX or Kiln from the core. `go test ./internal/depcheck` must pass.
2. Output is deterministic: no map iteration order, wall clock, random seed or floating-point fusion may reach a written byte. Use `det` for scalar math that reaches output.
3. Specs, plans, reports and decisions live in the Hyphae space `hypha://m31labs/greenware`, not here.
4. Stage and commit by path: `buckley commit --yes --minimal-output --backend claude --paths <files>`. Never `git add -A`.
5. Build with `CGO_ENABLED=0`. Run heavy tests with `tailnet-run` or `local-heavy`.

6. Development uses `go.work` (root, `det/`, `gltf/`, with replaces to the local folders). Consumers do not see it: the root `go.mod` requires the tagged `det/v0.1.0` and `gltf/v0.1.0`. Release builds run with `GOWORK=off`.
