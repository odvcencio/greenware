# Command reference

`greenware` is one static, pure-Go executable. This page lists every command, its flags and its exit status. The examples run against the files in `testdata/tools/`: a test (`cmd/greenware/tools_doc_test.go`) runs each `$ greenware ...` line from the repository root and compares standard output with the text shown. A line may end with `# exit N` when the command exits with a status other than 0. A final `...` line means the real output goes on. Examples change nothing on disk.

## Shared rules

1. A flag can come before or after the operands. `--flag value` and `--flag=value` both work. `--` ends the flags.
2. `--json` makes a command write exactly one JSON value to standard output, with a `schema` key first and the keys in a fixed order. Text for people goes to standard output in plain mode and diagnostics go to standard error.
3. Exit status: 0 success; 1 findings (an error diagnostic, a refused or failed gate, a drift, a conflict); 2 a usage or read error (a bad flag, a missing file, an unknown diagnostic code).
4. Every command here runs the front end only (parse, lower, evaluate the constant values). None builds geometry.
5. Positions are one-based lines and columns. Byte offsets are zero-based and count UTF-8 bytes.

## greenware check

`greenware check <file> [--project DIR] [--geometry-only] [--json]` lists the diagnostics of a file. Exit 0 when there is no error, 1 when there is one.

```text
$ greenware check testdata/tools/post_a.gw
testdata/tools/post_a.gw: ok
```

## greenware fix

`greenware fix <file> [--write] [--code GW-...] [--choose N] [--revision SHA256] [--project DIR] [--json]`

Applies the edits that diagnostics carry. Without `--write` it prints a unified diff and changes nothing.

1. A diagnostic with one edit contributes it.
2. A diagnostic with several alternative edits (for example `deg` or `rad`) contributes none unless `--choose N` names one, numbered from 1. The skipped alternatives are listed on standard error, or under `skipped` in JSON.
3. `--code` limits the fix to a diagnostic code. It can repeat.
4. Edits are chosen in span order. An edit that overlaps an earlier one is skipped, and applied edits are applied from the end of the file backwards.
5. The result is checked again. If it has more errors than the source had, nothing is written and the status is 1 (`refused` in JSON). A fix may show a second fault, so the command repeats up to 8 passes until no edit is left; running it again applies nothing.
6. `--revision` binds the edits to the file's SHA-256 (`input_revision` in the report). If the file differs, the fix is refused.

Exit 0 when the resulting source has no error, 1 when errors remain or the fix is refused, 2 on usage or read errors.

JSON (`greenware.fix/1`): `schema, file, input_revision, output_revision, ok, changed, written, refused, refusal, errors_before, errors_after, applied, skipped, diff`.

```text
$ greenware fix testdata/tools/unit.gw --choose 2
--- a/testdata/tools/unit.gw
+++ b/testdata/tools/unit.gw
@@ -6,7 +6,7 @@
 
 param height : length = 1m in 0.5m..3m
 
-let lift = sin(30) * 1m
+let lift = sin(30deg) * 1m
 
 part unit {
   cylinder {
```

Without `--choose` the two alternatives stay unapplied, so the error remains and the status is 1:

```text
$ greenware fix testdata/tools/unit.gw  # exit 1
```

## greenware diff

`greenware diff <old.gw> <new.gw> [--fail-on breaking|change] [--project DIR] [--json]`

Compares two files by meaning and by name. It reports parameters (type, default, range, runtime or bake), parts (added, removed, renamed, changed), materials, sockets, plugs, mounts, colliders, rig declarations, levels, budgets and outputs. Layout, comments and spelling of a unit (`1200mm` for `1.2m` in a default) are not changes. A part, socket or other entity that disappears while another of the same kind with the same content appears is one `renamed` change.

Breaking changes are marked: a runtime parameter, socket, joint, clip or output removed or renamed; a parameter's type changed, a runtime parameter made a bake parameter, or a parameter's range narrowed. Moves of a socket are reported as a change to its `at` and are not judged against a tolerance.

Changes are sorted by kind (asset, meta, import, parameter, material, part, socket, and so on), then name. Exit 0 unless a gate is requested: `--fail-on breaking` exits 1 when a breaking change is present and `--fail-on change` when there is any change.

JSON (`greenware.diff/1`): `schema, old, new, changed, breaking, fail_on, failed, changes`. A change has `kind, name, change, from, fields, breaking, reason`; each field has `field, old, new`.

```text
$ greenware diff testdata/tools/post_a.gw testdata/tools/post_b.gw
~ param height
    default: 1.2m -> 1.5m
~ param radius  BREAKING
    range: 0.02m..0.1m -> 0.03m..0.1m
+ param slope
- param tint  BREAKING
~ material paint
    base: tint -> #aa3322
~ material wood
    roughness: 0.8 -> 0.9
> part top -> finial
+ part rail
~ socket flag
    part: top -> finial
~ budget
    triangles: 600 -> 800
+ output practice
```

