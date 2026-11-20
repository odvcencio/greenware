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
| Morph targets, skins (static measures) | Default morph weights (node, else mesh) are applied to POSITION. The static measures (`Bounds`, `Closed`, `Gap`, ...) use the stored positions of a skinned mesh with no skinning and without the mesh node's transform (see "Rig and pose readings"). Only triangle-list primitives (mode 4) are measured. | `primitiveTriangles` |

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

## Rig and pose readings (`glb` skins, animations, morphs; `measure` pose)

Each row is a reading taken where glTF 2.0 or the design text leaves room. The
producer's reading may differ; a difference is a finding to resolve.

| Rule | Reading taken | Where |
|---|---|---|
| Weight sum tolerance | Per vertex, over all `WEIGHTS_n` sets together (sets beyond the first carry influences past the fourth, so one set alone need not sum to 1). `abs(sum-1) <= 1e-5` is clean, up to `1e-4` is a warning, beyond is an error; one finding per primitive, for the worst vertex. For normalized ubyte and ushort weights the tolerance grows by the rounding the format forces: `2/255` per ubyte set, `2/65535` per ushort set (four components, half a step each). Without that allowance no ubyte weights could pass `1e-5`. | `checkPrimitiveWeights` |
| Negative, NaN, unnormalized weights | A negative weight is an error (`WEIGHTS-NEGATIVE`). A non-finite float is left to `ACC-NONFINITE`, so a NaN gives one finding. Unsigned byte or short `WEIGHTS` without `normalized: true` is an error. | same |
| Joint index range | Every `JOINTS_n` value must be below the joint count of the skin of each node that draws the mesh, including entries whose weight is zero. A mesh with `JOINTS_0` on a node without a skin is a warning (`SKIN-ATTRS-UNUSED`): it draws unskinned and its indices cannot be checked. | `validateSkinnedNodes` |
| Skin without attributes | A node with a skin whose primitive lacks `JOINTS_0` or `WEIGHTS_0` is one error `SKIN-ATTRS` naming what is missing. "A skin object alone proves nothing" (spec 8.2 row on missing attributes). `JOINTS_n` without `WEIGHTS_n` (n > 0) is `SKIN-ATTRS-PAIR`. | same |
| Skeleton | "Acyclic skeleton" is the node graph's own cycle rule (`NODE-CYCLE`, reported once); the skin check is silent when it meets a cycle. Added: all joints must share one root (`SKIN-ROOT`); a named `skeleton` must be the closest common ancestor of the joints or an ancestor of it (`SKIN-SKELETON`). Missing `inverseBindMatrices` means identity matrices and is valid. | `checkSkeleton` |
| Morph names | `extras.targetNames` is a convention, not a glTF rule. It is read from the mesh, else from a primitive. A length different from the target count is an error (`MORPH-NAMES`), because Greenware promises named morphs; a repeated name is a warning. Targets may carry only attributes the base primitive has (`TARGET-BASE`). | `checkTargetNames` |
| Animation inputs | Times strictly increasing and non-negative; `min`/`max` required and equal to the stored extrema in binary32; `CUBICSPLINE` needs at least two keys. Output counts: `n` for LINEAR and STEP, `3n` for CUBICSPLINE, times the target count for `weights`. Component types: translation and scale FLOAT; rotation FLOAT or normalized byte/short; weights FLOAT or any normalized integer. | `validateAnimations` |
| Rotation length | A rotation key more than `1e-3` from unit length is a warning (`ANIM-QUAT`), not an error (the brief says warn). For `CUBICSPLINE` only the value of each triple is tested; tangents are free. A node's own stored `rotation` is still an error at `1e-3` (`NODE-ROTATION`, an earlier reading); the two rules disagree on severity. | `checkQuats` |
| Channel targets | A channel with no `node` is a warning (it animates nothing). Duplicate `(node, path)` in one animation is an error. A matrix node cannot be animated. `weights` on a node without morph targets is an error. | `validateAnimations` |
| CUBICSPLINE layout | Per key, three consecutive output elements: in-tangent, value, out-tangent (each `n` numbers; for `weights`, `n` = target count). Segment `k..k+1` uses the out-tangent of key `k` and the in-tangent of key `k+1`, both scaled by the time step `dt`: `p(s) = h00 p0 + h10 dt b0 + h01 p1 + h11 dt a1`, `s = (t-t0)/dt`. Rotations are normalized after the blend. At and beyond the last key (and before the first) the value is the key's value, not a tangent. | `track.at` |
| STEP | The value of the latest key with `time <= t`. At exactly a key time the new key applies. At and after the last key the last value holds. | `track.at` |
| Outside the key range | Before the first key a channel holds its first value; after the last it holds its last. Clips do not loop. A clip's duration is the latest key time over its channels (a clip that starts after 0 still has the later time as its duration, not the length of its span). | `PoseAt`, `Clips` |
| Slerp | Shortest arc: if the dot product is negative, the second key is negated first. Above dot 0.9995 the result is a normalized linear blend. Sign-aligned comparison (`QuatDistance`, the spec's 8.7 rule): negate when the dot is negative, then take the largest component difference. | `Slerp` |
| Channel absent from a clip | The node keeps its stored (rest) value. The oracle never inherits an idle rotation into a clip, because it never composes clips. | `PoseAt` |
| Skinning | Linear blend skinning with the weights as stored (no renormalizing): `p = sum(w_i * J_i * IBM_i * v)`, `J_i` the joint's world matrix in the pose, with the morph deltas added to `v` first. The skinned mesh node's own transform is ignored (glTF 2.0). Hence the static measures also ignore it for skinned meshes. A vertex whose weights are all zero is left at its stored position (not collapsed to the origin); it is also a `WEIGHTS-SUM` error. | `posedPrim` |
| "Lowest vertex" | The vertex with the smallest world Y among vertices that at least one triangle of a mode-4 primitive in the default scene uses (unreferenced vertices are not drawn). World positions are float64 and are not rounded to binary32 (the static measures round). First in file order wins a tie. | `LowestVertex` |
| Posed bounds | The box of those same vertices. | `PosedBounds` |
| "Identity pose" in the bind-pose check | The rest pose of the file: every node at its stored transform and morph weights at their defaults. The check skins each primitive there and compares with the stored positions moved by the same default morph weights. It is not vacuous: it fails when the inverse bind matrices do not match the stored joint transforms. The spec's three inputs (adjusted inverse bind, unplayed display pose, original rest) are therefore one input here: the file as stored. | `BindPoseDeviation` |
| Influence count | Distinct joints with a nonzero total weight on a vertex, over all sets. "More than four" means more than four such joints. | `WeightStats` |
| `oracle pose --node` | Repeatable. Restricts the joint list to the named nodes (any node, not only joints). The bounds and lowest vertex always cover the whole default scene. With no `--node` the list is every skin joint. An invalid file is reported (exit 1), not posed. | `cmd/oracle/pose.go` |

Not done: the oracle does not compose clips, loop, blend, or apply the
`KHR_animation_pointer` or `KHR_mesh_quantization` extensions; and it does not check
that the joint hierarchy matches any named Greenware joint set.

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

6. Weight-sum tolerance. Spec 8.3 says weights sum to 1 "within `10^-6`"; the
   brief for this extension says `1e-5` clean, `1e-4` warning, error beyond. The
   oracle uses the brief's thresholds (and cannot meet `1e-6` for normalized
   ubyte or ushort storage at all). If the producer writes float weights it can
   meet `1e-6`; tighten `weightOK` in `glb/rig.go` to compare.
