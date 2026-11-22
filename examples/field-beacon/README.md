# Procedural field beacon

A game can store a seed and a few parameters, then generate the same asset on demand. This example assembles a beacon from seven reusable meshes. Its recipe controls the tier count, panels per tier, paint and rotation. Each panel carries a named payload socket that follows the panel's transform. A top socket marks the position of a status effect.

From the Greenware checkout:

```sh
go run ./examples/field-beacon -seed 17 -tiers 3 -panels 5 -out field-beacon.glb
go run ./cmd/greenware sockets field-beacon.glb
```

Go applications can import ``m31labs.dev/greenware/examples/field-beacon/beacon`` and call ``beacon.Build(beacon.Recipe{Seed: 17, Tiers: 3, Panels: 5})``. It returns a self-contained GLB in memory. Pass the bytes to your engine or write them to a file. A renderer can use the GLB directly, and application code can use ``socket.ReadGLB`` to place lights, effects or gameplay attachments.

A production game can persist the recipe and generator revision, cache the GLB by that key, and keep effects and interaction in its game logic. Rebuilding at a different generator revision can change the asset.
