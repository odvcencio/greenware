# Conformance oracle: readings of ambiguous rules

This module is an independent second reading of the rules in
`specs/conformance.md` (sections 6, 6.1, 7.1) and the owner's
`goalpost.accept.gw`. It reads no Greenware, kiln or GoSX code. Every report says
"independent structural validator, not the Khronos validator".

Compare each reading below with the producer's reading. A difference is a
finding to resolve, not a bug in either side until the spec decides.

## Measurement readings

| Rule | Reading taken | Where |
|---|---|---|
| Scope of `gap`, `closed`, `connected`, `drawn_triangles` | Spec 6.1 says "the named node's own visible primitives"; the lead's brief and the acceptance file (`node(goalpost)`, a meshless root) need the subtree. The API takes a `Select`: `Sub(name)` is the whole subtree, `OwnOf(name)` is the node's own mesh. The CLI uses the subtree for `--node` and `--gap`, and the own mesh for the per-node table. `bounds` is always subtree (as the spec says). | `measure.Select` |
| "Visible" / drawn | A primitive is drawn when its node is reachable from the default scene (`scene`, else scene 0 with a warning) and it has at least one triangle. Nodes outside the default scene (alternates) are present but draw zero triangles. No `KHR_node_visibility` is read. | `measure/model.go` |
| `has_node` | True for any node name in the file, drawn or not (default or alternate hierarchy). First match in file order when names repeat (a warning is emitted for duplicates). | `HasNode` |
| `draw_calls` | One per primitive (with triangles) of every node instance in the default scene. A mesh with two materials is two draws. Two nodes with the same material are still two draws; the oracle does not merge across nodes. | `DrawCalls` |
| Welding | Position triple of binary32 values after the node's world transform (computed in float64, rounded to binary32; exact for identity). Equal when all three components are numerically equal (+0 equals -0). No tolerance. Normals, UVs and indices play no part. | `weld` |
| `closed` | At least one triangle; every undirected welded edge used by exactly two triangles, one in each direction. Four uses (the coincident-ring case) is not closed even when no edge is open. A triangle that repeats a welded vertex makes the result not closed (conservative; reported as `DegenerateTriangles`). Winding direction (outward or inward) is not part of `closed`; `SignedVolume` is reported separately. | `Closed` |
| Shells and `connected` | Shells are triangles joined by a shared welded edge (any use count). Two shells are in contact when the minimum surface distance between any pair of their triangles is <= 1 mm (inclusive; 0 when they intersect). Connected means at least one triangle and one contact component. The distance is exact triangle-to-triangle (edge crossings, edge-edge, vertex-face), not bounding-box. | `Connected` |
| `gap(node, axis, at)` | From the point, cast along +axis and -axis through the node's drawn triangles. Take the nearest positive hit on each side (both faces of a triangle count). Gap = sum of the two distances. Refused (status other than `ok`): a missing hit on either side; a hit within 1 micrometre of the origin (on surface); the nearest hit on either side is a back face (origin inside a solid wound outward, or inverted winding). The ray through a shared edge or vertex hits (inclusive barycentric test with 1e-9 slack). Triangles parallel to the ray are skipped. | `Gap` |
| Gap "between uprights" | The measurement does not know which hits are "uprights"; with the subtree selected it is the innermost pair around the point. A crossbar or plate crossing the ray at that height would be hit first. At y=7 m only the uprights cross. | |
| Sockets | A meshless node named `socket.<name>`; position is the translation of its world matrix (all ancestors applied, including under alternate roots, using local transforms only); axes are the normalised columns of the world matrix. `asset.extras.gw` and node extras are not read because the schema is not specified. | `Sockets` |
| Colliders | Nodes named `collider.<name>`; `HasMesh` is reported so "meshless" can be checked. | `Colliders` |
| Morph targets, skins | Default morph weights (node, else mesh) are applied to POSITION. Skinning is not applied; skinned meshes are measured with their node transform at bind data. Only triangle-list primitives (mode 4) are measured. | `primitiveTriangles` |

## Validator readings (`glb`)

* Header length must equal the file length; chunk lengths must be multiples of 4;
  chunk 0 is JSON, chunk 1 (optional) is BIN; unknown chunks are a warning.
* JSON padding must be spaces. Trailing NUL bytes are an error (JSON-TRAILING).
* Buffer 0 without a URI is the BIN chunk. The BIN chunk may be at most 3 bytes
  longer than `byteLength`. External URIs are loaded only through a resolver the
  caller supplies (the CLI confines it to the GLB's own directory); otherwise
  accessors on them are unchecked and a warning says so.
* POSITION accessors must carry `min` and `max`, and they must equal the stored
  extrema exactly in binary32. Other accessors with `min`/`max` are checked the same way.
* A non-finite float anywhere in a FLOAT accessor is an error.
* Normals: zero length is an error; length off by more than 0.005 is a warning.
* Primitive-restart index values are errors. Triangles that repeat an index are warnings.
* Time inputs of animations must be strictly increasing and carry `min`/`max`.
* Duplicate node names are a warning (by-name lookups take the first).
* Not checked: texture references inside materials, KHR extension payloads,
  mesh quantization (a required `KHR_mesh_quantization` is refused), Draco/meshopt,
  JSON duplicate keys (Go's decoder keeps the last), accessor alignment rules for
  `byteStride` sharing between accessors.

## Contradictions and mismatches found in the spec or the brief

1. Moved-upright numbers. The brief says one cylinder moved 2 cm gives 5.68 m;
   spec 7.1 says one pipe moved 20 mm gives about 5.660 m. Arithmetic agrees with
   the spec: 5.64 + 0.02 = 5.66 for one pipe, 5.68 only when both move 2 cm. The
   tests assert both.
2. Triangle count of the original upright. Spec 7.1 predicts 164 triangles for a
   12-side cylinder (two fan caps) plus a 12 by 6 sphere with pole fans. That
   construction gives 24 + 12 + 12 = 48 for the pipe and 12 + 12 + 4*24 = 120 for
   the sphere, so 168. The 12 four-use rim edges and zero open edges match the
   spec; only the count of 164 does not. If the producer's pipe or sphere differs
   (for example a single cap, or a different pole treatment), the producer's 164 may
   be right for its own mesh. The oracle tests assert 168 for its own mesh and
   do not use 164.
3. Gap in the brief is "5.64 - 2*0.05" with uprights at x=+-2.82. The spec puts the
   uprights' centres at +-2.87 m (inside faces at +-2.82 m, gap 5.64). The tests use 2.87.
4. Spec 6.1 limits `gap`, `closed` and `connected` to the node's own primitives, but
   the acceptance file applies them to `node(goalpost)`, which has no mesh of its
   own in the described hierarchy. The subtree reading (see table) is the only one
   under which the acceptance file can pass.
5. Spec 6.1 asks for a ray "from (0,7m,0) along +-X" to the "two nearest positive ray
   hits"; the nearest hit on each side is what a 12-gon gives at a vertex (5.640) but
   at an edge midpoint it is 5.6434 (apothem 0.05*cos 15 degrees). The 1 mm + 2*lod_error
   tolerance accepts the L0 case only because lod_error is not zero; check the phase of the
   polygon at each level when comparing.

## What the oracle did not verify

* It has not been run on any real Greenware output; only on its own fixtures.
* The Khronos validator was not run (not available).
* Outline measurements (`outline(...)`) are not implemented: they need the protected
  reference mask and camera sidecar.
* Appearance, skin, clip and morph contracts are only structurally checked.