7. Spec 7.2 and 8.4 disagree about the historical clip count: 19 body and four
   wrapper animations (23 total, §8.4 and the §3 inventory) against "only six
   animations" when installation is omitted. These are different claims (all
   clips against the clips present without the library) and the oracle reports
   whatever the file holds; it does not know either number.
8. Spec 8.3 lists "within influence limit" without a number. The oracle reports
   `max_influences` and `over_four` and fails nothing on them; a limit has to be
   supplied by whoever calls it. The language text (M5, `max_influences = ...`) is
   the only place it appears.
9. Spec 8.7 says to compare "unit quaternion components within `10^-6` after
   sign alignment". Sign alignment makes `q` and `-q` equal, which is right for
   stored keys. It does not make two interpolated paths equal: a file that
   slerps the long way round and one that slerps the short way agree at the keys
   and differ between them. The oracle's `Slerp` takes the short way (glTF's
   reading); it flags nothing when a producer's keys are far apart in sign.
10. Spec 8.2 names the "adjusted inverse bind", the "unplayed display pose" and
    the "original rest" as three inputs. A GLB has one stored node transform and
    one set of inverse bind matrices; the oracle cannot tell which of the three
    a file's stored pose is. It checks the file for self-consistency only.
11. Time resolution: the spec compares sampled values at "the sorted union of
    delivered key times" and also says "do not certify motion between keys".
    `PoseAt` interpolates between keys because the brief asks for any time; a
    caller who follows the spec should sample only at `KeyTimes`.
12. Node `rotation` and animation rotation keys use different severities for the
    same 1e-3 tolerance (error versus warning); see the table above.

## What the oracle did not verify

