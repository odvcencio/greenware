package athleteparity

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Look is the part of a player's look that shapes the body: the morph
// weights, the joint scales and the model scale. The game writes them with
// examples/athlete/testdata/looks.json from athletelook.PlayerLook.
type Look struct {
	// Name is set on the named wearers only.
	Name         string             `json:"name,omitempty"`
	School       int                `json:"school"`
	Pos          string             `json:"pos"`
	Depth        int                `json:"depth"`
	Number       int                `json:"number,omitempty"`
	ShapeWeights map[string]float64 `json:"shape_weights"`
	JointScales  map[string]float64 `json:"joint_scales"`
	BodyScale    float64            `json:"body_scale"`
}

// LookFile is the file of looks: the two named wearers and every roster
// slot of the silhouette tool.
//
// On disk the wearers are objects and the roster is a table, to keep the file
// small: "roster_columns" names the columns, "joint:X" is JointScales["X"] and
// "weight:X" is ShapeWeights["X"], and each roster row is an array of values
// in that order. LoadLooks expands the rows into Looks.
type LookFile struct {
	GameCommit string `json:"game_commit"`
	Seed       int    `json:"seed"`
	Note       string `json:"note"`
	Wearers    []Look `json:"wearers"`
	Roster     []Look `json:"-"`

	RosterColumns []string            `json:"roster_columns"`
	RosterRows    [][]json.RawMessage `json:"roster"`
}

// LoadLooks reads a looks file.
func LoadLooks(path string) (*LookFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseLooks(b, path)
}

// ParseLooks reads the bytes of a looks file; name is used in errors.
func ParseLooks(b []byte, name string) (*LookFile, error) {
	var f LookFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(f.Wearers) == 0 {
		return nil, fmt.Errorf("%s: no wearers", name)
	}
	for i, row := range f.RosterRows {
		if len(row) != len(f.RosterColumns) {
			return nil, fmt.Errorf("%s: roster row %d has %d values, the table has %d columns", name, i, len(row), len(f.RosterColumns))
		}
		l := Look{ShapeWeights: map[string]float64{}, JointScales: map[string]float64{}}
		for c, col := range f.RosterColumns {
			var err error
			switch {
			case col == "school":
				err = json.Unmarshal(row[c], &l.School)
			case col == "pos":
				err = json.Unmarshal(row[c], &l.Pos)
			case col == "depth":
				err = json.Unmarshal(row[c], &l.Depth)
			case col == "number":
				err = json.Unmarshal(row[c], &l.Number)
			case col == "body_scale":
				err = json.Unmarshal(row[c], &l.BodyScale)
			case strings.HasPrefix(col, "joint:"):
				var v float64
				err = json.Unmarshal(row[c], &v)
				l.JointScales[strings.TrimPrefix(col, "joint:")] = v
			case strings.HasPrefix(col, "weight:"):
				var v float64
				err = json.Unmarshal(row[c], &v)
				l.ShapeWeights[strings.TrimPrefix(col, "weight:")] = v
			default:
				err = fmt.Errorf("unknown column %q", col)
			}
			if err != nil {
				return nil, fmt.Errorf("%s: roster row %d, column %q: %w", name, i, col, err)
			}
		}
		f.Roster = append(f.Roster, l)
	}
	return &f, nil
}

// Group is a roster median row of the silhouette table: the positions it
// pools, as the game's silhouette tool groups them.
type Group struct {
	Name string
	Pos  []string
}

// Groups are the roster medians of the table, in the game's order.
var Groups = []Group{
	{"OL/DL median", []string{"OL", "DL"}},
	{"TE/LB median", []string{"TE", "LB"}},
	{"RB median", []string{"RB"}},
	{"WR/CB median", []string{"WR", "CB"}},
	{"QB median", []string{"QB"}},
}

// Members returns the roster looks of the group's positions.
func (f *LookFile) Members(g Group) []Look {
	var out []Look
	for _, l := range f.Roster {
		for _, p := range g.Pos {
			if l.Pos == p {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// MorphNames lists the morph targets the game folds, in the order the
// silhouette tool documents them.
var MorphNames = []string{"mass", "belly", "pads", "chest-deep", "limb-lean", "waist-seat", "leg-power"}
