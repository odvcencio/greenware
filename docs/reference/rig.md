# Rigging: skeletons, skin, morphs and clips

This page describes what the compiler reads for a rigged asset (milestone M4). The language reference (section 5) is in the Hyphae space `hypha://m31labs/greenware`. Where this compiler differs from it, the page says so. Distances are written in the asset's length unit and written to the file in metres; angles are written in the asset's angle unit.

The smallest rigged asset is `examples/arm/arm.gw`. The athlete, `examples/athlete/athlete.gw`, uses every construct below.

## Skeleton

```
skeleton body {
  joint_order = [pelvis, chest, head]   // every joint once; default is depth-first order
  root_scale = height                   // a numeric runtime parameter
  joint pelvis {
    at = (0, 1, 0)                      // model-space bind position
    orient = rot(0, 0, 10)              // model-frame bind orientation
    twist = -40..40  swing_x = -70..30  // natural bands
    limit { swing_x = -90..60 }         // hard limits
    socket towel { kind = towel at = (0.09, 1.04, -0.14) }
    joint chest { at = (0, 1.12, 0) }
  }
}
```

- Exactly one joint has no parent (`GW-RIG-STRUCTURE`). `at` is a model-space point, not an offset from the parent.
- The local rest of a joint is the inverse of its parent's bind composed with its own. Inverse binds are the exact inverse of the adjusted world rest, in mesh space.
- A socket's explicit `at` is a model-space bind point. It is converted once to the joint's frame, so a socket at the joint's bind position has local translation zero.
- `mirror { plane = x ... }` inside a skeleton, a skin, a morph or a pose adds a reflected copy. Names declared inside are pairs `(left, right)`. In the copy, value expressions read the original names with `side` equal to -1, and the result is reflected once by slot: points and directions by the plane, rotations by the quaternion reflection, signed angle ranges by reversing and negating them when the component changes sign.
- Twist axis: `twist_axis` if given, else the frame axis closest to the bone (ties choose x, then y, then z). A swing range about the twist axis is refused.

## Binding and skin steps

A shape or part binds rigidly with `joint = name`; the nearest enclosing part's joint is inherited. A shape with no joint in a skinned asset is `GW-RIG-UNBOUND`. A collider's `joint` is recorded in its node extras.

```
skin {
  max_influences = 2
  lengthen { joint = arml to = 0.355 anchor = 0.5 rigid = [shoulder_pad_l] }
  blend waist { parent = pelvis child = chest centre = joint(chest) axis = (0, 1, 0) width = 0.07 }
}
```

- Steps run in written order. `solve` (below) is a step too. `lengthen` needs a joint with one child; `blend` needs a parent and its immediate child and a nonzero axis.
- `joint(name)` in `centre` or `about` reads the bind the earlier steps left. Only `joint(name)` and `joint(name) + point` (or `-`) are read.
- Weights are sorted by weight, then joint index; zero weights are dropped and duplicates are combined. More than `max_influences` fails with `GW-RIG-STRUCTURE`; nothing is dropped silently.

## Solved skin

`solve` is the weight solver of milestone M5. It chooses influences for joints you supplied. It never places, moves or adds a joint, and it is experimental: it is not runtime safe and the browser worker refuses it (`GW-RUNTIME-UNSAFE`). The language reference (7.8) proposes `solve { method = ... max_influences = ... refine = ... }` and names three methods (geodesic voxel binding, heat diffusion, bounded biharmonic weights). This compiler reads one method of its own, `falloff`, which is none of the three, and does not yet require `experimental organic` in a manifest or policy (the compiler has no manifest gate for it).

```
skin {
  max_influences = 2
  lengthen { joint = shoulder to = 0.32 anchor = 0.5 }
  solve { method = falloff refine = smooth iterations = 64 tolerance = 0.0001 reach = 1 }
}
```

`examples/arm-solved/arm-solved.gw` is the arm with two `blend` steps replaced by one `solve`.

One `solve` per skin, outside any `mirror`. It replaces the weights of every position that is not in a `rigid` shape, so write the steps that move joints (`lengthen`) before it. A `blend` after it works on the solved weights.

