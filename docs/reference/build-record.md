# The build record and the levels of detail

`greenware build` writes one `<asset>.build.json` next to the GLB files, schema `greenware.build/1`. It holds no time, path or host detail, so the same build gives the same bytes anywhere. `greenware build --json` writes the same per-output data to standard output. Fields are only ever added to this schema, never renamed or removed.

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
