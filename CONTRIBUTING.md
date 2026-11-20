# Contributing to Greenware

Greenware is pre-1.0. The language, the compiler and the file format change without notice, so a change can break your files and your tools. Read `CHANGELOG.md` before you start.

Read `README.md` and `AGENTS.md` first. `AGENTS.md` holds the full rules; this page summarises the ones that matter to contributors. Specs, plans and reports are not kept in this repository. `AGENTS.md` names the Hyphae space where they live. Do not commit them here.

## Build and test

Run these commands from the repository root. The root `go.work` links the nested modules `det` and `gltf` to their local folders.

Set `CGO_ENABLED=0`, as `AGENTS.md` asks.

1. Build and vet the root module: `go build ./...` and `go vet ./...`.
2. Test the root module: `go test ./...`. This includes `internal/depcheck`, which must pass.
3. Test `det`: `(cd det && go test ./...)`.
4. Test `gltf`: `(cd gltf && go test ./...)`.
5. Test `conformance`: `(cd conformance && GOWORK=off go test ./...)`. The `go.work` does not list `conformance`, so this module needs `GOWORK=off`.

Maintainers run heavy suites on remote machines, as `AGENTS.md` describes.

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