| Field | Default | Meaning |
|---|---|---|
| `method` | `falloff` | The only method. |
| `max_influences` | the skin's | Weights kept on a vertex, 1 to 4, not above the skin's. |
| `refine` | `smooth` | `smooth` runs the bounded smoothing; `none` keeps the falloff weights; `audit` is `GW-UNSUPPORTED`. |
| `iterations` | 64 | Cap on smoothing sweeps, 1 to 256. |
| `tolerance` | 0.0001 | The solve has converged when a sweep changes no weight by more than this. |
| `smooth` | 2 | Strength of the smoothing against the falloff weights, above 0 and up to 4. |
| `power` | 2 | Falloff exponent, 1 to 4. |
| `own` | 128 | Factor on the weight of a joint the vertex is already bound to, at least 1. |
| `cutoff` | 0.02 | Taken off the share of each weight; a share that reaches 0 is dropped, so a vertex far from every blend ends rigid on one joint. |
| `soft` | 5 mm | Softening distance of the falloff. |
| `reach` | 1 | Joint-tree steps from a vertex's own joint that may take weight. |
| `rigid` | none | Shapes that keep the weights they have. |

### Algorithm

Distances are in metres. A vertex's own joints are the joints that carry weight on it before the solve (the shape's `joint`, after earlier steps). Its candidates are its own joints and every joint within `reach` steps of one of them in the joint tree. A weight on any other joint is never given.

1. Bones. The bone of joint c is the set of segments from c to each of its children. A leaf has one segment from c to the centroid of the positions that carry weight on it before the solve, each counted by its weight, because that is where its geometry is; a leaf that carries nothing has one segment from c along its parent's bone direction for the length of that bone. A lone joint is a point. The joint positions are the binds the earlier steps left.
2. Falloff. For a candidate c, d2 is the squared distance from the vertex to the nearest segment of c, and w = g / (d2 + soft^2)^power, where g is `own` for an own joint and 1 for another. The candidate weights are divided by their sum.
3. Smoothing (`refine = smooth`). Jacobi sweeps over the mesh edges of each shape, for each candidate c of a vertex p: `w[p][c] <- (w0[p][c] + smooth * sum of w[n][c] over the neighbours n of p) / (1 + smooth * deg(p))`, where w0 is the falloff weight. A neighbour's weight on a joint that is not a candidate of p counts as 0. The sweep is a contraction (it solves `(I + smooth L) w = w0`), so it converges. The residual of a sweep is the largest change of any weight in it. The sweeps stop at the first residual not above `tolerance`.
4. Finish. `cutoff` comes off every share, the `max_influences` largest weights stay (ties go to the lower joint index), and the weights are written on the grid of multiples of 2^-24: all but the largest are rounded half to even and the largest takes the rest. The weights are exact `float32` values, none is negative, and they sum to exactly 1.

If the residual is still above `tolerance` after `iterations` sweeps, the build is refused with `GW-SOLVE`, naming the iteration count and the residual. Nothing is written: no partly solved weights reach a file. A vertex with no usable candidate is `GW-SOLVE` too.

