# Printing: 3MF

This page describes the first print backend: one level of one output, written as a 3MF file of solid bodies. It is a Go API (`compiler.BuildPrint`, `backend/threemf`); the command-line flag is the lead's to add. The design is in `compiler.md` section 8.3 in the Hyphae space `hypha://m31labs/greenware`.

## Use

```go
p, ds := compiler.BuildPrint(file, "stadium", 0)            // scale 1
p, ds := compiler.BuildPrintWith(file, "stadium", 0,
    compiler.PrintOptions{Scale: 0.01})                     // a 1:100 model
```

`p` is nil when any diagnostic is an error. Otherwise `p.Bytes` is the 3MF, `p.Name` its file name (`goalpost.3mf`, or `goalpost-lod2.3mf` for level 2), `p.SHA256` its digest, `p.Objects` one entry per solid (name, material, vertex and triangle counts, closedness, volume in mm3, bounds in mm, source span), `p.Lo` and `p.Hi` the bounds of the whole print, and `p.Notes` what the print does not carry or check.

## What is printed

- One object per shell of each shape, in the order the compiler walks the source. A radial array of eight bolts is eight objects (`goalpost/base/bolts.1` to `.8`). Names are the part names joined with `/`, then the shape's name or kind.
- Nothing is merged. The specification says a renderable closed shell is not necessarily a printable assembly and defines no rule that joins shells, so touching shells stay separate bodies. The painted frame of the goal post is a post, a crossbar, two uprights and two spheres, not one frame. `p.Notes` lists the pairs whose bounding boxes meet, as a hint and not as a proof of contact.
- The default look: a node that a runtime `when` hides at the default parameter values is left out.
- A level's own tessellation, with its `drop` list applied. `BuildPrint(file, out, 2)` prints level 2.
- Not printed: rigged assets (refused), mounts (refused), sockets, colliders, materials and colours.

## Units and frame

Assets are in metres with +y up. A 3MF is in millimetres with +z up. The conversion happens once, in `compiler/print.go`, when a vertex leaves the evaluated mesh: `(x, y, z)` metres become `(x, -z, y) * 1000 * Scale` millimetres. That is a 90 degree turn about x, which keeps handedness and winding. The model is not moved to the origin; an asset that stands on y = 0 stands on z = 0. An asset with `up = z` is not turned. Any other `up` is refused. `backend/threemf` takes millimetres and converts nothing.

## The file

The package has three stored (uncompressed) parts in bytewise name order: `3D/3dmodel.model`, `[Content_Types].xml`, `_rels/.rels`. Every part is dated 1980-01-01 00:00:00, has no extra field and no comment, and has its CRC-32 written. The model has `unit="millimeter"`, a Title (the root part's name), an `Application` of `greenware`, one `<object type="model">` per solid and one `<build><item>` per object with the identity transform.

Numbers are written with `threemf.FormatMM`: six decimals (a nanometre), rounded half to even on the exact value of the float64, and `0.000000` for any value that rounds to zero. Within an object, vertices that share a position in the evaluated mesh are one vertex, numbered by first use in triangle order; triangles keep the mesh's order. The same source gives the same bytes on every architecture; the tests run it with `GOMAXPROCS` 1 and 8, from two directories.

## The gate: GW-PRINT

`BuildPrint` encodes the file, reads the bytes back with `threemf.Decode`, and runs `threemf.Validate` on what it read. This is after the six-decimal rounding, which is what a slicer sees. It refuses with `GW-PRINT` when an object has:

| Finding | Meaning |
| --- | --- |
| `open-edge` | An edge used by one triangle: the shape is a surface, not a solid. A cylinder with `caps = off` is one. |
| `non-manifold-edge` | An edge shared by more than two triangles. |
| `inconsistent-winding` | Two triangles run the same way along an edge. |
| `degenerate-triangle` | A repeated vertex or position, or zero area. |
| `non-positive-volume` | Closed and consistent, but wound inward (signed volume 0 or less). |
| `index-range`, `empty`, `non-finite-coordinate`, `unit`, `object-id`, `build-item` | A malformed model. |

The diagnostic's span is the shape's own declaration (its name, else the nearest named part). Edges join vertices by exact position, as a slicer joins them. The GLB of the same source still builds.

## Not done

- Minimum wall thickness. The specification names the check (`FACET-PRINT-WALL`, under the former name) and gives no minimum thickness and no method, so there is no check. Every object is written as a solid body, so the notes say so. Add the check when the specification gives a number.
- Self-intersection and intersection between solids. A closed manifold shell that cuts itself, or two solids that cut each other, pass the gate. The notes list the pairs whose bounding boxes meet.
- A print variant. The specification allows "an explicitly modelled print variant" for shapes that intersect; the language has no such construct yet.
- The 3MF material subset, colours, components, transforms, and thumbnails.
- STL and OBJ.
- Cost tickets: `BuildPrint` does not reserve any, and relies on the level being bounded by the surface class that `Build` checks.
