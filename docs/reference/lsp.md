# Language server

`greenware lsp` serves one editor session over standard input and output, using the Language Server Protocol (JSON-RPC 2.0 with `Content-Length` framing). It uses only the Go standard library and the compiler's front end. It keeps no index across files, opens no network port, starts no other process, and ends when standard input closes.

```text
greenware lsp [--stdio] [--check-config] [--json]
```

1. `--stdio` is the default and the only transport. Standard output carries protocol messages only; the server writes its own notes to standard error.
2. `--check-config` prints the transport, the position encoding, the sync kind, the methods and the SHA-256 of the grammar, then exits. It starts no session (`started: false`). `--json` implies it and writes one `greenware.lsp/1` value with the keys `schema, transport, position_encodings, sync_kind, capabilities, grammar_sha256, started`.
3. Exit status: 0 when the input closed or `exit` followed `shutdown`; 1 when `exit` came with no `shutdown`; 2 when the input was not valid framing (a bad or missing `Content-Length`, a length over 64 MiB, a body that ends early) or a write failed.

## Editor setup

Any LSP client works. Start `greenware lsp --stdio` in the project folder for files that end in `.gw`, with the language id `greenware`. For Neovim:

```lua
vim.lsp.start({ name = "greenware", cmd = { "greenware", "lsp", "--stdio" },
  root_dir = vim.fs.root(0, { "greenware.mod", ".git" }) })
```

The server does not install a client for any editor.

## Documents and positions

1. The client's text wins over the file on disk. `didOpen` stores the text; `didClose` drops it and clears its diagnostics.
2. Sync is incremental (`textDocumentSync.change = 2`). Each range in a `didChange` is read against the text as it stands after the changes before it. The server converts the range to bytes and calls `lang.Tree.Reparse`; if that returns an error it parses the whole text again. A change with no range replaces the text.
3. Positions are UTF-16: `character` counts UTF-16 code units, so an emoji is two. The server offers no other position encoding. A `character` past the end of its line means the end of the line, a line past the end of the file means the end of the file, and a `character` inside a surrogate pair rounds up to the end of that character.
4. The project is the nearest folder above the file that holds `greenware.mod` (else the file's folder), found once at `didOpen`, so project imports and tables resolve as they do for `greenware check`. A document whose URI is not `file:` has no project.

## Methods

| Method | What it answers |
|---|---|
| `initialize`, `initialized`, `shutdown`, `exit` | The session. A request before `initialize` gets error -32002; a request after `shutdown` gets -32600. |
| `textDocument/didOpen`, `didChange`, `didClose` | Document text. Each open and change sends one `textDocument/publishDiagnostics`; a close sends an empty one. |
| `textDocument/publishDiagnostics` (sent) | Syntax errors, the lowering diagnostics and the evaluator's (`ast.LowerWith`, then `resolve.Evaluate` when the file parses), sorted and deduplicated, with their spans, codes (`GW-...`), severities and related spans. The server computes them at once on every change; debounce is the client's job. |
| `textDocument/formatting` | `lang.Format` as one edit that covers only the lines that changed, or no edit when the text is formatted. A file with syntax errors fails with -32803 and the first error. |
| `textDocument/codeAction` | Quick fixes (`quickfix`) from the edits that diagnostics carry, for the diagnostics that meet the range: one action for each alternative edit. `isPreferred` is set only when a diagnostic has a single edit. |
| `textDocument/hover` | A parameter: type, unit, default, range, bake or runtime, doc. A let: its source and its value at the defaults. A part, shape, socket or material: its kind and the evaluated fields, marking defaults. A template: its signature. An output: its file, levels and `set` values. A setting name: the registry's type, default, bounds and doc. A diagnostic under the cursor, or a code such as `GW-UNKNOWN-FIELD` written in a comment: the catalogue's doc line. |
| `textDocument/definition` | The declaration of a name: parameter, let, part, shape, material, template, socket, sample variable, template parameter, array `index`. |
| `textDocument/references` | Every use that resolves to the same declaration, and the declaration when `includeDeclaration` is set. |
| `textDocument/documentSymbol` | A nested outline: asset, parameters, lets, templates, materials, the part tree (with shapes, arrays, sockets and colliders; a mirror or `when` adds no level), outputs and the rig declarations (a `clip` is an event symbol). |
| `textDocument/completion` | In a block, at the start of a setting: the registry's fields, rows and child blocks for the enclosing kind. After `=` or an operator: the legal values of that field (enum members, `on` and `off`, axes, the materials or parts of a reference slot), then the names in scope and the builtin functions. At the top level: declaration keywords; after `param name :` the parameter types. The list is never marked incomplete. |

Every other request gets error -32601 (`MethodNotFound`). That includes `textDocument/rename`, `textDocument/semanticTokens/*`, `textDocument/foldingRange`, `textDocument/inlayHint`, `textDocument/signatureHelp`, `workspace/symbol` and `completionItem/resolve`. A notification the server does not know, such as `$/cancelRequest`, is ignored. Requests run one at a time in arrival order, so `$/cancelRequest` has nothing to cancel.

A panic in a handler is caught: the request gets error -32603 (`InternalError`), the panic goes to standard error, and the server goes on. Malformed JSON gets -32700 with a null id, params of the wrong shape get -32602.

## How names resolve

Definition, references and hover on a name use one index of the open document, built from the lowered file on first use after a change and kept until the next change. A use resolves among the declarations of its name whose scope holds it. The innermost scope wins, then the kind of declaration the slot reads (`material =` reads a material; `of`, `root` and `drop` read parts), so a part and a let with the same name stay apart. A local `let`, a template parameter, a sample variable and an array `index` are visible only in their own body. The index reads this one file: a name that an import brings in has no declaration here, so definition answers `null` for it.

## Limits

1. No semantic tokens. The editor's grammar (TextMate or tree-sitter) has to colour the source.
2. No rename, folding ranges, inlay hints, signature help or workspace symbols.
3. Diagnostics come from `resolve.Evaluate` at the default parameter values, not from `EvaluateAll`, so a fault that shows only under one output's `set` block appears in `greenware check` but not here.
4. Completion reads braces and the words before them, not the parse tree, so it works in text that does not parse yet. It can pick the wrong block kind for an unusual layout, such as a block header that spans lines.
5. Hover shows the values at the defaults. A value that depends on a runtime parameter is marked as such, not given as one number.

## Speed

`lsp/perf_test.go` generates a 100 KiB valid file (one asset and many parts with unique names, 102574 bytes), makes 200 incremental edits of one number and measures each. Run it with `go test ./lsp -run Performance -v`. The test asserts a ceiling of 100 ms for each figure so a slow machine does not fail it, and logs the real numbers. The table gives p95 over 200 runs, measured on 2026-11-20.

| Measure | Plan target | Linux x86-64 (chi-1) | Apple Silicon (arm64) |
|---|---|---|---|
| CST edit (`Tree.Reparse`) | p95 <= 20 ms | 19.2 ms | 16.0 ms |
| Incremental edit to published diagnostics | none | 22.6 ms | 18.7 ms |
| Cached hover, request to response | p95 <= 50 ms | 0.02 ms | 0.02 ms |
| First hover after an edit (builds the index) | none | 0.8 ms | 0.5 ms |

The CST edit is close to its target: most of its time is the copy of the parser's tree into `lang` nodes, not the parse. The first figure on chi-1 sat between 18.8 and 19.2 ms across runs, so the margin is small on a loaded machine.
