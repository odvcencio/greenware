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
| `smooth` | 0.5 | Strength of the smoothing against the falloff weights, above 0 and up to 4. |
| `power` | 2 | Falloff exponent, 1 to 4. |
| `own` | 16 | Factor on the weight of a joint the vertex is already bound to, at least 1. |
| `cutoff` | 0.02 | Taken off the share of each weight; a share that reaches 0 is dropped, so a vertex far from every blend ends rigid on one joint. |
| `soft` | 5 mm | Softening distance of the falloff. |
| `reach` | 1 | Joint-tree steps from a vertex's own joint that may take weight. |
| `rigid` | none | Shapes that keep the weights they have. |

### Algorithm

Distances are in metres. A vertex's own joints are the joints that carry weight on it before the solve (the shape's `joint`, after earlier steps). Its candidates are its own joints and every joint within `reach` steps of one of them in the joint tree. A weight on any other joint is never given.

1. Bones. The bone of joint c is the set of segments from c to each of its children. A leaf has one segment from c along its parent's bone direction for the length of that bone. A lone joint is a point. The joint positions are the binds the earlier steps left.
2. Falloff. For a candidate c, d2 is the squared distance from the vertex to the nearest segment of c, and w = g / (d2 + soft^2)^power, where g is `own` for an own joint and 1 for another. The candidate weights are divided by their sum.
3. Smoothing (`refine = smooth`). Jacobi sweeps over the mesh edges of each shape, for each candidate c of a vertex p: `w[p][c] <- (w0[p][c] + smooth * sum of w[n][c] over the neighbours n of p) / (1 + smooth * deg(p))`, where w0 is the falloff weight. A neighbour's weight on a joint that is not a candidate of p counts as 0. The sweep is a contraction (it solves `(I + smooth L) w = w0`), so it converges. The residual of a sweep is the largest change of any weight in it. The sweeps stop at the first residual not above `tolerance`.
4. Finish. `cutoff` comes off every share, the `max_influences` largest weights stay (ties go to the lower joint index), and the weights are written on the grid of multiples of 2^-24: all but the largest are rounded half to even and the largest takes the rest. The weights are exact `float32` values, none is negative, and they sum to exactly 1.

If the residual is still above `tolerance` after `iterations` sweeps, the build is refused with `GW-SOLVE`, naming the iteration count and the residual. Nothing is written: no partly solved weights reach a file. A vertex with no usable candidate is `GW-SOLVE` too.

Vertices, joints, edges and segments are visited in index order and there are no maps in the numeric path, so the bytes do not depend on the architecture (`kernel/rig/lint_test.go` checks it, and `TestSolvedArmDigestMatchesCandidate` pins the arm's digest on amd64 and arm64).

### What the audit says

`go test ./compiler -run TestSolvedAthleteAuditAgainstHand -v` builds the athlete with its hand skin and with the blends replaced by `solve { }`, runs the audit of `check rig` over the default runtime points, and prints the measures per joint. The audit measures the geometry bound to a joint before the skin steps: the largest edge strain (r or 1/r, r the posed edge length over its bind length), the worst share of edges outside 1/1.5..1.5 and outside 1/4..3, and the lowest posed vertex. On the athlete's two skeletal clips (`ready`, `run`) the solved skin is better than the hand skin at 11 joints and worse at both ankles (worst edge 3.38 against 2.90; lowest vertex 1.1 mm and 0.6 mm lower, still inside the -5 mm..20 mm band). The solver does not qualify for M5: that needs no regression on any measure, over all 19 body clips, and the 19-clip library is not shipped here.

## Cost ticket for the solver

A level whose skin has a `solve` reserves one more ticket after `weights`: `solve`, 1,000,000 units per level, class one solve step, at most 256 sweeps, 64 joints and 8,192 bound positions. The price is this compiler's assumption. An asset with no `solve` reserves none, so the other receipts do not change.

## Morphs

```
morph tall {
  weight = clamp((height - 1) / 0.08, 0, 1)
  stretch { axis = y from = 0 to = 1.62 by = 8% }
  joints = follow
}
```

- Operations: `inflate`, `stretch`, `reshape`, `offset`, run in order on a copy of the finished bind mesh at weight 1. Each target is built from the bind mesh, never from another target.
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

## Not supported (all `GW-UNSUPPORTED`)

`mount` and `fit` (they need a project import), `check rig` and `check runtime` (the posed audit and runtime points), clip `ground`, `interpolation = cubicspline`, `when` and `waive` inside a rig body, `solve` inside a mirror or a second `solve`, `refine = audit`, the geodesic, heat and biharmonic methods, a second skeleton, a mirror inside a mirror, `joints = follow` with an operation other than `stretch`, an exported part with shapes inside a skinned asset, `lod keep`, and sparse morph targets. `side` is not yet refused in a point slot.

## Cost tickets

A rigged output reserves four more tickets. Their names, prices and classes are this compiler's assumption (compiler.md 3.1 names none): `rig` (100,000; at most 64 joints and 64 sockets), `weights` (300,000 per level; 32 steps, 8,192 bound positions), `morph` (150,000 per level with targets; 10 targets) and `clip` (200,000; 32 clips, 4,096 keys).
