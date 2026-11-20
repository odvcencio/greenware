package units

import "testing"

func TestInchIsExactlyTwentyFiveAndFourTenthsMillimetres(t *testing.T) {
	in, err := ParseNumber("1in")
	if err != nil {
		t.Fatal(err)
	}
	if in.Dim != Length || in.Num != 0.0254 {
		t.Errorf("1in = %v dim %v, want 0.0254 m", in.Num, in.Dim)
	}
	for _, c := range [][2]string{
		{"1in", "25.4mm"},
		{"12in", "1ft"},
		{"36in", "1yd"},
		{"4in", "0.1016m"},
		{"18.5in", "469.9mm"},
		{"0.5in", "12.7mm"},
	} {
		a, err := ParseNumber(c[0])
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParseNumber(c[1])
		if err != nil {
			t.Fatal(err)
		}
		if a.Num != b.Num || a.Dim != b.Dim {
			t.Errorf("%s = %v and %s = %v differ", c[0], a.Num, c[1], b.Num)
		}
	}
}

func TestInchIsNotAnAssetUnit(t *testing.T) {
	// `in` is the range word, so an asset cannot name it as its length unit.
	for _, u := range LengthUnits {
		if u == "in" {
			t.Error("`in` must not be listed as an asset length unit")
		}
	}
}
