package athleteparity

import "strings"

// Part is one row of the game's ledger: its name, its ceilings in triangles
// and indexed vertices at the replay (R), broadcast (B) and far (F) levels,
// and the shell and gear names that belong to it.
//
// The table is copied from the game's cmd/bakeathletes/ledger.go (`parts`),
// whose ceilings come from deep consult 1, section B (reports/strategy in the
// Hyphae space hypha://m31labs/solmara-saturday). It is football data: it
// lives here and in examples, not in a generic package.
type Part struct {
	Name          string
	RT, RV        int
	BT, BV        int
	FT, FV        int
	Transparent   bool
	Shells, Gears []string
}

// Parts is the ledger's budget table, in the game's row order.
var Parts = []Part{
	{Name: "jersey torso, collar, belt", RT: 2400, RV: 1450, BT: 1200, BV: 740, FT: 420, FV: 280, Shells: []string{"torso", "collar", "belt"}},
	{Name: "caps and cuffs", RT: 1800, RV: 1100, BT: 800, BV: 500, FT: 280, FV: 190, Shells: []string{"shoulderpad", "sleevehem"}},
	{Name: "arms, tape, play card", RT: 2200, RV: 1350, BT: 900, BV: 580, FT: 320, FV: 220, Shells: []string{"upperarm", "forearm"}, Gears: []string{"tape-", "playcard-"}},
	{Name: "hands or gloves", RT: 1800, RV: 1100, BT: 360, BV: 250, FT: 120, FV: 90, Shells: []string{"hand", "palm", "thumb", "finger"}, Gears: []string{"glove-"}},
	{Name: "hips, thighs, knee plates", RT: 3200, RV: 1900, BT: 1500, BV: 940, FT: 560, FV: 375, Shells: []string{"hips", "thigh", "thighpad"}},
	{Name: "lower legs, socks, bands", RT: 1200, RV: 750, BT: 520, BV: 350, FT: 200, FV: 140, Shells: []string{"shin", "kneeband", "kneepad", "sock"}},
	{Name: "face and neck", RT: 1800, RV: 1150, BT: 420, BV: 290, FT: 120, FV: 90, Shells: []string{"neck", "face", "nose", "brow", "chin", "cheek", "eye", "pupil", "mouthguard", "ear"}, Gears: []string{"eyeblack-"}},
	{Name: "shell, pads, strap, crest", RT: 2800, RV: 1700, BT: 970, BV: 620, FT: 380, FV: 250, Shells: []string{"helmet", "helmetrim", "earcup", "earhole", "chinstrap", "maskclip", "chinpad", "bumper", "cheekpad"}, Gears: []string{"crest"}},
	{Name: "cage and clips", RT: 1400, RV: 850, BT: 350, BV: 250, FT: 64, FV: 52, Gears: []string{"mask-"}},
	{Name: "boots and collars", RT: 1400, RV: 890, BT: 520, BV: 350, FT: 200, FV: 140, Shells: []string{"cleat", "sole", "lace", "stud"}, Gears: []string{"cleat-"}},
	{Name: "other opaque gear", RT: 400, RV: 260, BT: 120, BV: 90, FT: 24, FV: 20, Gears: []string{"towel-"}},
	{Name: "visor (transparent)", RT: 64, RV: 51, BT: 24, BV: 21, Transparent: true, Gears: []string{"visor"}},
}

// Unassigned is the ledger row for shells in no part.
const Unassigned = "(unassigned)"

// measured lists the shells the silhouette bands select; every one is also in
// a ledger part's shell list.
var measured = []string{"torso", "shoulderpad", "helmet", "hips", "thigh"}

// PartOfShell names the ledger part a shell belongs to, or "" when the table
// does not know the shell.
func PartOfShell(shell string) string {
	for _, p := range Parts {
		for _, s := range p.Shells {
			if shell == s {
				return p.Name
			}
		}
	}
	return ""
}

// sideSuffixes are the endings a Greenware shape takes for the left and right
// of a mirrored pair; the game's shells carry no side.
var sideSuffixes = []string{"_l", "_r", "-l", "-r", ".l", ".r", "_left", "_right", "-left", "-right"}

// ShellName maps the leaf name of a shape to the name the game's tables use.
// The rules, in order, with the first that gives a name the table knows:
//
//  1. an alias given by the caller (from=to);
//  2. the name as written;
//  3. the name without a side ending (_l, _r, -l, -r, .l, .r, _left, _right);
//  4. 2 or 3 with "_" and "-" removed ("sleeve_hem_l" gives "sleevehem").
//
// When none of them gives a known name the leaf name is returned unchanged.
func ShellName(leaf string, aliases map[string]string) string {
	if a, ok := aliases[leaf]; ok {
		return a
	}
	strip := leaf
	for _, s := range sideSuffixes {
		if strings.HasSuffix(leaf, s) {
			strip = strings.TrimSuffix(leaf, s)
			break
		}
	}
	squash := func(s string) string { return strings.NewReplacer("_", "", "-", "").Replace(s) }
	for _, c := range []string{leaf, strip, squash(leaf), squash(strip)} {
		if PartOfShell(c) != "" {
			return c
		}
	}
	return leaf
}
