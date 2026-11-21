// Package athleteparity measures a Greenware-compiled athlete against the
// numbers of the game's Go-baked athlete.
//
// The game (solmara-saturday, cmd/bakeathletes) prints two reports for its
// own athlete: the ledger (indexed vertices and triangles by body part, against
// the ceilings of deep consult 1, section B) and the silhouette table (widths
// of the posed body in helmet widths, for two named wearers and the roster
// medians). This package reproduces both from a Greenware source, so the two
// can be compared number by number:
//
//  1. Build compiles an athlete source with the compiler package and decodes
//     the GLB it writes with the gltf package. The per-shape census of the
//     build record says which shape owns which vertices and triangles.
//  2. Ledger sorts those shapes into the game's parts by shape name.
//  3. Pose folds the morph targets at a look's weights, scales the head joint
//     as the game does, poses a clip with linear blend skinning written here
//     (it shares no code with kernel/rig) and scales the body.
//  4. Measure takes the widths of the posed figure, choosing each band's
//     vertices by their bind height, exactly as the game's measure does.
//  5. Gate renders the front and side silhouettes and counts pixels, as the
//     deep consult's silhouette gate asks.
//  6. Compare reads the game's printed numbers and prints the difference.
//
// Football data (part names, ceilings, the silhouette bands) is allowed here
// and in examples; it stays out of the generic packages.
//
// The model is in metres and the game is in yards: every length is converted
// once, with 1/0.9144, when the bind positions are read.
package athleteparity
