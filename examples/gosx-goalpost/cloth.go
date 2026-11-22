package main

import (
	"math"

	"m31labs.dev/gosx/scene"
	"m31labs.dev/greenware/det"
	"m31labs.dev/greenware/gltf"
	"m31labs.dev/greenware/kernel/mesh"
)

const (
	columns     = 16
	rows        = 3
	clothLength = 1.04
	clothHeight = 0.32
	loopSeconds = 3
	frames      = 36
	flutter     = "flutter"
)

func makeFlag(wind *scene.Wind, socket mesh.Vec3) ([]byte, error) {
	ir := wind.SceneIR()
	forward := mesh.Vec3{ir.DirectionX, 0, ir.DirectionZ}
	across := mesh.Vec3{-ir.DirectionZ, 0, ir.DirectionX}
	up := mesh.Vec3{0, 1, 0}
	cycles := math.Round(ir.Frequency * loopSeconds)
	const vertexCount = (columns + 1) * (rows + 1)
	p := gltf.Primitive{
		Material:  gltf.Int(0),
		Positions: make([]float32, 3*vertexCount),
		Normals:   make([]float32, 3*vertexCount),
		Targets:   make([]gltf.MorphTarget, frames),
	}
	const stride = columns + 1
	for row := uint32(0); row < rows; row++ {
		for column := uint32(0); column < columns; column++ {
			i := row*stride + column
			p.Indices = append(p.Indices, i, i+stride, i+1, i+1, i+stride, i+stride+1)
		}
	}

	const waveNumber = 2 * math.Pi * 1.23
	const spanPhase = 0.68
	puffAt := func(t float64) float64 {
		v := wind.Sample(socket[0], socket[2], t)
		return math.Min(mesh.Dot(mesh.Vec3{v.X, 0, v.Z}, forward)/12, 1)
	}
	firstPuff := puffAt(0)
	for frame := 0; frame < frames; frame++ {
		t := float64(frame) * loopSeconds / frames
		phase := float64(2 * math.Pi * cycles * t / loopSeconds)
		// For a repeating preview, close the gust envelope around the sampled
		// first pose. The flutter itself completes whole cycles over the loop.
		envelope := det.Sin(math.Pi * t / loopSeconds)
		puff := firstPuff + float64((puffAt(t)-firstPuff)*envelope)
		droop := 0.055 + float64(0.21*(1-puff))
		amplitude := 0.045 + float64(0.14*puff)
		extension := 0.78 + float64(0.22*puff)
		lift := 0.03 + float64(0.12*puff)
		delta := gltf.MorphTarget{
			Positions: make([]float32, 3*vertexCount),
			Normals:   make([]float32, 3*vertexCount),
		}
		for row := 0; row <= rows; row++ {
			v := float64(row) / rows
			for column := 0; column <= columns; column++ {
				u := float64(column) / columns
				u2 := float64(u * u)
				x := float64(clothLength * u)
				y := 0.26 - float64(clothHeight*v)
				// Taper the free end and let the wave travel downwind.
				taper := float64(0.10 * u * v)
				wave := phase - float64(waveNumber*u) + float64(spanPhase*v) + socket[0]
				sine, cosine := det.Sin(wave), det.Cos(wave)
				bend := float64(amplitude * u2)
				z := float64(bend * sine)
				base := mesh.Add(mesh.Scale(forward, x), mesh.Scale(up, y+taper))
				liftAtU := float64(lift * u2)
				yOffset := float64(liftAtU*cosine) - float64(droop*u2)
				offset := mesh.Add(mesh.Scale(forward, float64(x*(extension-1))),
					mesh.Add(mesh.Scale(up, yOffset), mesh.Scale(across, z)))
				// The partial derivatives along the cloth give the lit normal.
				waveSlope := float64(bend * cosine)
				dzdu := float64(2*amplitude*u*sine) - float64(waveNumber*waveSlope)
				dzdv := float64(spanPhase * waveSlope)
				du := mesh.Add(mesh.Scale(forward, float64(clothLength*extension)),
					mesh.Add(mesh.Scale(up, float64(0.10*v)-float64(2*droop*u)), mesh.Scale(across, dzdu)))
				dv := mesh.Add(mesh.Scale(up, -clothHeight+float64(0.10*u)), mesh.Scale(across, dzdv))
				normal := mesh.Normalize(mesh.Cross(dv, du))
				i := 3 * (row*stride + column)
				for axis := 0; axis < 3; axis++ {
					p.Positions[i+axis] = float32(base[axis])
					p.Normals[i+axis] = float32(across[axis])
					delta.Positions[i+axis] = float32(offset[axis])
					delta.Normals[i+axis] = float32(normal[axis] - across[axis])
				}
			}
		}
		p.Targets[frame] = delta
	}
	times := make([]float32, frames+1)
	weights := make([]float32, (frames+1)*frames)
	initial := make([]float32, frames)
	initial[0] = 1
	for frame := range times {
		times[frame] = float32(frame) * loopSeconds / frames
		weights[frame*frames+frame%frames] = 1
	}
	return gltf.Encode(&gltf.Document{
		Asset:  gltf.Asset{Generator: "Greenware GoSX goal-post example"},
		Scene:  gltf.Int(0),
		Scenes: []gltf.Scene{{Nodes: []int{0}}},
		Nodes:  []gltf.Node{{Name: "wind flag", Mesh: gltf.Int(0)}},
		Meshes: []gltf.Mesh{{Name: "cloth", Primitives: []gltf.Primitive{p}, Weights: initial}},
		Materials: []gltf.Material{{
			Name: "orange sailcloth", BaseColor: [4]float32{0.94, 0.11, 0.024, 1},
			Roughness: 0.88, DoubleSided: true,
		}},
		Animations: []gltf.Animation{{
			Name:     flutter,
			Channels: []gltf.Channel{{Node: 0, Path: gltf.PathWeights}},
			Samplers: []gltf.AnimSampler{{Input: times, Output: weights, Interpolation: gltf.InterpLinear}},
		}},
	})
}
