# Placing parts, instances and shapes: `at`, `rot`, `scale`, `pivot`

These fields follow `language.md` sections 2.4, 3.3 and 3.4.

| Node | Fields | Matrix, for column vectors |
|---|---|---|
| `part` | `at`, `pivot`, `rot`, `scale` | `T(at) . T(pivot) . R(rot) . S(scale) . T(-pivot)` |
| `instance` | `at`, `rot`, `scale` | `T(at) . R(rot) . S(scale)` |
| a shape (`box`, `cylinder`, `sphere`, `sweep`, `lathe`, `loft`, `ellipsoid`, `tube`) | `at`, `rot` | `T(at) . R(rot)` |

A child's matrix multiplies on the right of its parent's. Every `scale` component must be greater than zero (`GW-FIELD-RANGE`); to reflect, use `mirror`.

## The rotation

`rot = (x, y, z)` is three angles in the asset's angle unit. `rot(x, y, z)` means the same.

- The turns are intrinsic X, then Y, then Z. For column vectors the matrix is `Rx . Ry . Rz`, so the Z turn acts on a point first. This is the order `language.md` states; it is the quaternion product `qx . qy . qz`.
- A positive turn about +X carries +Z toward -Y. The system is right-handed.
- An angle at a multiple of 45 degrees gives exact entries. A quarter turn is made of exact zeros and ones, so `rot = (90deg, 0, 0)` lays a `+y` cylinder along z with its ends at exactly `+-height / 2`. A turn of 45 degrees uses one value, the square root of one half, for both sine and cosine. Other angles use the deterministic sine and cosine of package `det`.
- Normals use the inverse transpose of the matrix. Winding stays outward, because a rotation and a positive scale keep the sign of the determinant.

## Shapes

A shape is built about its own origin, turned, and then moved by `at`. So `at` is never turned with the shape, and `ellipsoid { centre = ... }` turns its centre with the shape. A UV projection (`uv = box`, `cylindrical`, `spherical`, `planar`) and an atlas `cell` act on the shape before the turn, in its own frame. The triangle count is the same as without `rot`.

## Parts

A part with `rot`, `scale` or `pivot` moves everything inside it: its shapes, its child parts and its sockets. A socket's position goes through the part's matrix. A socket's own `rot` is still `GW-UNSUPPORTED`; a socket is a point to the compiler.

An exported part (`export = on`) is a node of its own whose translation is the part's `at` (plus the pivot term). The turn and the scale are written into its vertices, not into a node rotation, so the node carries a translation only.

## Not defined by the specification

The specification states the order of the turns, so nothing here is a choice, with two notes.

- It does not say how close to a multiple of 45 degrees an angle must be to count as that multiple. The compiler uses 1e-12 of an eighth turn, which covers `90deg` being 90 times a rounded degree.
- It does not give a pivot to an instance. An instance has none.