A reformatted copy of the same asset has no change:

```text
$ greenware diff testdata/tools/post_a.gw testdata/tools/post_a_reformatted.gw
no semantic changes
```

A gate turns a breaking change into a failing status:

```text
$ greenware diff testdata/tools/post_a.gw testdata/tools/post_b.gw --fail-on breaking --json  # exit 1
{
  "schema": "greenware.diff/1",
  "old": "testdata/tools/post_a.gw",
  "new": "testdata/tools/post_b.gw",
  "changed": true,
  "breaking": true,
  "fail_on": "breaking",
  "failed": true,
...
```

## greenware describe

`greenware describe <file> [--project DIR] [--json]`

Prints a structured summary: the asset block, parameters (type, unit, default as written and evaluated, range, `bake` or `runtime`), outputs with their levels, the part tree, materials, sockets, plugs, mounts, colliders, rig declarations (skeletons, clips, morphs), tables, budgets and the imports. Values come from the source and the evaluated defaults; no geometry is built. Exit 1 when the file has an error diagnostic (the summary is still printed), 2 on a read error.

JSON (`greenware.describe/1`): `schema, file, ok`, then `asset, meta, imports, params, outputs, levels, parts, materials, sockets, plugs, mounts, colliders, rigs, tables, templates, painters, textures, budget, evaluated, unresolved`, then `diagnostics`. Setting values are the source text with layout and comments removed. A part has `body`, the first 12 hex digits of the SHA-256 of its own text.

```text
$ greenware describe testdata/tools/post_a.gw
asset testdata/tools/post_a.gw (Fence post)
  title = "Fence post"
  root = post
  length = m
  angle = deg
  up = y
  forward = -z
parameters (4):
  height : length [m] = 1.2m in 0.5m..2m  bake
  radius : length [m] = 0.04m in 0.02m..0.1m  bake
  cap : switch = on  bake
  tint : color = #aa3322  runtime
outputs (1):
  stadium file=post.glb lods=files levels=[0]
parts:
  post [root] shapes: cylinder shaft
    top [part] shapes: sphere ball
    lid [part] when cap shapes: cylinder disc
materials (2):
  wood = pbr { base = #8b5a2b; roughness = 0.8; metallic = 0 }
  paint = pbr { base = tint; roughness = 0.5; metallic = 0 }
sockets:
  flag on top kind=anchor at=(0,height+cap_height,0)
budget: triangles = 600; draw_calls = 3
```

## greenware ir

`greenware ir <file> [--output NAME] [--project DIR] [--json]`

Dumps the resolved model: every value after evaluation (lengths in metres, angles in radians), for the file's defaults or for one output (a table-driven output is `name[key]`). Without `--json` it prints the model alone, as `resolve.Resolved` marshals it. With `--json` the model sits in an envelope with the schema, the stage (`evaluated`), the output and the diagnostics. A file with an error has no model (`null`) and exits 1. The model is not editable source.

JSON (`greenware.ir/1`): `schema, stage, file, output, ok, model, diagnostics`.

```text
$ greenware ir testdata/tools/post_a.gw --output stadium --json
{
  "schema": "greenware.ir/1",
  "stage": "evaluated",
  "file": "testdata/tools/post_a.gw",
  "output": "stadium",
  "ok": true,
  "model": {
    "output": "stadium",
    "asset": [
      {
        "name": "title",
        "value": "Fence post"
      },
      {
...
```

## greenware init

`greenware init <dir> [--name NAME] [--author TEXT] [--license SPDX] [--dry-run] [--json]`

Creates a project in `<dir>` (made if needed): `greenware.mod`, one example asset `<name>.gw` that passes `check` and `build`, and `greenware.sum`. The name defaults to the directory's name, lower-cased with `-`, `.` and spaces turned into `_`. It never overwrites: if any of the three files exists, nothing is written and the status is 1. It does not guess the artwork's licence: the example has a `meta` block only when both `--author` and `--license` are given. No README is created.

JSON (`greenware.init/1`): `schema, project, edition, dir, files, dry_run, ok, conflicts`; each file has `path, action` (`create`, `would create`, `conflict`, `skip`).

```text
$ greenware init testdata/tools/new_project --name crate --dry-run
would create testdata/tools/new_project/greenware.mod
would create testdata/tools/new_project/crate.gw
would create testdata/tools/new_project/greenware.sum
```

## greenware lock

`greenware lock [<dir>] [--check] [--dry-run] [--json]`

Writes `greenware.sum` in the project at `<dir>` (default `.`, which needs a `greenware.mod`). The file pins every input by SHA-256, one line per pin, sorted by kind and then name:

