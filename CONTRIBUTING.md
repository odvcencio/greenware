# Contributing to Greenware

Greenware is pre-1.0. The language, the compiler and the file format change without notice, so a change can break your files and your tools. Read `CHANGELOG.md` before you start.

Read `README.md` and `AGENTS.md` first. `AGENTS.md` holds the full rules; this page summarises the ones that matter to contributors. Specs, plans and reports are not kept in this repository. `AGENTS.md` names the Hyphae space where they live. Do not commit them here.

## Build and test

Set `CGO_ENABLED=0` and run `go build -mod=readonly ./...`, `go vet -mod=readonly ./...` and `go test -mod=readonly -short -count=1 ./...` from the checkout root. Follow the same commands inside `det/` and `gltf/`. The workspace links both modules to their local source.

CI repeats the checks with `GOWORK=off` in the root, `det/`, `gltf/` and `conformance/`. The standalone root resolves the tagged modules. Run the full suite with `local-heavy go test -count=1 ./...` or the repository's remote test runner. The short suite keeps the small rigging and solver fixtures. The full suite also executes the historical athlete bakes and the solver protocol.

### Rigid-prop evidence

Run `python3 scripts/rigid-evidence.py qualify /path/to/new-packet`. The packet contains the compiled files and receipts, acceptance results, independent oracle reports, controls, and the work that remains before sign-off. A local Khronos glTF Validator `2.0.0-dev.3.10` can be passed with `--validator /path/to/gltf_validator`. The command checks the executable's SHA-256 before running it. The Linux amd64 executable has a default pin. For another platform pass the verified executable digest with `--validator-sha256`. [Khronos release binaries and license](https://github.com/KhronosGroup/glTF-Validator/releases/tag/2.0.0-dev.3.10)

To reproduce the native byte check, build the CLI and run `python3 scripts/rigid-evidence.py witness /path/to/new-witness --greenware /path/to/greenware`. On two native runners, compare the resulting manifests with `python3 scripts/rigid-evidence.py compare <amd64-manifest> <arm64-manifest>`. The manifests cover the goal post, helmet and arm GLBs and their build records.

## Rules for every change

- **No game engine in the core.** The core never imports GoSX or Kiln. `internal/depcheck` checks the import closure.
- **Deterministic output.** No written byte may depend on map iteration order, the wall clock, a random seed or float fusion. Sort map keys before they reach output.
- **No float fusion.** On arm64, Go can merge `x*y + z` into one rounding. On amd64 it does not. Write each multiply that feeds an add or a subtract as `float64(x*y) + z`. Use `det` for scalar math that reaches output. `det`'s `TestNoUnwrappedMultiply` fails on a bare multiply.
- **The conformance checker stays independent.** `conformance/` reads no Greenware, Kiln or GoSX code (see `conformance/NOTES.md`). It must not import the compiler's code. Check the imports before you commit.

## Changing the approved goal post digests

`testdata/compiler/approved/examples-goalpost.provisional.sha256` lists the digests of the six files that `examples/goalpost/goalpost.gw` builds. `TestProvisionalBaseline` in `compiler/examples_test.go` compares a fresh build with that file. The file has no update flag. A change in output bytes needs a new review.

1. Run `go test ./compiler -run TestProvisionalBaseline` to see which digest changed.
2. Find the cause of the change in the compiler or the source.
3. Ask a reviewer to approve the new digests.
4. After the review, update the approved file in its own commit. Say in the message which review approved it.

## Changing the grammar

The grammar is a Go program in `language/grammar`. Two files come from it: `lang/greenware.bin`, the parser, and `docs/reference/grammar.ebnf`, the reference.

1. Edit the grammar in `language/grammar`.
2. From the repository root, run `go run ./cmd/greenware-grammar -bin lang/greenware.bin -ebnf docs/reference/grammar.ebnf`. You can run `go generate ./lang` instead.
3. Commit the grammar change and both generated files together.

If you skip step 2, `go test ./cmd/greenware-grammar` fails in `TestCommittedFilesMatchTheGrammar`. The failure says to run `go generate ./lang`.

## Commits

- Make one change per commit.
- Say why in the message. The subject says what changed. The body says why.
- Stage files by path, for example `git add cmd/greenware/main.go`. Do not use `git add -A` or `git commit -a`.

## Licence

Greenware is MIT licensed (`LICENSE`, copyright M31 Labs). By contributing, you agree that your contribution is licensed under the MIT licence. Assets that people author with Greenware declare their own licences (see `README.md`).
