package compiler

// Census is the measured figures of one level file of one output: what
// conformance 7.2 asks a build to record for every output, row and level
// before any ceiling is chosen. Every number is read from the encoded file the
// build wrote, not estimated.
type Census struct {
	Output string `json:"output"`
	Level  int    `json:"level"`
	File   string `json:"file"`
	// Images is the number of distinct embedded images; ImageDecodedBytes is
	// their decoded RGBA size (the `texture_bytes` figure) and
	// ImageEncodedBytes the length of their PNG files.
	Images            int `json:"images"`
	ImageDecodedBytes int `json:"image_decoded_bytes"`
	ImageEncodedBytes int `json:"image_encoded_bytes"`
	// FileBytes is the length of the encoded GLB.
	FileBytes int `json:"file_bytes"`
	Vertices  int `json:"vertices"`
	// Triangles is drawn in the default look, TrianglesWorst in the worst
	// reachable look and TrianglesStored over every branch of the file.
	Triangles       int `json:"triangles"`
	TrianglesWorst  int `json:"triangles_worst"`
	TrianglesStored int `json:"triangles_stored"`
	Draws           int `json:"draws"`
	DrawsWorst      int `json:"draws_worst"`
	Materials       int `json:"materials"`
	Joints          int `json:"joints"`
	Influences      int `json:"influences"`
	MorphTargets    int `json:"morph_targets"`
	Animations      int `json:"animations"`
	AnimationKeys   int `json:"animation_keys"`
}

// CensusRow is a census row with the census of its leaf shapes. The build
// record and the build report hold rows of this type; Census stays a plain
// comparable value for readers that only need the figures.
type CensusRow struct {
	Census
	// Parts is the census of the leaf shapes, in the order their geometry
	// is written to the file. See CensusPart.
	Parts []CensusPart `json:"parts"`
}

// CensusPart is the share of one leaf shape in a level file.
//
// Name is the shape's name as written in the source, led by the names of the
// parts that hold it, joined by "/" (for example "body/thigh_l"); a shape
// without a name is called by its kind. Triangles is the number of triangles
// the shape wrote to the file. Vertices is the number of indexed vertices it
// wrote: the compiler welds vertices that are equal in position, normal, UV
// (and in the skin and morph data of a rigged asset) only inside one shape,
// never across shapes, so every vertex has exactly one owner and the
// vertices of all parts add up to the file's indexed vertices. Material is the
// name of the material the shape draws with ("" when it has none).
//
// The parts are listed in file order: primitive by primitive (a primitive is
// one material of one node, in the order the materials first appear), and in
// each primitive in the order the shapes were written. A reader can therefore
// cut a primitive's vertex and index ranges into parts by adding up the
// counts. The triangles of all parts add up to TrianglesStored, which is the
// file's triangle count over every branch.
//
// A mount baked into the file (`mounts = bake`) adds one row named
// "mount/<mount name>" with the triangles and vertices its gear contributes,
// and no material, because the gear brings its own; those rows come last.
type CensusPart struct {
	Name      string `json:"name"`
	Triangles int    `json:"triangles"`
	Vertices  int    `json:"vertices"`
	Material  string `json:"material"`
}

// Census returns the figures of the file.
func (f *File) Census(output string) Census {
	c := Census{
		Output: output, Level: f.Level, File: f.Name,
		Images: len(f.Images), ImageDecodedBytes: f.TextureBytes,
		FileBytes: f.Size, Vertices: f.Vertices,
		Triangles: f.Triangles, TrianglesWorst: f.TrianglesWorst, TrianglesStored: f.TrianglesStored,
		Draws: f.Draws, DrawsWorst: f.DrawsWorst, Materials: f.Materials,
		Joints: f.Joints, Influences: f.Influences, MorphTargets: f.Morphs,
		Animations: f.Animations, AnimationKeys: f.AnimKeys,
	}
	for _, im := range f.Images {
		c.ImageEncodedBytes += im.Bytes
	}
	return c
}

// Census returns one row for every level of every output, in the order the
// build record lists the files.
func (p *Product) Census() []Census {
	rows := []Census{}
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			rows = append(rows, f.Census(o.Name))
		}
	}
	return rows
}

// CensusRows returns the rows of Census with each file's leaf shapes.
func (p *Product) CensusRows() []CensusRow {
	rows := []CensusRow{}
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			rows = append(rows, CensusRow{Census: f.Census(o.Name), Parts: append([]CensusPart{}, f.Parts...)})
		}
	}
	return rows
}

// Audits returns the audit reports of every output and level that has one.
func (p *Product) Audits() []*AuditReport {
	var out []*AuditReport
	for _, o := range p.Outputs {
		for _, f := range o.Files {
			if f.Audit != nil {
				out = append(out, f.Audit)
			}
		}
	}
	return out
}
