# Changelog

## Unreleased

What works now, as of this list:

- Examples that build with `greenware build`: the goal post (`examples/goalpost`), the helmet, the rigged arm and the athlete, each with an acceptance file.
- The skin weight solver (milestone M5): `skin { solve { ... } }` chooses weights on the supplied joints from the distance to the bones, smooths them over the mesh and refuses with `GW-SOLVE` when the smoothing does not settle in the iteration cap. New ticket `solve`, new example `examples/arm-solved`. It is not qualified: on the athlete it is worse than the hand skin at both feet (`docs/reference/rig.md`, "Solved skin"). The worker refuses it with `GW-RUNTIME-UNSAFE`.
- `greenware accept` runs those acceptance files on decoded GLB files.
- Authoring commands: `fix`, `diff`, `describe`, `ir`, `init`, `lock`, `explain`, `search` and `trace` (`docs/reference/commands.md`).
- `greenware print` writes one level of one output as a 3MF file of solids, and refuses a shape that is not a closed manifold with `GW-PRINT` (`docs/reference/print.md`). It does not check wall thickness or intersections.
- The cost ledger: a build reserves cost tickets and refuses a build over its limits.

- Renamed from Facet to Greenware (`.gw`, module `m31labs.dev/greenware`).
- Geometry kernel (`kernel/`), neutral GLB codec (`gltf/`), deterministic math (`det/`) and an independent checker (`conformance/`).

- Rigging: skeleton, joints, skin steps (`lengthen`, `blend`), morph targets, clips and sockets compile to glTF skins, morph targets and animations (`kernel/rig`, `docs/reference/rig.md`, `examples/arm`, `examples/athlete`). New codes `GW-RIG-UNBOUND`, `GW-RIG-STRUCTURE`, `GW-CLIP-KEY`, `GW-RIG-LIMIT`, `GW-RIG-BAND`; new tickets `rig`, `weights`, `morph`, `clip`.
- The athlete builds with `greenware build`: project imports (`import "../helmet/helmet.gw" as helmet`, confined to the project root, pinned by SHA-256), `mount` (runtime record or baked into the skinned mesh), `output mounts`, `table` and `for_each`, `lod keep`, `check runtime` (96-point product, partial plans) and `check rig` (ground band, torn edges, mount fit, `bands = error`). New codes `GW-MOUNT`, `GW-TABLE-ROW`, `GW-RIG-GROUND`, `GW-RIG-TORN`, `GW-RUNTIME-PARTIAL`; new tickets `paint_large`, `mount`, `surface_rig`, `audit`, `fit`, `encode_large` (measured classes, `docs/reference/athlete.md`); new flags `--project` and `--runtime-points`.
- The build record and `--json` report carry a census of every output and level, and the rig audit.
- Morph targets are written as sparse accessors when smaller, which changes the bytes of every rigged file (the arm candidate and the athlete files).
- `greenware accept` measures rigs on decoded files: `joint_count`, `morph_count`, `animation_count`, `has_animation`, `animations_present`, `runtime_points`, `posed_lowest`, `posed_position`, `mounted_clearance`; `lods` defaults to `files`. New package `kernel/geom` (triangle distance and a bounding-volume tree).
- Fixed `if c then a else b`, which returned `a` for any condition when both branches were numbers.
- Repository skeleton, dependency boundary check, CI on Linux and macOS arm64.