Vertices, joints, edges and segments are visited in index order and there are no maps in the numeric path, so the bytes do not depend on the architecture (`kernel/rig/lint_test.go` checks it, and `TestSolvedArmDigestMatchesCandidate` pins the arm's digest on amd64 and arm64).

### What the audit says

`go test ./compiler -run TestSolveFinalVectorMatchesRecord -v` builds the frozen athlete (`testdata/athlete-frozen/`, with the clip library of `examples/athlete/clips/` installed, 27 clips) and the arm with the hand skin and with `solve { }`, runs the audit of `check rig` over the default runtime points, and compares every joint on four measures (worst edge strain, strained share, torn share, lowest vertex at ground contacts), over all poses and for each clip alone. A measure is regressed when solved > hand + eps (strain 0.001, shares 0.0005) or, for the lowest vertex, solved < hand - 0.1 mm. `testdata/solve/protocol.json` records the grid, the folds, the held-out splits, the verdict and the whole table; rerun the grid with `GW_SOLVE_PROTOCOL=1 go test ./compiler -run TestSolveProtocolMatchesRecord` (about 12 minutes).

Result (not qualified): the defaults (own 128, cutoff 0.02, smooth 2) were chosen on the arm and the upper body and pass on the legs, knees, ankles and feet (held out, 0 regressed rows), but regress on 21 rows of the upper body: worst edge of both hands on 10 clips each (for example `caught_l` 1.0975 to 1.1207) and the chest on `catch` (1.1210 to 1.1318). Tuned on the arm and the legs instead, no vector passes the upper body (best: 27 rows). The leaf-bone fix removed the ankle regression of the first table (worst edge 3.38 against 2.90; lowest vertex 1.1 mm and 0.6 mm lower); the rest of that came from `own` and `cutoff`. M5 stays partial.

## Cost ticket for the solver

A level whose skin has a `solve` reserves one more ticket after `weights`: `solve`, 1,000,000 units per level, class one solve step, at most 256 sweeps, 64 joints and 16,384 bound positions. The price is this compiler's assumption. An asset with no `solve` reserves none, so the other receipts do not change.

## Morphs

```
morph tall {
  weight = clamp((height - 1) / 0.08, 0, 1)
  stretch { axis = y from = 0 to = 1.62 by = 8% }
  joints = follow
}
```

- Operations: `inflate`, `stretch`, `reshape`, `offset`, run in order on a copy of the finished bind mesh at weight 1. Each target is built from the bind mesh, never from another target.
- `girth` widens the rings of a loft about their centres by amounts that depend on height:
  ```
  girth {
    parts = [thigh_l]
    side = 0.06 * pow(sin(180deg * clamp((y - 0.555) / (1.057 - 0.555), 0, 1)), 2)   // x offset from the ring's centre
    front = 0.14 * smoothstep(0.6, 0.9, y)                                            // z offset, in front of the centre
    back = 0.09 * smoothstep(0.6, 0.9, y)                                             // z offset, at or behind the centre
  }
  ```
  `side`, `front` and `back` are expressions of `y`, the position's height in the asset's length unit as a plain number; each gives a fractional change (0.1 is 10 %), and an absent one is 0. A position p of a shape moves from the centre c of its ring, the point of the shape's centre line at p's height, by `(p.x - c.x) * side(y)` along x and `(p.z - c.z) * front(y)` (when p.z < c.z) or `back(y)` along z. Heights never change. The finished bind mesh is read, so `y` is the height after `lengthen`. An amount cannot read a runtime parameter (`GW-RUNTIME`; put the parameter in the morph's `weight`); a shape without a centre line is `GW-UNSUPPORTED`. `girth` is allowed in a `mirror` and is symmetric under it. The athlete's seven targets are written with it (`athletebody.GirthAt` of the game).
- `inflate` grows a sphere, ellipsoid, cylinder, lathe, loft, sweep or tube about its centre line. The line rides the skin steps with the shape, so it is the finished line.
- `weight` is a constant or a function of one runtime parameter that is a finite table. A numeric parameter gives breakpoints over its full declared range (the compiler reads `+ - *` by a constant, `/` by a constant, `clamp`, `min`, `max` and `abs` as piecewise-linear functions and adds the corners); an enum gives a total lookup. A product of two parameters, `sin`, or two parameters is `GW-RUNTIME`.
- `joints = follow` is allowed with `stretch` only. The joint offsets are the difference of the parent-local rests, written in the runtime table.
- Targets are written dense (package gltf has no sparse accessors). Normal deltas are the difference of the recomputed per-shell normals.

## Clips

```
clip ready {
  duration = 0.9s  keys = 13  loop = on
  let wob = sin(360deg * t)             // t runs 0..1 over the clip
  pose { pelvis = rot(0, 0, 0)  root = (0, 0.97, 0) }
}
clip field_x { name = "field-x"  node = "field lateral"  key (0s, (-64, 0, 0))  key (128s, (64, 0, 0)) }
```

- A pose clip samples `keys` values at `duration * i/(keys-1)`. A joint with no entry holds its bind rotation; the root's `root` entry is an absolute local translation. A pose rotation Q is in the bind frame; the written rotation is R0 Q for the local rest rotation R0.
- A keyed clip drives a wrapper node named in the asset's `wrappers` (outermost first). `sample v in lo..hi by n -> key (...)` expands to `n` rows. Key times must increase strictly and may not exceed `duration`; nothing is sorted or repaired (`GW-CLIP-KEY`).
- Every channel starts at 0 s and ends at the duration (a copy of the first and last value is added when needed).
- A skinned asset needs `idle`. The joint nodes hold the idle clip's first key, so an unplayed file shows no bind pose. A joint property that the idle clip sets away from the adjusted rest and another clip leaves out gets a constant channel at the adjusted rest.
- Translation keys are written as the key plus the weighted parent-local offsets at the default point; for the keyed root with `root_scale` the up component is the key's times the scale, with no added offset. `rig_base` and the runtime table in the file's extras let a reader rebuild these for another point.
- Every pose key is tested against the joint's bands and hard limits (swing and twist). A band is `GW-RIG-BAND`, a warning; a hard limit is `GW-RIG-LIMIT`, an error. Poses are never clamped.

## Clip libraries

A clip library is a project file that declares clips and no asset. An asset installs the clips with `use_clips` inside its `asset` block:

```
asset athlete {
  ...
  idle = ready
  use_clips { of = football skeleton = body }
  use_clips { of = solved }
}
import "clips/football.gw" as football
import "clips/solved.gw" as solved
```

- The import path follows the project import rules: relative to the file, inside the project root. An import that a `use_clips` block names is read as a clip library. Any other project import is gear (a whole asset, for a `mount`).
- `of` is the qualifier of the import. `skeleton` is optional because an asset has one skeleton; when written it must be that skeleton's name. A library that declares an asset, a skeleton, a skin, a morph, an import or a parameter is refused. A library may declare `let` values; its clips read them.
- Every clip whose name does not start with `_` is installed, with its declared name and its exported `name`. A clip named `_helper` is private. Importing a library without `use_clips` installs nothing, and the build fails because the file is then read as gear that has no asset.
- Installed clips count like local ones. `idle` may name an installed clip. Two clips that export one animation name are `GW-DUPLICATE` even when their keys are equal. The file lists its animations in order of exported name.
- A library clip is evaluated in the library's scope, against the asset's skeleton. Joint names, and the pairs a `mirror` block reflects, are the asset's. The library is written for one joint interface and one bind frame and is not retargeted: a joint the skeleton lacks is `GW-UNKNOWN-NAME`. A library has no `length` setting, so a bare length is metres; write a unit (`0.97yd`) where the asset's unit is not metres. A bare angle is degrees.
- A problem found inside a library is reported at the `use_clips` block that installs it, with the library's path in the message, because a span of the library's source means nothing in the asset's. The library's bytes are pinned with the project files the build read.
- A pose that has no closed form in `t` is written as key tables: `let legl_at = [rot(...), rot(...)]` and `legl = legl_at[k]` with `let k = round(t * 24)`. Lists and indexing are the language's own.

`examples/athlete/clips/` holds the athlete's library: the 27 body clips of the game's `cmd/bakeathletes/pose.go`, in two files. `football.gw` has the 15 that are formulas (`ready`, `stance`, `run`, `carry`, `sprint`, `block`, `throw`, `catch`, `handoff`, `juke`, `tackle`, `cut-l`, `cut-r`, `stumble`, `kick`, with their names, durations and key counts). `solved.gw` has the 12 the game computes numerically: the four paired tackles (`wrap-l`, `wrap-r`, `caught-l`, `caught-r`) as key tables, and the lateral footwork (`backpedal`, `shuffle-l`, `shuffle-r`, `crossover-l`, `crossover-r`, `hip-flip-l`, `hip-flip-r`, `kick-slide`) with the trunk and arms as formulas and the solved legs as tables. The four wrapper clips (`field-x`, `field-z`, `stance-height`, `heading`) stay in the asset, as keyed clips on its `wrappers`. The library includes `ready` and `run`: an asset that installs it declares neither itself.

`testdata/clips/fixture.gw` is a 15-joint asset with the athlete's joint names that installs both files. `TestClipLibraryMatchesTheGameBake` (`compiler/clips_test.go`) builds it, decodes the GLB, and compares each clip with `testdata/clips/vectors.json`: five keys of each of the 27 clips (the rotation of every joint and the root) and every key of the four wrapper clips, from the game's own code. The distance between two quaternions, taken as the smaller of the two signs, must be at most 1e-6, and a root height at most 1e-6 m after one conversion from yards. Set `GW_CLIP_VECTORS` to a dump with every key to check them all. `TestClipFixtureIsByteStable` builds the fixture twice and pins a digest of its decoded animations (`testdata/clips/animations.candidate.sha256`), measured on amd64 and checked on arm64.

## Not supported (all `GW-UNSUPPORTED`)

`mount` and `fit` (they need a project import), `check rig` and `check runtime` (the posed audit and runtime points), clip `ground`, `interpolation = cubicspline`, `when` and `waive` inside a rig body, `solve` inside a mirror or a second `solve`, `refine = audit`, the geodesic, heat and biharmonic methods, a second skeleton, a mirror inside a mirror, `joints = follow` with an operation other than `stretch`, an exported part with shapes inside a skinned asset, `lod keep`, and sparse morph targets. `side` is not yet refused in a point slot.

## Cost tickets

A rigged output reserves four more tickets. Their names, prices and classes are this compiler's assumption (compiler.md 3.1 names none): `rig` (100,000; at most 64 joints and 64 sockets), `weights` (300,000 per level; 32 steps, 16,384 bound positions), `morph` (150,000 per level with targets; 10 targets) and `clip` (200,000; 64 clips, 4,096 keys).