```text
compiler greenware 0.1
lib std/finish std sha256:<64 hex digits> -
selena materials/painted_metal.sel project sha256:<64 hex digits> -
source goalpost.gw project sha256:<64 hex digits> CC0-1.0
```

1. `compiler <identity>`: the compiler's generator string, the one it writes into every GLB.
2. `lib <import> std sha256:<digest> <license>`: each embedded standard library file a source imports.
3. `source <path> project sha256:<digest> <license>`: every `.gw` file under the directory (directories that start with `.` are skipped). This includes project imports. The licence is the file's `meta` licence, or `-`.
4. `table <path> project ...` and `selena <path> project ...`: each file that a `table` path or a Selena material `source` names. A missing file, or a path that leaves the project, stops the command with status 1.

Fields that hold white space are quoted. The write goes through a temporary file and a rename. `--check` writes nothing and exits 1 when the file is missing or differs; `--dry-run` shows the changes without writing. Changes print as `+` (new pin), `-` (gone) and `~` (changed digest). The format follows the pin lines of the language reference, section 7.2; `solver` lines and the library closure digest are not written yet.

JSON (`greenware.lock/1`): `schema, dir, mode, before_sha256, after_sha256, changes, drift, written, ok`; each change has `kind, name, old, new`.

```text
$ greenware lock testdata/tools/proj --dry-run
+ compiler
+ source a.gw
+ source sub/b.gw
testdata/tools/proj/greenware.sum: would be written (3 pins)
```

```text
$ greenware lock testdata/tools/proj --check  # exit 1
+ compiler
+ source a.gw
+ source sub/b.gw
```

## greenware explain

`greenware explain <GW-CODE> [--json]`

Prints the catalogue entry of a diagnostic code: its severity, what it means and what to do, the fixture that makes the compiler report it, and whether it can carry an edit. The code is not case sensitive. An unknown code lists the known ones on standard error and exits 2.

JSON (`greenware.explain/1`): `schema, code, severity, doc, fixture, fix`.

```text
$ greenware explain GW-UNIT
GW-UNIT (error)
Two numbers have dimensions that cannot combine (a length plus an angle), a slot got a number of another dimension, or a trigonometric function got a number with no angle unit. For a bare angle argument the edits offer deg and rad as alternatives.
fixture: testdata/diag/GW-UNIT.gw
fix: a diagnostic of this code can carry an edit (greenware fix)
```

## greenware search

`greenware search <query> [dir] [--kind KIND] [--json]`

Finds names in the `.gw` files under `dir` (default `.`; directories that start with `.` are skipped). A name matches when it contains the query, ignoring case. The names searched are parameters, lets, materials, parts, shapes, sockets, outputs, clips and the other declarations, and the codes of the diagnostics a file reports (kind `diagnostic`). `--kind` keeps one kind. Each result is `path:line:col kind name`, sorted by path, line, column, kind and name. No match is a success (exit 0 with no output). Exit 2 when `dir` is not a directory.

JSON (`greenware.search/1`): `schema, query, dir, kind, files, matches`; each match has `path, line, column, kind, name, parent`.

```text
$ greenware search latch testdata/tools/proj
testdata/tools/proj/a.gw:18:7 param latch_height
testdata/tools/proj/a.gw:24:12 shape latch
```

## greenware trace

`greenware trace <file> <name> [--project DIR] [--json]`

Shows where a parameter, let, material, part or other declaration is used. A use inside a `let` or in a parameter's default or range leads on to the uses of that let, so the trace follows a value to the parts, shapes and outputs that read it. A `set` block of an output counts as a use of the parameter it sets. Each use has its line and column, the declaration it sits in (`in`), the field, and `via`, the chain of lets between the name and the use (empty for a direct use). `reaches` lists the holders that end the chains. Scope is not tracked: a local let or sample variable with the same name counts as a use. Exit 1 when the file declares no such name.

JSON (`greenware.trace/1`): `schema, file, name, definition, uses, reaches`.

```text
$ greenware trace testdata/tools/post_a.gw cap_height
let cap_height  testdata/tools/post_a.gw:19:5
  testdata/tools/post_a.gw:37:16 in shape ball: radius = cap_height
  testdata/tools/post_a.gw:42:25 in socket flag: at = (0, height + cap_height, 0)
```

## Other commands

`greenware build <file> [--output NAME] [--out-dir DIR] [--max-work N] [--accept FILE] [--project DIR] [--runtime-points N] [--geometry-only] [--json]` writes the GLB files and a build record. `greenware fmt <file> [--check] [--write] [--json]` formats a file. `greenware accept <source.gw> <accept.gw> --glb-dir DIR [--json]` runs an acceptance file on built files; it exits 3 when no check failed but some could not be measured. Their exit status is 0 success, 1 an error diagnostic (or an unformatted file under `fmt --check`), 2 a usage or read error.
