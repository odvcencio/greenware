# The build record and the levels of detail

`greenware build` writes one `<asset>.build.json` next to the GLB files, schema `greenware.build/1`. It holds no time, path or host detail, so the same build gives the same bytes anywhere. `greenware build --json` writes the same per-output data to standard output. Fields are only ever added to this schema, never renamed or removed.

## `inputs`

An asset that imports a data package has an `inputs` object after `input_sha256`; an asset that imports none has no `inputs` key. `inputs.data` lists each package, sorted by contract: `{ "contract": "example/world@1", "profile": "standard", "sha256": "<digest>" }`. The digest is the pin of the package (`docs/reference/data-package.md`). The same list is in each GLB's `asset.extras.greenware.inputs`, and it is part of `input_sha256` and `fir_sha256`.

## `levels`

Every output has a `levels` array, sorted by `level`, one entry per file:

| Key | Meaning |
|---|---|
| `level` | 0 for the source, then 1, 2, ... as the `lod` blocks declare them |
| `file` | the file name, as in `files` |
| `distance_m` | the `distance` of the level's `lod` block, in metres (a number); `null` for level 0, which is the source and declares none |
| `max_error_m` | the `max_error` of the `lod` block, in metres; 0 for level 0 |
| `triangles` | triangles drawn in the default look, as in the census |
| `draws` | draws in the default look |

Example, from the goal post (`examples/goalpost`):

```json
"levels": [
  { "level": 0, "file": "goalpost.glb", "distance_m": null, "max_error_m": 0, "triangles": 1340, "draws": 3 },
  { "level": 1, "file": "goalpost-lod1.glb", "distance_m": 40, "max_error_m": 0.005, "triangles": 696, "draws": 3 },
  { "level": 2, "file": "goalpost-lod2.glb", "distance_m": 90, "max_error_m": 0.03, "triangles": 112, "draws": 1 }
]
```

The distance is the switch distance from the asset origin: a game shows level N while the camera is at least `distance_m` of level N away and less than that of level N + 1. Greenware does not read the distance when it builds geometry, so changing a `distance` changes the record and no GLB byte.

## `manifest = on`

`output ... { manifest = on }` still writes no GoSX asset manifest entry, because the specification does not define the entry's layout. The build prints a note saying so. The record's `levels` carry the data the entry would hold for distances, so a game can read them there.

## `census` and `census[].parts`

`census` has one row for every output and level, with the figures measured on the encoded file (`vertices`, `triangles`, `draws`, `materials`, `joints`, and so on). Every row also has `parts`: the share of each leaf shape in that file.

| Key | Meaning |
|---|---|
| `name` | the shape's name as written in the source, led by the names of the parts that hold it, joined by `/` (`body/thigh_l`); a shape without a name is called by its kind; the print backend names shapes the same way |
| `triangles` | triangles the shape wrote to the file |
| `vertices` | indexed vertices the shape wrote to the file |
| `material` | the name of the material the shape draws with, or `""` |

How the counts are owned:

1. The compiler welds vertices only inside one shape: vertices that are equal in position, normal and UV (and, in a rigged asset, in joints, weights and morph deltas) share an index within a shape and never across two shapes. Every indexed vertex in the file therefore has one owner, the shape that wrote it, and no vertex is counted twice.
2. The `triangles` of all parts add up to `triangles_stored` of the same file (the triangle count over every runtime branch); the `vertices` of all parts add up to the file's `vertices`. `triangles` in the census row is the count of the default look, so it can be smaller when a `when` branch is hidden by default.
3. Parts are listed in file order: primitive by primitive (one primitive per material of a node, in the order the materials first appear), and within a primitive in the order the shapes were written. A reader can cut a primitive's vertex and index ranges into parts by adding up the counts.
4. A mount built with `mounts = bake` adds one row named `mount/<mount name>` with the triangles and vertices its gear adds, and `material` `""` because the gear brings its own. These rows come last.
5. A shape with no triangles (a level that dropped it) has no row. A repeated or mirrored shape has one row for each copy, under its own name.

The `parts` field is only in the record and in `greenware build --json`; no GLB byte depends on it.
