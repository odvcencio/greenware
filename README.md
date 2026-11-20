# Facet

Facet is a source language for 3D assets. One `.facet` file describes one asset (a prop, a building, a vehicle, a character). The compiler turns it into a glTF 2.0 binary (GLB), the same bytes every time, and checks it.

```
facet 1

asset crate { root = crate }

param size : length = 1m in 0.5m..2m

part crate {
  box { size = (size, size, size) }
}

output main { file = "crate.glb" }
```

```
facet build crate.facet --out-dir out
```

Status: early. The language, the compiler and the acceptance checker are under construction; see `CHANGELOG.md` for what works now.

## Layout

| Path | Job |
|---|---|
| `det/` | Deterministic scalar math (own module, `m31labs.dev/det`) |
| `gltf/` | Neutral GLB reader and writer (own module, `m31labs.dev/gltf`) |
| `language/`, `lang/` | The tree-sitter grammar, parser and formatter |
| `kernel/` | The geometry kernel |
| `cmd/facet` | The command-line tool |
| `conformance/` | An independent checker, in its own module |

Facet depends on no game engine. The core never imports GoSX or Kiln; `internal/depcheck` enforces that.

## Licence

MIT, copyright M31 Labs. Authored assets declare their own licences.
