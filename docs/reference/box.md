# The box shape

`box` draws a rectangular solid. It follows `language.md` sections 3.4, 3.5 and 3.10.

```
box wall {
  at = (0, 0.5m, 0)        // centre; default the part's origin
  size = (4in, 1m, 4in)    // full extent along x, y and z; required
  segments = 1             // equal divisions of each face along each edge; default 1
  bevel { width = 8mm  segments = 2 }
  material = foam
}
```

| Field | Meaning |
|---|---|
| `size` | Three positive lengths: the full extent along x, y and z. The box is centred on `at`. |
| `segments` | Divisions of each face along each of its two edge directions (`1` or more). A face has `segments x segments` quads, so a box has `12 * segments^2` triangles: 12 at one division. |
| `bevel` | Rounds all twelve edges. See below. |
| `rot` | Turns the box about its centre, then `at` moves it. See `transforms.md`. |

## Shape of the mesh

- Without a bevel the box is one closed shell. Each face has corners of its own with a flat normal, so a one-division box has 8 positions, 24 corners and 12 triangles.
- Winding is counter-clockwise seen from outside.
- UVs follow the box projection (`uv = auto` and `uv = box` give the same chart): each triangle takes the plane perpendicular to the largest component of its normal (ties x, then y, then z), using YZ, ZX or XY, and negates the first coordinate on a negative face. Each projected coordinate is normalised over the faces that share the plane and sign, so a plain box has one unit square per face. A `cell` maps that square into the atlas cell.
- A level of detail leaves a box unchanged: it has no circular division for the chord error to reduce.

## Bevel

`bevel { width = w  segments = b }` rounds every edge of the box with a quarter circle of radius `w` in `b` equal angular steps; `1` is a chamfer. The count is `12 + 24 b + 8 b^2` triangles: six trimmed face quads (12), twelve edge strips of `b` quads (24 b) and eight corner patches of `b^2` triangles (8 b^2). One step gives 44 triangles, as `language.md` 3.10 says.

- The corner patch is the triangular grid of weights `(i, j, k) / b`. A vertex on a patch boundary uses the same angular sample as the edge strip beside it, so the shell has no crack. An interior vertex points along the normalised weight vector.
- Edge strips and corner patches carry the normal of the round, so a flat face meets its bevel without a visible edge.
- The box must have `segments = 1` when a bevel is present (`language.md` 3.10). Otherwise the compiler reports `GW-FIELD-RANGE`.
- `width` must be smaller than half of every extent. A wider bevel would consume a face and reports `GW-FIELD-RANGE`.
- `edges` accepts `all` (the default). `top`, `bottom` and `caps` have no meaning for a box in the specification, so they report `GW-UNSUPPORTED`.
