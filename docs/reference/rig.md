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

- Steps run in written order. `lengthen` needs a joint with one child; `blend` needs a parent and its immediate child and a nonzero axis.
- `joint(name)` in `centre` or `about` reads the bind the earlier steps left. Only `joint(name)` and `joint(name) + point` (or `-`) are read.
- Weights are sorted by weight, then joint index; zero weights are dropped and duplicates are combined. More than `max_influences` fails with `GW-RIG-STRUCTURE`; nothing is dropped silently.

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

`mount` and `fit` (they need a project import), `check rig` and `check runtime` (the posed audit and runtime points), clip `ground`, `interpolation = cubicspline`, `when` and `waive` inside a rig body, a second skeleton, a mirror inside a mirror, `joints = follow` with an operation other than `stretch`, an exported part with shapes inside a skinned asset, `lod keep`, and sparse morph targets. `side` is not yet refused in a point slot.

## Cost tickets

A rigged output reserves four more tickets. Their names, prices and classes are this compiler's assumption (compiler.md 3.1 names none): `rig` (100,000; at most 64 joints and 64 sockets), `weights` (300,000 per level; 32 steps, 8,192 bound positions), `morph` (150,000 per level with targets; 10 targets) and `clip` (200,000; 32 clips, 4,096 keys).
