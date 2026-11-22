# Goal-post wind flags

This GoSX Scene3D example compiles the [Greenware goal post](../goalpost/goalpost.gw) and mounts a flag on each upright. Try both goal-post variants, all three levels of detail, and the two wind presets. The Flags camera frames the tips. The Full post camera shows the entire prop.

From this directory, with Go 1.26 on your PATH:

```sh
export CGO_ENABLED=0 GOWORK=off
go mod download m31labs.dev/gosx
go run . -runtime "$(go list -m -f '{{.Dir}}' m31labs.dev/gosx)"
```

Open the loopback address printed by the server. Drag to orbit and scroll to zoom. The download link gives you the selected rigid GLB.

Every rigid GLB carries `socket.ribbon_l` and `socket.ribbon_r`. The app reads both from the default scene and places an independent flag and staff at each world-space socket. The example expects translation-only socket paths and returns an error if either socket is missing, duplicated, or on a rotated or scaled path.

GoSX's `scene.Wind` supplies the direction, gust and flutter rate. The example samples the field at each socket, writes a three-second glTF morph animation, and plays it in Scene3D. The attached edge stays fixed as the free end moves and droops. Applying a wind preset selects its precomputed loops. The flags and staffs are scene assets, so the qualified goal-post GLBs can be reused in scenes with different attachments.

Run `CGO_ENABLED=0 GOWORK=off go test ./...` here to verify the socket bindings, unchanged rigid bytes, and the cloth's fixed edge and loop seam.