* It has not been run on any real Greenware output; only on its own fixtures.
* The Khronos validator was not run (not available).
* The rig and pose readings are tested on a two-joint strip and on small fixtures
  with hand-computed values; they have not been run on a real athlete file.
* Normalized-integer rotation outputs are accepted and decoded; no test samples a
  normalized-short clip through `PoseAt`.
* Outline measurements (`outline(...)`) are not implemented: they need the protected
  reference mask and camera sidecar.
* Appearance, skin, clip and morph contracts are only structurally checked.

## Sparse accessor readings (`glb`, `measure`)

Written from glTF 2.0 section 3.6.2.3 (sparse accessors) and the schema pages
`accessor.sparse.*`. Codes are `SPARSE-*`; each malformed block gives one
finding (the first fault) and the accessor then reads as unavailable, so no
second finding follows from the same fault.

| Rule | Reading taken | Where |
|---|---|---|
| Base | An accessor with no `bufferView` has a base of all zeros. With a `bufferView` the base is those values. Sparse values then replace the base values at their indices: they overwrite, they are not added. The same overlay is used for every float or integer accessor, so a sparse base POSITION that a primitive attribute uses is read like any other. | `File.raw` |
| `sparse.count` | Must be at least 1 and at most the accessor count (`SPARSE-COUNT`). A count equal to the accessor count is valid (it replaces every element) and is read normally. The schema says `minimum: 1`; the oracle reports 0 and negative counts as `SPARSE-COUNT`, not as a schema range error, so the code is stable. | `validateSparse`, parser |
| Index type | 5121, 5123 or 5125 only (`SPARSE-INDEX-TYPE`). | |
| Indices | Strictly increasing and each below the accessor count (`SPARSE-INDEX`, path `/accessors/N/sparse/indices/K` for the first bad entry). A duplicate counts as not increasing. The oracle does not distinguish "out of order" and "duplicate" by code, only by message. | |
| Views | The `bufferView` of `indices` and of `values` must exist (`SPARSE-VIEW`) and must set neither `target` nor `byteStride` (`SPARSE-VIEW-USE`), per the schema text. | |
| Alignment | The schema says the view and the optional `byteOffset` must be aligned to the component size. Read as: `byteOffset` and the sum of the view's `byteOffset` and `byteOffset` must each be a multiple of the component size. For indices the size is the index type; for values it is the accessor's component type (`SPARSE-ALIGN`). Sparse values are not padded per element; elements of matrix types keep the accessor's column padding. | |
| Length | Indices need `count * index size` bytes and values need `count * element size` bytes after their offsets, inside the view (`SPARSE-BOUNDS`). The check is against the `bufferView` length, not the buffer length. | |
| min and max | POSITION `min`/`max` must equal the extrema of the applied values (base plus overlay), exactly in binary32, the same rule as dense accessors (`ACC-MIN`, `ACC-MAX`). Min and max written for the base alone fail when a sparse value is outside them. | `checkAccessorData` |
| Finite floats, normals | Checked on the applied values: a NaN in a sparse value is `ACC-NONFINITE`; a zero-length sparse normal is `NORMAL-ZERO`. | |
| Morph targets | Target accessors may be sparse (typical) or dense. `measure` reads a target through the same applied values. `moved_vertices` counts vertices whose POSITION delta has any nonzero component, over all primitives of the mesh (NORMAL and TANGENT deltas are not counted). `vertices` is the sum of the POSITION accessor counts. `sparse` on a target is true when its POSITION accessor is sparse in any primitive (else any other attribute accessor of the target). The glTF guidance is sparse when fewer than about half the vertices move; the report shows both numbers and applies no rule. | `measure.Rig`, `morphTargets` |

Contradictions and unresolved points:

* The schema text for `sparse.values.bufferView` says "MUST NOT have its target
  or byteStride properties defined", while the accessor text says nothing about
  a base view with a `target`. The oracle only applies the target/stride rule
  to the sparse views.
* The specification does not say what a validator does with an accessor whose
  `bufferView` is absent and has no `sparse`: the base is zeros. The oracle
  reads zeros and writes no finding. A POSITION attribute of that kind still
  needs `min`/`max` (`POSITION-MINMAX`), and they are checked against zeros.
* The specification leaves the byte alignment of the sparse `values` view open
  for types made of several components. The oracle uses the component size, not
  the element size, as it does for dense accessors.
* `FuzzPipeline` (measure) runs very slowly under the fuzz engine (about 10
  executions in 30 s) because of its heavy seeds (`post`, `upright`) and the
  exact shell-contact test. It did the same with the sparse seeds removed, so it
  is not caused by the sparse work. It finds no failure.
