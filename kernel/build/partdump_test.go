package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"testing"

	"m31labs.dev/greenware/kernel/mesh"
)

// TestDumpGoalPostParts writes each goal-post part as a list of float64 bit
// patterns (one hex word per line) to $GW_DUMP_DIR/<part>.txt and logs the
// per-part hash. It does nothing without GW_DUMP_DIR for the files, but
// always logs the hashes.
func TestDumpGoalPostParts(t *testing.T) {
	g, err := GoalPost(GoalPostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("GW_DUMP_DIR")
	for name, m := range map[string]*mesh.Mesh{"post": g.Post, "crossbar": g.Crossbar, "upright_l": g.UprightL, "upright_r": g.UprightR} {
		sum := sha256.Sum256(m.CanonicalBytes())
		t.Logf("PART %s %s", name, hex.EncodeToString(sum[:]))
		var words []string
		for _, p := range m.Positions {
			for _, v := range p {
				words = append(words, fmt.Sprintf("%016x", math.Float64bits(v)))
			}
		}
		for _, c := range m.Corners {
			for _, v := range []float64{c.Normal[0], c.Normal[1], c.Normal[2], c.UV[0], c.UV[1]} {
				words = append(words, fmt.Sprintf("%016x", math.Float64bits(v)))
			}
		}
		if dir != "" {
			out := ""
			for _, w := range words {
				out += w + "\n"
			}
			os.WriteFile(dir+"/"+name+".txt", []byte(out), 0o644)
		}
	}
}
