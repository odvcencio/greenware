# Greenware

[![CI](https://github.com/odvcencio/greenware/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/odvcencio/greenware/actions/workflows/ci.yml)

Greenware compiles 3D assets to deterministic glTF 2.0 binaries (GLB). Describe an asset in `.gw`, or generate rigid surfaces in Go with the same geometry kernel and GLB packing code. The project is pre-1.0. Pin a commit when using it in a build pipeline.

## Quick start

Install the qualified v0.1.0 CLI with `go install m31labs.dev/greenware/cmd/greenware@v0.1.0`. To try the features documented on main, install from a checkout with Go 1.25 or later. CI runs on Go 1.26.11. The browser preview is included in the CLI.

```sh
git clone https://github.com/odvcencio/greenware.git
cd greenware
CGO_ENABLED=0 go install ./cmd/greenware
```

Put Go's bin directory on your PATH. Build the bollard, run its acceptance file, and open the loopback URL printed by `view`.

```sh
greenware build examples/bollard/bollard.gw -o out/bollard
greenware accept examples/bollard/bollard.gw examples/bollard/bollard.accept.gw --glb-dir out/bollard
greenware view examples/bollard/bollard.gw
```

The browser can orbit the model, switch outputs and levels, play its clips, mark socket positions in the rest pose, and download the selected GLB. Edit the source to see it rebuild. A failed edit keeps the last good model and shows the compiler diagnostics. Use `greenware watch` to publish preview files for another viewer. The [VS Code extension](editors/vscode) adds highlighting, and the checkout includes build, watch, and view tasks.

## Write an asset

Save this as `post.gw` and run `greenware build post.gw -o out/post` or `greenware view post.gw`.

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

## Author from Go

Import `m31labs.dev/greenware/author` to build rigid GLBs from named Greenware meshes and PBR materials. Use `kernel/build` for stock shapes or supply your own indexed surfaces. The [Go authoring guide](docs/reference/go-authoring.md) includes a complete example, input limits, attachment frames, and material acceptance checks.

## Examples and verification

| Example | What it exercises |
| --- | --- |
| [Bollard](examples/bollard) | Small rigid prop with a passing acceptance file |
| [Goal post](examples/goalpost) | Multiple outputs, levels and decoded geometry checks |
| [Goal-post wind flags](examples/gosx-goalpost) | GoSX Scene3D mounts animated flags on Greenware sockets |
| [Arm](examples/arm) | Introductory rig with hand-painted weight bands and animation |
| [Helmet](examples/helmet) | Offline PBR materials and visible variants |
| [Athlete](examples/athlete) | Historical regression fixture with known clearance failures |
| [Solved arm](examples/arm-solved) | Experimental skin-weight solver |

CI runs on native Linux amd64 and macOS arm64 and compares the GLB and build-record digests for the goal post, helmet and arm. The goal post acceptance file checks both variants at all three levels against the [approved v1 masks and locked cameras](examples/goalpost/ref). See the [evidence workflow](CONTRIBUTING.md#rigid-prop-evidence) for the independent oracle, Khronos validator and negative controls.

## Commands

| Command | Job |
|---|---|
| `build` | Compile a file to GLB files and a build record |
| `watch` | Rebuild preview GLBs when the source or its inputs change |
| `view` | Preview and inspect the live asset in a browser |
| `check` | List the diagnostics of a file |
| `fmt` | Format a file |
| `accept` | Run an acceptance file on built GLB files |
| `sockets` | Inspect the attachment frames in a GLB |
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

See the [command reference](docs/reference/commands.md) for flags, output and exit status.

## Layout

| Path | Job |
|---|---|
| `det/` | Deterministic scalar math (own module, `m31labs.dev/greenware/det`) |
| `gltf/` | Neutral GLB reader and writer (own module, `m31labs.dev/greenware/gltf`) |
| `language/`, `lang/` | The tree-sitter grammar, parser and formatter |
| `ast/`, `resolve/`, `units/`, `registry/`, `diag/` | The declaration model, name and constant evaluation, units, field tables and diagnostics |
| `std/` | The standard library of `.gw` sources |
| `kernel/` | The geometry kernel: `build` (shapes), `mesh`, `geom` (distance and bounding volumes), `image` (textures) and `rig` (skeletons, skins, morphs, clips) |
| `author/` | Go API for building rigid assets from kernel meshes |
| `compiler/` | The back end: GLB output, the cost ledger, the rig audit and the print backend |
| `backend/threemf` | The 3MF writer, reader and validator |
| `accept/` | The acceptance-file evaluator |
| `tools/` | The authoring commands (`fix`, `diff`, `describe`, `ir`, `init`, `lock`, `explain`, `search`, `trace`) |
| `cmd/greenware` | The command-line tool |
| `examples/` | Rigid props, an introductory rig, and historical or experimental fixtures |
| `conformance/` | An independent checker, in its own module |

The core is engine-independent. `internal/depcheck` checks the dependency boundary.

## Licence

MIT, copyright M31 Labs. Authored assets declare their own licences.
