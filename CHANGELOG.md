# Changelog

## Unreleased

- `author.BuildAssembly` packs a mesh kit into a GLB with per-instance transforms and attachment frames. The field beacon example generates variants from a small gameplay recipe.
- `greenware verify` checks a published GLB inventory against its build record and can flag extra files in a dedicated output folder.

- `greenware view` opens a live browser preview with orbit, camera presets, output and level selection, animation playback and GLB download. Rejected edits preserve the last successful view.
- The `author` package builds rigid GLBs from Go. The `.gw` compiler and Go API share indexed vertex packing and deterministic sRGB conversion.
- `material(node)` in acceptance files measures the visible node's decoded base colour, alpha, metallic and roughness.
- The determinism lint uses the active Go build's type information, keeping integer arithmetic distinct from floating-point expressions under `-trimpath`.

- `greenware watch` rebuilds preview GLBs when the source, a local import, or pinned data changes. A rejected build leaves the last good preview available.
- `greenware accept` can measure rigid silhouettes against a locked camera and a binary reference mask. The report records the mask and camera digests.
- The CI workflow checks workspace and standalone module builds, runs a short test suite on Linux amd64 and macOS arm64, and compares both runners' native GLB and receipt digests. The full workspace suite runs on the schedule or when explicitly requested.
- The bollard example provides a small rigid prop and acceptance file. The VS Code extension adds syntax highlighting, with build and watch tasks available from the checkout.

