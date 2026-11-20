# The look: textures, painters, palette materials and plugs

This page describes what the compiler reads for the helmet's look (milestone M3). The language reference (section numbers below) is in the Hyphae space `hypha://m31labs/greenware`. Where this compiler differs, the page says so.

## `import "std/finish"`

```
import "std/finish"
import "std/finish" as fin
```

An import makes its painters callable with a qualifier: `finish.gloss()`. The qualifier is the last path component unless `as` gives another. Two imports with one qualifier fail with `GW-DUPLICATE`.

The compiler ships one library, `std/finish`. It is ordinary Greenware source, embedded in the compiler (`std/finish.gw`). It has no hidden executable hooks. The build's input hash records the SHA-256 of every imported library, so a change to the library changes the input hash of each build that imports it. A project file import is `GW-UNSUPPORTED`, and so are the reserved libraries that are not shipped (`std/hardware`, `std/cloth`, `std/lettering`, `std/kinds`).

`std/finish` has three painters. Each returns opaque grey shading for the current cell:

| Painter | Look |
|---|---|
| `finish.gloss()` | A sky gradient, a bright vertical streak left of centre and a darker band along the lower edge. |
| `finish.satin()` | A gentle horizontal sheen with fine brushed lines every third texel row. |
| `finish.foam()` | Soft mottling from two scales of lattice noise; the seed and the cell index move the pattern. |

They use no `grain`, so the images compress well.

## Painters (4.9)

```
painter flat(base: color, k: number = 1) {
  let wrap = 0.62 + 0.38 * (0.5 + 0.5 * cos(360deg * u + 0.27rad))
  color = base * (0.88 + 0.12 * wrap) * k
}
```

A painter body holds `let` rows and one `color`. The body is checked once, when the file is read: names, types, units and calls are bound before any texel is painted.

- **Values** are numbers, angles (`360deg`), colours and switches. A length is `GW-UNIT`.
- **Colours** are sRGB-encoded RGBA as real components. `colour * k` and `colour + n` act on RGB and keep alpha. Nothing clips until storage, where each channel is clipped to `[0,1]`, multiplied by 255 and rounded to the nearest integer, ties to even.
- **Names**: the formals, earlier `let`s, `px` and `py` (the integer texel of the interior), `u` and `v` (0 to 1 across the inset area, at the texel centre), `cell` (the physical cell index), and the document's bake parameters and top-level `let`s. A painter cannot read a runtime parameter (`GW-RUNTIME`). A library painter reads none of the document's names.
- **Functions**: `abs clamp cos floor lerp max min mod pow round sin smoothstep sqrt tan`, and the two random samples `grain(sigma)` and `noise(scale)`.
- **Calls** between painters must not form a cycle (`GW-CYCLE`).
- **Cost**: the compiler counts one scalar operation for each operator, call and `if` (both branches of an `if`), and adds a called painter's count. Painting an atlas is charged `interior texels x count` before it runs.

A painter fault found while painting, such as a square root of a negative number, is reported by `greenware build` as `GW-DOMAIN` with the cell and texel. `greenware check` does not paint.

### The random sample (4.9)

`noise` and `grain` hash exactly 64 bytes with SHA-256: the eight little-endian 64-bit words `(domain, revision, function, seed, cell, x, y, lane)`, with domain 0, revision 0, function 0 for noise and 1 for grain. The first 13 hexadecimal digits of the digest are a 52-bit integer `h`, and `U = (h + 0.5) / 2^52`. Seed 0, cell 0, `noise(0)` hashes 64 zero bytes (`f5a5fd42d16a20302798ef6ed309979b43003d2320d9f0e8ea9831a92759fb4b`), and `#000000 + noise(0)` stores `f5 f5 f5 ff`.

## Atlas textures (4.8)

```
texture helmet_mask = atlas {
  grid = (4, 2)
  cell_size = 64px
  inset = 7%
  mode = palette
  seed = 0
  cell shell { slot = 0 paint = finish.gloss() }
  cell pad   { slot = 3 paint = finish.foam() }
}
```

