// Package compiler is the Greenware back end: it turns a resolved output
// into glTF binary files.
//
// Build takes a lowered file and builds every output (or one), at every level
// of detail, from the values that package resolve computed. It returns the
// documents, the encoded GLB bytes, the measured facts (triangles, draws,
// materials, vertices, file size) and a deterministic build record. Write puts
// the files on disk in a fixed order and writes the record last, so an
// interrupted build never leaves a record that claims the set is complete.
//
// What the first compiler reads (milestone M2-S):
//
//   - Shapes: box (segments, bevel), cylinder (caps, bevel), sphere, sweep (circle profile, path
//     rows, caps), lathe (rows, closed), and, for the helmet (milestone M3,
//     geometry), loft (rows, columns, an author's vertex rule, arc, caps),
//     ellipsoid and tube (open or closed). Other shapes are GW-UNSUPPORTED.
//   - Templates: a row that calls a `template` expands where it stands and
//     adds no node.
//   - Runtime branches: a part whose whole body is a runtime `when` on a switch
//     or an enum member is a node of its own. A branch that is hidden at the
//     defaults is written outside the default scene, as a root of the scene
//     named "alternates", and every such node carries KHR_node_visibility. The
//     runtime table records each branch's node, intended parent, local
//     translation, scene and test. File.Triangles and File.Draws count the
//     default look; TrianglesWorst and DrawsWorst the largest look over every
//     assignment of the tested parameters; TrianglesStored every branch.
//   - Composition: parts and nested parts, mirror (planes x, y, z; names
//     given as pairs), array (linear, grid, radial), instance, bake `when`.
//     Parts are placed by `at`, `pivot`, `rot` and `scale`, instances by `at`,
//     `rot` and `scale`, shapes by `at` and `rot` (frame.go); a socket's `rot`
//     must be the identity.
//   - Materials: pbr, and selena through its pbr fallback. A runtime colour
//     or number parameter is written as its default value and recorded as a
//     binding in the runtime table.
//   - The look (milestone M3): `import "std/finish"` (ordinary Greenware source
//     embedded in package std, pinned by SHA-256 in the input hash), painters,
//     and `texture name = atlas { ... }` with named cells. The atlas is painted
//     on the CPU and written as PNG by package kernel/image, in colour or
//     palette mode. A `cell` on a part or shape maps the shape's chart into the
//     cell's inset rectangle (an explicit `uv` projection is applied first). A
//     Selena material with a palette mask writes its pbr fallback, whose
//     `base_texture = paint(mask)` is the mask painted with the material's
//     default slot colours, and also the raw mask as a second image: two
//     distinct images, so `texture_bytes` counts both. The Selena descriptor,
//     its default values and the mask texture index are recorded in the
//     material's extras as pending. A runtime slot colour is a binding on the
//     palette field; a runtime `alpha` is a binding on baseColorFactor.alpha.
//   - `plug name { kind = ... at = ... }` is a meshless node named plug.<name>
//     under the root, with its position in metres, and an entry in the asset's
//     `plugs`.
//   - Levels: level 0 is the source. Levels 1 and up re-tessellate each shape
//     to the level's max_error and remove the geometry its `drop` list names
//     (and the lists of earlier levels) before merging.
//   - Sockets and colliders as meshless nodes, with their data in node extras.
//   - Rigging (milestone M4, docs/reference/rig.md): a skeleton with model-frame
//     binds and mirrors, rigid binding and ordered skin steps (lengthen, blend),
//     morph targets with table weights and `joints = follow`, sampled and
//     keyed clips, wrapper nodes and an idle pose. They are written as glTF
//     joints, a skin with inverse binds, JOINTS_0 and WEIGHTS_0, morph targets
//     with names, and animations. Package kernel/rig does the math. A morph
//     target is stored as a glTF sparse accessor when that is smaller.
//   - The athlete (milestone M4, docs/reference/athlete.md): project imports,
//     `mount` (runtime: a record in the extras; bake: the gear's default look
//     merged into the skinned mesh, rigid on the socket's joint), `output
//     mounts`, `table` and `for_each`, `lod keep`, `check runtime` and `check
//     rig` (the audit of rigaudit.go: ground, torn edges, mount fit), and a
//     census of every output and level in the build record.
//   - Budgets, measured on the encoded files, at the output's effective budget.
//
// Cost: BuildWith reserves a fixed price for every operation of the planned
// run, in a fixed order, before it builds anything (cost.go). A ticket that
// does not fit under Policy.MaxTotalWork, or whose operand is over its class
// bound, stops the build with GW-LIMIT and no product. The reserved tickets
// are a Receipt, written into the build record.
//
// Where it differs from the specification, the package states it here:
//
//   - The runtime table and the other Greenware data are written in the
//     top-level glTF `extras` member under the key "greenware", not in
//     `asset.extras`, because package gltf has no asset extras.
//   - `manifest = on` writes no GoSX manifest entry: the specification does
//     not define its layout. Product.Unsupported says so. The build record
//     carries each output's levels with their `distance` and `max_error` in
//     metres (OutputProduct.Levels), so a game need not repeat them.
//   - The Selena artifact is neither read nor pinned; only the fallback
//     material is written, and the build notes record the artifact as pending.
//   - The look's tickets (`paint`, `encode_images`) are this compiler's
//     assumption, as are `select` and `surface_branches`; see cost.go. So are
//     the athlete's (costathlete.go).
//   - The audit does not measure the waist section, the seam probe or the hip
//     crossing (reference 5.9, items 3 and 4); the build says so in its
//     unsupported notes. `buffer`, `image` and split glTF are not written:
//     package gltf writes GLB only.
package compiler