- `output { grid = n }` snaps GLB values to a multiple of 2^-n (1 to 30) for the historical athlete comparison (`docs/reference/athlete.md`, "`grid`"). Absent, the output does not change.
- The athlete is the body of the game's Go bake (`examples/athlete/athlete.gw`): the same ring tables, skeleton, skin steps and seven build targets, 13,016 triangles and 6,836 vertices part for part, with the game's 27 body clips installed from `examples/athlete/clips`. New for it: `seam = off` and `weld_caps = on` on `loft`, `ellipsoid` and `tube` (n vertices a ring, caps that share the end rings' vertices), and the `girth` morph operation (`docs/reference/rig.md`). The old Python port's `height` parameter and its `tall`, `short`, `neck-long`, `neck-short` and `brow` targets are gone from it (`testdata/athlete-frozen` keeps the old file). A file with runtime morph weights and no joint offsets now records `rig_base`, so the acceptance can pose it at another runtime point. The rig classes grew: 18,432 triangles, 16,384 bound positions and 64 clips.
- Examples that build with `greenware build`: the goal post (`examples/goalpost`), the helmet, the rigged arm and the athlete, each with an acceptance file.
- The skin weight solver (milestone M5): `skin { solve { ... } }` chooses weights on the supplied joints from the distance to the bones, smooths them over the mesh and refuses with `GW-SOLVE` when the smoothing does not settle in the iteration cap. New ticket `solve`, new example `examples/arm-solved`. It is not qualified: on the athlete it is worse than the hand skin at both feet (`docs/reference/rig.md`, "Solved skin"). The worker refuses it with `GW-RUNTIME-UNSAFE`.
- `greenware accept` runs those acceptance files on decoded GLB files.
- Authoring commands: `fix`, `diff`, `describe`, `ir`, `init`, `lock`, `explain`, `search` and `trace` (`docs/reference/commands.md`).
- `greenware print` writes one level of one output as a 3MF file of solids, and refuses a shape that is not a closed manifold with `GW-PRINT` (`docs/reference/print.md`). It does not check wall thickness or intersections.
- The cost ledger: a build reserves cost tickets and refuses a build over its limits.

- `box` shape: flat faces with `segments` divisions, UVs by the box projection, and a `bevel` of all twelve edges (`docs/reference/box.md`). Edges other than `all` on a box bevel are `GW-UNSUPPORTED`.

- `rot` on shapes, and `rot`, `scale` and `pivot` on parts and instances (`docs/reference/transforms.md`): intrinsic X, Y, Z turns, exact for multiples of 45 degrees, normals by the inverse transpose. A socket's `rot` stays `GW-UNSUPPORTED`.

- Inch unit: `4in`, `18.5in` (0.0254 m exactly) when `in` touches its number; a spaced `in` stays the range word, and `4 in` as a syntax error now says `write the inch unit without a space: 4in` (`docs/reference/units.md`, decision 0019). The grammar blob `lang/greenware.bin` and `docs/reference/grammar.ebnf` are regenerated.

- The build record and `greenware build --json` list each output's `levels` with `distance_m` (null for level 0), `max_error_m`, `triangles` and `draws`, so a game can read the level distances instead of repeating them (`docs/reference/build-record.md`). No GLB byte changes. `manifest = on` still writes no GoSX manifest entry; the note now says where the distances are.

- `greenware version [--json]` (also `--version`, `-v`): the generator version, Go version, platform and VCS revision of the binary, schema `greenware.version/1`. An unknown flag now prints one line (`greenware build: unknown flag --foo (see greenware help build)`) instead of the whole usage, `greenware help <command>` prints one command's usage, and `build` accepts `-o` for `--out-dir` (`docs/reference/commands.md`).

- Typed data packages (decision 0020): `import "football/world@1" as world` reads a project-local lock, schema and data file, and `world.goal_post.inside_width` evaluates to a dimensioned value (length in metres, angle in radians, ratio, number). The lock pins the files by SHA-256 of their canonical JSON; a stale or missing pin is `GW-LOCK` and nothing is written. New codes `GW-IMPORT-DATA`, `GW-IMPORT-CONTRACT`, `GW-LOCK`; new package `datapkg` (`Load`, `Digest`, `Verify`, `NewLock`, `WriteLock`, exact `*big.Rat` values); `greenware lock --data <name@N>` repins one package and `greenware lock` records `data` lines; `describe` and the language server show each field's meaning. A GLB of an asset that imports a package carries `asset.extras.greenware.inputs`, and the build record carries `inputs.data`; an asset that imports none is byte-identical to before (`docs/reference/data-package.md`). Needs `gltf` v0.4.0 (`Asset.Extras`).

- Renamed from Facet to Greenware (`.gw`, module `m31labs.dev/greenware`).
- Geometry kernel (`kernel/`), neutral GLB codec (`gltf/`), deterministic math (`det/`) and an independent checker (`conformance/`).

- Rigging: skeleton, joints, skin steps (`lengthen`, `blend`), morph targets, clips and sockets compile to glTF skins, morph targets and animations (`kernel/rig`, `docs/reference/rig.md`, `examples/arm`, `examples/athlete`). New codes `GW-RIG-UNBOUND`, `GW-RIG-STRUCTURE`, `GW-CLIP-KEY`, `GW-RIG-LIMIT`, `GW-RIG-BAND`; new tickets `rig`, `weights`, `morph`, `clip`.
- The athlete builds with `greenware build`: project imports (`import "../helmet/helmet.gw" as helmet`, confined to the project root, pinned by SHA-256), `mount` (runtime record or baked into the skinned mesh), `output mounts`, `table` and `for_each`, `lod keep`, `check runtime` (96-point product, partial plans) and `check rig` (ground band, torn edges, mount fit, `bands = error`). New codes `GW-MOUNT`, `GW-TABLE-ROW`, `GW-RIG-GROUND`, `GW-RIG-TORN`, `GW-RUNTIME-PARTIAL`; new tickets `paint_large`, `mount`, `surface_rig`, `audit`, `fit`, `encode_large` (measured classes, `docs/reference/athlete.md`); new flags `--project` and `--runtime-points`.
- The build record and `--json` report carry a census of every output and level, and the rig audit.
- Morph targets are written as sparse accessors when smaller, which changes the bytes of every rigged file (the arm candidate and the athlete files).
- `greenware accept` measures rigs on decoded files: `joint_count`, `morph_count`, `animation_count`, `has_animation`, `animations_present`, `runtime_points`, `posed_lowest`, `posed_position`, `mounted_clearance`; `lods` defaults to `files`. New package `kernel/geom` (triangle distance and a bounding-volume tree).
- Fixed `if c then a else b`, which returned `a` for any condition when both branches were numbers.
- Repository skeleton and dependency boundary check.
