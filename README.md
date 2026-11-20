# Greenware

**Pre-1.0: the language, the compiler and the file format will change without notice.** Do not depend on it yet.

Greenware is a source language for 3D assets, named for shaped clay before it is fired. One `.gw` file describes one asset (a prop, a building, a vehicle, a character). The compiler turns it into a glTF 2.0 binary (GLB), the same bytes every time, and checks it.

```
greenware 1

asset post { root = post }

param height : length = 3m in 1m..5m

material steel = pbr { base = #8a8d8f roughness = 0.45 metallic = 1 }

part post {
  material = steel
  cylinder {
    radius = 0.05m
    height = height
    segments = 16
    caps = on
  }
}

output main { file = "post.glb" }
```

```
greenware build post.gw --out-dir out
```

Status: early. The grammar, the geometry kernel, the GLB codec and an independent checker exist; the compiler that joins them is under construction. See `CHANGELOG.md` for what works now.

## Layout

| Path | Job |
|---|---|
| `det/` | Deterministic scalar math (own module, `m31labs.dev/greenware/det`) |
| `gltf/` | Neutral GLB reader and writer (own module, `m31labs.dev/greenware/gltf`) |
| `language/`, `lang/` | The tree-sitter grammar, parser and formatter |
| `kernel/` | The geometry kernel |
| `cmd/greenware` | The command-line tool |
| `conformance/` | An independent checker, in its own module |

Greenware depends on no game engine. The core never imports GoSX or Kiln; `internal/depcheck` enforces that.

## Licence

MIT, copyright M31 Labs. Authored assets declare their own licences.
