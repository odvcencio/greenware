# Greenware

**Pre-1.0: the language, the compiler and the file format will change without notice.** Do not depend on it yet.

Greenware is a source language for 3D assets, named for shaped clay before it is fired. One `.gw` file describes one asset (a prop, a building, a vehicle, a character). The compiler turns it into a glTF 2.0 binary (GLB), the same bytes every time, and checks it.

```
greenware 1

asset post { root = post }

param height : length = 3m in 1m..5m

material steel = pbr { base = #8a8d8f roughness = 0.45 metallic = 1 }

part post {
  material = steel
  cylinder {
    radius = 0.05m
    height = height
    segments = 16
    caps = on
  }
}

output main { file = "post.glb" }
```

```
greenware build post.gw --out-dir out
```

Start with the [bollard](examples/bollard) for a small rigid prop with a passing acceptance file. The [goal post](examples/goalpost) exercises multiple outputs and levels. Its geometry checks pass and its six outline checks await approved reference masks and cameras. The [hand-weighted arm](examples/arm) introduces rigging. The [helmet](examples/helmet) demonstrates an offline PBR build. The [athlete](examples/athlete) is a historical regression fixture with known helmet-clearance failures and unavailable measurements. Solved weights and 3MF are experimental.

Run `greenware watch post.gw` to rebuild the preview GLBs under `out/preview` whenever a source or imported input changes. The [VS Code extension](editors/vscode) supplies highlighting and the checkout includes build and watch tasks.

## Commands

| Command | Job |
|---|---|
| `build` | Compile a file to GLB files and a build record |
| `watch` | Rebuild preview GLBs when the source or its inputs change |
| `check` | List the diagnostics of a file |
| `fmt` | Format a file |
| `accept` | Run an acceptance file on built GLB files |
| `fix` | Apply the fixes that diagnostics offer |
| `diff` | Compare two sources and report breaking changes |
| `describe` | Summarise a file's declarations |
| `ir` | Show the evaluated form of an output |
| `init` | Create a project folder |
| `lock` | Write or check the project's lock file |
| `explain` | Explain a diagnostic code |
| `search` | Find declarations in a project |
| `trace` | Follow a name to where it is used |
| `print` | Write one output level as a 3MF file of solids |

Flags, output and exit status are in `docs/reference/commands.md`.

## Layout

| Path | Job |
|---|---|
| `det/` | Deterministic scalar math (own module, `m31labs.dev/greenware/det`) |
| `gltf/` | Neutral GLB reader and writer (own module, `m31labs.dev/greenware/gltf`) |
| `language/`, `lang/` | The tree-sitter grammar, parser and formatter |
| `ast/`, `resolve/`, `units/`, `registry/`, `diag/` | The declaration model, name and constant evaluation, units, field tables and diagnostics |
| `std/` | The standard library of `.gw` sources |
| `kernel/` | The geometry kernel: `build` (shapes), `mesh`, `geom` (distance and bounding volumes), `image` (textures) and `rig` (skeletons, skins, morphs, clips) |
| `compiler/` | The back end: GLB output, the cost ledger, the rig audit and the print backend |
| `backend/threemf` | The 3MF writer, reader and validator |
| `accept/` | The acceptance-file evaluator |
| `tools/` | The authoring commands (`fix`, `diff`, `describe`, `ir`, `init`, `lock`, `explain`, `search`, `trace`) |
| `cmd/greenware` | The command-line tool |
| `examples/` | Rigid props, an introductory rig, and historical or experimental fixtures |
| `conformance/` | An independent checker, in its own module |

Greenware depends on no game engine. The core never imports GoSX or Kiln; `internal/depcheck` enforces that.

## Licence

MIT, copyright M31 Labs. Authored assets declare their own licences.
