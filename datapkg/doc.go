// Package datapkg reads, checks and pins a typed data package: the
// project-local JSON that `import "football/world@1" as world` brings into a
// Greenware source (decision 0020, docs/reference/data-package.md).
//
// A package is three files inside the project root:
//
//   - a lock, `<logical>.lock`, strict JSON that names the other two files and
//     carries the SHA-256 pin of their canonical forms;
//   - a schema, which declares the contract id and, for every field, its
//     dimension (length, angle, ratio or number) and its meaning;
//   - a data file, which gives `{"value":"222","unit":"in"}` for every field.
//
// Load checks all of it and returns exact rationals. Nothing is fetched,
// defaulted or guessed. The same code verifies the pin for the compiler and
// for a game's generator, so the two cannot drift apart: Digest is the one
// definition of the pin.
//
// The package reads files through io/fs and does no floating-point math,
// except Value.Float64, which meets float64 once, at the end of an exact
// conversion.
package datapkg
