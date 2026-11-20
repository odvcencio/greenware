# Changelog

## Unreleased

- Renamed from Facet to Greenware (`.gw`, module `m31labs.dev/greenware`).
- Geometry kernel (`kernel/`), neutral GLB codec (`gltf/`), deterministic math (`det/`) and an independent checker (`conformance/`).

- Rigging: skeleton, joints, skin steps (`lengthen`, `blend`), morph targets, clips and sockets compile to glTF skins, morph targets and animations (`kernel/rig`, `docs/reference/rig.md`, `examples/arm`, `examples/athlete`). New codes `GW-RIG-UNBOUND`, `GW-RIG-STRUCTURE`, `GW-CLIP-KEY`, `GW-RIG-LIMIT`, `GW-RIG-BAND`; new tickets `rig`, `weights`, `morph`, `clip`.
- The athlete builds with `greenware build`: project imports (`import "../helmet/helmet.gw" as helmet`, confined to the project root, pinned by SHA-256), `mount` (runtime record or baked into the skinned mesh), `output mounts`, `table` and `for_each`, `lod keep`, `check runtime` (96-point product, partial plans) and `check rig` (ground band, torn edges, mount fit, `bands = error`). New codes `GW-MOUNT`, `GW-TABLE-ROW`, `GW-RIG-GROUND`, `GW-RIG-TORN`, `GW-RUNTIME-PARTIAL`; new tickets `paint_large`, `mount`, `surface_rig`, `audit`, `fit`, `encode_large` (measured classes, `docs/reference/athlete.md`); new flags `--project` and `--runtime-points`.
- The build record and `--json` report carry a census of every output and level, and the rig audit.
- Morph targets are written as sparse accessors when smaller, which changes the bytes of every rigged file (the arm candidate and the athlete files).
- `greenware accept` measures rigs on decoded files: `joint_count`, `morph_count`, `animation_count`, `has_animation`, `animations_present`, `runtime_points`, `posed_lowest`, `posed_position`, `mounted_clearance`; `lods` defaults to `files`. New package `kernel/geom` (triangle distance and a bounding-volume tree).
- Fixed `if c then a else b`, which returned `a` for any condition when both branches were numbers.
- Repository skeleton, dependency boundary check, CI on Linux and macOS arm64.
