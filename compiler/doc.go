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
//   - Shapes: cylinder (caps, bevel), sphere, sweep (circle profile, path
//     rows, caps), lathe (rows, closed). Other shapes are GW-UNSUPPORTED.
//   - Composition: parts and nested parts, mirror (planes x, y, z; names
//     given as pairs), array (linear, grid, radial), instance, bake `when`.
//     A part's `at` is a translation. `rot`, `scale` and `pivot` on parts,
//     shapes, sockets and instances must be the identity.
//   - Materials: pbr, and selena through its pbr fallback. A runtime colour
//     or number parameter is written as its default value and recorded as a
//     binding in the runtime table.
//   - Levels: level 0 is the source. Levels 1 and up re-tessellate each shape
//     to the level's max_error and remove the geometry its `drop` list names
//     (and the lists of earlier levels) before merging.
//   - Sockets and colliders as meshless nodes, with their data in node extras.
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
//     not define its layout. Product.Unsupported says so.
//   - The Selena artifact is neither read nor pinned; only the fallback
//     material is written.
package compiler
