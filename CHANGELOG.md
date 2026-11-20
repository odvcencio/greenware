# Changelog

## Unreleased

- Renamed from Facet to Greenware (`.gw`, module `m31labs.dev/greenware`).
- Geometry kernel (`kernel/`), neutral GLB codec (`gltf/`), deterministic math (`det/`) and an independent checker (`conformance/`).

- Rigging: skeleton, joints, skin steps (`lengthen`, `blend`), morph targets, clips and sockets compile to glTF skins, morph targets and animations (`kernel/rig`, `docs/reference/rig.md`, `examples/arm`, `examples/athlete`). New codes `GW-RIG-UNBOUND`, `GW-RIG-STRUCTURE`, `GW-CLIP-KEY`, `GW-RIG-LIMIT`, `GW-RIG-BAND`; new tickets `rig`, `weights`, `morph`, `clip`.
- Fixed `if c then a else b`, which returned `a` for any condition when both branches were numbers.
- Repository skeleton, dependency boundary check, CI on Linux and macOS arm64.
