# The athlete: mounts, tables, audits and the census

This page covers what the athlete needs beyond the rig (`rig.md`). The worked
file is `examples/athlete/athlete.gw`; its acceptance is
`examples/athlete/athlete.accept.gw`.

## Projects and imports

A project root is the nearest directory with a `greenware.mod` file above the
source (or `--project DIR`). `import "../helmet/helmet.gw" as helmet` names a
project asset, relative to the importing file. The path may climb with `..` as
long as it stays inside the root. An absolute path, a backslash or a path that
leaves the root is `GW-MISSING`. Files that import each other are `GW-CYCLE`.
The SHA-256 of each imported file (and of each table file) goes into the
build's input record; a comment in the gear changes the pin, not the product.

## Mounts

```
mount headgear {
  asset = helmet            // an imported project asset
  socket = head_gear        // a joint socket of this asset
  mode = runtime            // or bake
  set { shell_style = classic  facemask = cage }
  fit { clearance = 2mm }   // optional
}
```

The compiler finds the gear's plug of the socket's kind (among several, the one
named like the socket) and the one gear output whose bake parameters agree with
`set`. Every other failure is `GW-MOUNT` with the mount's name.

- `runtime`: the host file records the mount in its Greenware extras (socket
  node, plug, gear output and file, `set` values). The gear stays its own file.
- `bake`: the gear's default look (hidden branches left out) is merged into the
  host's skinned mesh, rigid on the socket's joint with weight 1, moved by the
  socket position minus the plug position. Its materials and images become the
  host's. The host's morph targets stay as they are: the gear's vertices carry
  no deltas.

An output's `mounts = runtime` or `mounts = bake` overrides every mount's mode.

`fit { clearance }` measures the posed gear triangles against the posed host
skin in the bind pose and at every key of every skeletal clip, at every runtime
point and level. A gap below the clearance, or a touch at clearance zero, is
`GW-MOUNT` naming the clip, key, point, gear part and host part.

The athlete's example leaves `fit` out. The ported numbers do not meet it: the
shell's lower front rim and the head loft's face lie within 0 mm of each other.
The acceptance file measures the same gap on the baked outputs.

## Tables and `for_each`

```
table schools = json { path = "build/schools.json"  columns = (key, main) types { key = string } }
output team { for_each = schools  file = "team-{row.key}.glb"  set { shirt = row.main } }
```

The file is a top-level array of flat objects. A column's type is the type its
uses need (`{row.key}` is a string; `param = row.col` is the parameter's type)
or the one `types` gives. A row that does not decode is `GW-TABLE-ROW` with the
table, the zero-based row and the column; an out-of-range value is
`GW-PARAM-RANGE`. A `key` column names each row's output `team[oak]` and must
be a safe, unique file name component. Split `.gltf` files (`buffer`, `image`)
are not written: package gltf writes GLB only.

## `lod keep`

`keep = [head, glove_l]` keeps the level-0 tessellation of the named parts and
shapes at that level.

## `check runtime` and `check rig`

Runtime points are the default, the product of the corners of `corners`, every
member of `each`, each `gear` output of each runtime mount, and the named
`case`s. Above `--runtime-points` (default 256) the audit keeps the default,
every case, one axis at a time and the declared pairs, and warns
`GW-RUNTIME-PARTIAL` with what it left out.

`check rig` measures, at every key of every skeletal clip and the bind pose of
every point and level:

| Measure | Rule | Code |
|---|---|---|
| ground | lowest skinned vertex of a clip that is not `airborne`, inside `ground` (default -5 mm..20 mm) | `GW-RIG-GROUND` |
| torn | share of geometric edges whose posed length is under 1/4 or over 3 times the bind length, at most `torn` (default 1%) | `GW-RIG-TORN` |
| bands | a pose outside a joint's natural band; `bands = error` makes it an error | `GW-RIG-BAND` |
| fit | the mount's clearance, above | `GW-MOUNT` |

`waist`, `legs`, `waist_ratio` and `open` are read and not applied: the waist
section, the seam probe and the hip crossing (language reference 5.9, items 3
and 4) are not measured, and the build says so in `unsupported`.

## The census, the profile and the classes

`greenware build --json` and the build record list, for every output and level:
images (count, decoded and encoded bytes), file bytes, vertices, triangles
(default, worst, stored), draws, materials, joints, influences, morph targets,
animations and keys. The record also holds the audit of each level.

Decision 0018 (H2): budgets are measured from the census. An output with a
skeleton and a mount, an audit or an atlas uses the athlete profile's tickets.
These prices and bounds are this compiler's assumption, with the measured
figures beside them:

| Ticket | Replaces | Price | Class | Measured |
|---|---|---:|---|---|
| `paint_large` | `paint` | 500,000 | 262,144 texels, 64 cells, 12,000,000 operations | 262,144 texels, 12 cells, 7,502,000 operations |
| `mount` | | 700,000 | helmet profile per gear output | 1 (home, away) |
| `surface_rig` | `surface` | 400,000 | 10,240 stored triangles per level | 8,028 |
| `audit` | | 400,000 | 256 points, 4,096 poses each, 8,192 positions | 98 points, 39 poses, 4,465 |
| `fit` | | 200,000 | 256 points, 4,096 poses, 4,096 gear triangles | not used by the example |
| `encode_large` | `encode_images` | 250,000 | 4 images, 2 MiB per file | 3 images, 1,074,692 B |

The old classes still refuse the athlete's figures, and tests pin that. The
whole athlete costs 14,150,000 of the 20,000,000 work units.

## Acceptance functions

`greenware accept` reads only the decoded file: `joint_count()`,
`morph_count()`, `animation_count()`, `has_animation(name)`,
`animations_present(names...)`, `runtime_points()`, `posed_lowest(clip =,
time =, <param> = value ...)`, `posed_position(node, clip =, time =, ...)` and
`mounted_clearance(clip =, time =)`. A pose follows reference 5.6: morph weights
from the runtime bindings, joint offsets, root scale, adjusted inverse binds.
`mounted_clearance` needs gear baked into the file and is unavailable otherwise.