| Field | Meaning |
|---|---|
| `grid` | `(columns, rows)`, at most 4096 cells. |
| `cell_size` | Side of a square cell in pixels. The image is `columns x cell_size` by `rows x cell_size`, at most 2048 on a side. |
| `inset` | Share of a cell reserved on each side, from 0 up to but not including 0.5. A cell size and inset that leave no texel centre inside are rejected. |
| `seed` | Input to `noise` and `grain`. |
| `mode` | `color` stores sRGB. `palette` stores shading in red (the painter's red channel), the slot byte in green, zero in blue and 255 in alpha. |
| `encode` | `png`. `jpeg(q)` is not supported. |
| `fill` | Painter or colour for the cells no `cell` names. |

Cells are numbered left to right, then top to bottom. An explicit `index` takes its place first; the unindexed cells then take the lowest free indices in cell-name order, so reordering declarations does not move a cell. A duplicate or out-of-range index and an overfull grid are errors. In a palette atlas every cell needs `slot` (0 to 255; several cells may share one); in a colour atlas `slot` is an error. A cell needs `paint` unless the atlas has a `fill`.

A texel outside the inset rectangle copies the nearest interior texel, so the gutter holds no new grain.

For a shape `cell = name` maps its chart into the cell: `U = (col + d + u (1 - 2d)) / columns` and `V = (row + d + v (1 - 2d)) / rows`, with `d` the inset and `(0,0)` at the top left of the image. A `cell` on a part is inherited. It names a cell of the atlases of the shape's effective material; if the material samples two atlases the cell must be the same rectangle in each (`GW-UV-CELL`). A chart that leaves the unit square is `GW-UV-CELL` too: the compiler never adds triangles to make it fit.

An explicit `uv` of `planar`, `box`, `cylindrical` or `spherical` replaces the builder's chart before the cell is applied. `planar` uses local XY, normalised over its bounds (0.5 where a dimension has no extent). `spherical` has `v = 0` at the south pole and `v = 1` at the north pole, with no flip. A seam changes corners, never triangles.

## Palette materials and the fallback (4.10)

```
material helmet_paint = selena {
  source = "materials/palette.sel"
  name = "PaletteMask"
  mask = helmet_mask
  slot0 = shell_color
  fallback = pbr { base_texture = paint(helmet_mask) roughness = 0.35 clearcoat = 1 }
}
```

The compiler writes the `pbr` fallback as the glTF material and does not read the Selena artifact. The build notes record `materials/palette.sel.json` as pending.

`paint(helmet_mask)` makes a colour image from the palette atlas and the containing material's default `slotN` colours. Each texel is the slot colour in linear light, times the decoded red shading, converted back to sRGB and stored; alpha is the slot colour's alpha. A used slot with no colour is `GW-MAT-PARAM`. A raw palette mask is not a base texture (`GW-MAT-PARAM`).

The file carries two distinct images, the raw mask and the painted fallback. Each is 256 x 128 for the helmet, so `texture_bytes` is 262,144 (256 KiB), not 131,072. A budget of 128 KiB fails with `GW-BUDGET`.

In the GLB:

- `images` hold the PNG bytes. `textures` pair each image with a sampler: the fallback with the material's `filter` and `wrap` (trilinear and repeat by default), the mask with nearest filtering.
- `materials[].pbrMetallicRoughness.baseColorTexture` is the fallback.
- A clearcoat is written with `KHR_materials_clearcoat`, both factors.
- The material's `extras.greenware` (`greenware.selena/1`) records the source, name, artifact status (`pending`), the descriptor values, and the palette (grid, cell size, inset, each cell's index and slot, the mask texture index and the fallback texture index).
- Runtime colours: `shell_color` and the other slot colours are bindings in the runtime table on the palette field (`slot0`, `target: palette`, map `srgb_to_linear`). A runtime `alpha` such as `visor_tint` binds `baseColorFactor.alpha` with the identity map.

`blend = on` is written as the glTF alpha mode `BLEND`; `alpha` is the fourth component of `baseColorFactor`. `double_sided = on` is written as `doubleSided`.

## Plugs

```
plug head_gear { kind = helmet at = (0, 1.7, -0.012) }
```

The plug is a meshless node `plug.head_gear` under the root, with its position in metres and `greenware.plug/1` extras. The asset's `extras.greenware.plugs` lists each plug with its position. `rot` on a plug is `GW-UNSUPPORTED`. Sockets and plugs share one namespace.

## Cost tickets

These tickets are this compiler's assumption (compiler.md 3.1 asks for them and does not price them). The M2 schedule and the goal post's receipt of 8,750,000 are unchanged.

| Ticket | Per | Class | Units |
|---|---|---|---:|
| `paint` | atlas of an output that a drawn material samples | at most 65,536 texels, 64 cells and 20,000,000 scalar painter operations | 250,000 |
| `encode_images` | level file of an output with images, in place of `encode` | at most 4 embedded images and 192 KiB | 200,000 |

## Diagnostics

`GW-UV-CELL` and `GW-MAT-PARAM` are new. The rest reuse existing codes: unknown names and cells are `GW-UNKNOWN-NAME`, a texture budget is `GW-BUDGET`, a class bound is `GW-LIMIT`.
