# The athlete: mounts, tables, audits and the census

This page covers what the athlete needs beyond the rig (`rig.md`). The worked
file is `examples/athlete/athlete.gw`; its acceptance is
`examples/athlete/athlete.accept.gw`. The file is the body of the game's Go bake
(`cmd/bakeathletes`, `athletebody`) as Greenware source: the same ring tables,
sector counts, skeleton, skin steps and seven build targets, one leaf shape for
each of the bake's 72 shells (see "The athlete's body" below).

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
| `paint_large` | `paint` | 500,000 | 262,144 texels, 64 cells, 12,000,000 operations | 250,000 texels, 21 cells, 8,727,280 operations |
| `mount` | | 700,000 | helmet profile per gear output | 1 (home, away) |
| `surface_rig` | `surface` | 400,000 | 18,432 stored triangles per level | 14,782 (home and away, with the old helmet gear baked in; match has 13,016) |
| `audit` | | 400,000 | 256 points, 4,096 poses each, 16,384 positions | 50 points, 556 poses, 6,836 (home and away: 26 points, 8,048) |
| `fit` | | 200,000 | 256 points, 4,096 poses, 4,096 gear triangles | not used by the example |
| `encode_large` | `encode_images` | 250,000 | 4 images, 2 MiB per file | 3 images, 1,316,688 B |

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

## The athlete's body

`examples/athlete/athlete.gw` writes the game's Go-baked body. What each part of
the source answers to in the game, and what the compiler gained for it:

| Source | Game | Note |
|---|---|---|
| `loft`, `ellipsoid`, `tube` with the game's tables | `RingStack`, `SeamStack`, `Ellipsoid`, `Tube` of `assetpipe/author`; `athletebody` rings | Triangle and vertex counts equal the game's ledger, part for part (13,016 and 6,836 in all). |
| `seam = off`, `weld_caps = on` | `RingStack`, `Ellipsoid` and `Tube` write n vertices a ring and share the end rings with their caps | Both default to the old behaviour (a repeated column at u = 1; caps with corners of their own). They are left out of the meaning hash when at their default. |
| `girth` in `morph` | `athletebody.GirthAt` and `Displace` | See `rig.md`. |
| `lengthen`, `blend` | `proportion`, `blend` of `rig.go` | The same bands, widths and fades. |

Names. Each leaf shape has the name the game's bake gives its shell. A shape that
exists on both sides is a pair, and the census (`census.parts[]`) names it by the
pair member and the parts that hold it: `body/thigh_l` and `body/thigh_r` are the
game's two `thigh` shells. An `array` is one piece, named by the array (`finger_l`
is the game's four `finger` shells on that side; `lace_l` its three `lace`
shells; `maskclip_l` its two `maskclip` shells). Strip the `_l` and `_r` to get
the game's name.

Where the source differs from the game's bake, and why:

- The game has a thigh cell and a sleeve cell for each side, because its
  right-hand ring stacks are not mirrored; a mirrored copy keeps its original's
  UVs, so one cell serves both sides here. The game's `v` along a seamed shell is
  the distance along its profile; here it is the row share, as for any loft.
- The four fingers of a hand stand level at the mean height (0.9365 yd); the game
  staggers them by 5 mm. An `array` copy moves only by `step`: the array's `index`
  is 0 in every copy.
- There is no `height` parameter and no `tall` or `short` target: the game sets a
  player's stature as a uniform scale on the model, and its bake has neither. The
  other old targets (`neck-long`, `neck-short`, `brow`, `pad_size`) went too. The
  parameters `build`, `pads`, `belly` and `species` drive the seven targets, in
  ranges that carry the game's weights.
- The helmet shell, rim, ear cups, ear holes, chin straps, mask clips, chin pad
  and bumper are shapes of the body, as in the game. The `mount` of the old
  helmet gear (`examples/helmet`) is still declared, and `home` and `away` still
  bake it in, so they hold two helmets: they are fixtures for the mount and its
  fit check, not game files.
- The natural bands of the elbow (`swing_z`), the hip (`swing_z`) and the ankle
  (`swing_x`, `twist`) are a little wider than the old file's, because the game's
  lateral footwork and tackles measure outside them by up to 4 degrees under
  swing and twist (the game's own audit uses Euler angles).
- `check rig` takes the ground band -60..40 mm: the game roots each clip with a
  closed form that is exact for `ready` and off by +28 mm (`catch`) and -56 mm
  (`kick`) for others. `athlete.accept.gw` holds `ready` to -5..20 mm.

## `grid`: the same bytes on every machine

`output ... { grid = 20 }` snaps every number written to the GLB to a multiple of 2^-20 (about a micron in metres), so a build and the game's Go bake write the same bytes even when one machine rounds a product differently in the last place. `grid` is a whole number from 1 to 30; another value is `GW-FIELD-RANGE`. Absent, nothing is snapped and the file does not change.

The rule is `snap(v) = round(v * 2^n) / 2^n`, halves away from zero, never `-0`.

1. Before the file is written: mesh positions, UVs, skin weights and morph target deltas. After snapping, the largest weight of each vertex takes up the remainder, so the four still sum to exactly 1 (for `n` up to 24; a finer grid does not fit a 32-bit float). A sparse morph target keeps the vertices whose snapped delta is not zero.
2. After the file is written: every dense float accessor that has no bounds and no sparse part (normals, clip values, inverse bind matrices). Accessors with bounds (positions, clip times) keep them, and their data stays equal to the bounds. Clip times are not snapped.
