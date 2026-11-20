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
