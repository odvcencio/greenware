module m31labs.dev/facet

go 1.25

require (
	m31labs.dev/det v0.0.0
	m31labs.dev/gltf v0.0.0
)

// Staged extraction: the neutral leaves live in this repository until they are
// released on their own (decisions/0010 in the Facet space).
replace (
	m31labs.dev/det => ./det
	m31labs.dev/gltf => ./gltf
)
