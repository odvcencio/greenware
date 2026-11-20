package mesh

import (
	"math"
	"testing"
)

func TestEulerQuarterTurnsAreExact(t *testing.T) {
	deg := math.Pi / 180
	cases := []struct {
		name string
		got  Mat3
		want Mat3
	}{
		{"x 90", EulerXYZ(90*deg, 0, 0), Mat3{{1, 0, 0}, {0, 0, -1}, {0, 1, 0}}},
		{"y 90", EulerXYZ(0, 90*deg, 0), Mat3{{0, 0, 1}, {0, 1, 0}, {-1, 0, 0}}},
		{"z 90", EulerXYZ(0, 0, 90*deg), Mat3{{0, -1, 0}, {1, 0, 0}, {0, 0, 1}}},
		{"y 180", EulerXYZ(0, 180*deg, 0), Mat3{{-1, 0, 0}, {0, 1, 0}, {0, 0, -1}}},
		{"y -90", EulerXYZ(0, -90*deg, 0), Mat3{{0, 0, -1}, {0, 1, 0}, {1, 0, 0}}},
		{"y 450", EulerXYZ(0, 450*deg, 0), Mat3{{0, 0, 1}, {0, 1, 0}, {-1, 0, 0}}},
		{"zero", EulerXYZ(0, 0, 0), Identity3},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestEulerOrderAndHandedness(t *testing.T) {
	deg := math.Pi / 180
	// +Z goes toward -Y under a positive turn about X.
	if v := EulerXYZ(90*deg, 0, 0).MulVec(Vec3{0, 0, 1}); v != (Vec3{0, -1, 0}) {
		t.Errorf("x turn of +Z = %v, want (0,-1,0)", v)
	}
	// R = Rx * Ry * Rz: the Z turn acts on the vector first.
	v := EulerXYZ(90*deg, 90*deg, 0).MulVec(Vec3{1, 0, 0})
	// Ry first takes +X to -Z, then Rx takes -Z to +Y.
	if v != (Vec3{0, 1, 0}) {
		t.Errorf("(90,90,0) of +X = %v, want (0,1,0)", v)
	}
	w := EulerXYZ(90*deg, 0, 90*deg).MulVec(Vec3{1, 0, 0})
	// Rz first takes +X to +Y, then Rx takes +Y to +Z.
	if w != (Vec3{0, 0, 1}) {
		t.Errorf("(90,0,90) of +X = %v, want (0,0,1)", w)
	}
}

func TestEulerFortyFiveIsSymmetric(t *testing.T) {
	m := EulerXYZ(0, 45*math.Pi/180, 0)
	if m[0][0] != m[0][2] || m[0][0] != m[2][2] || m[0][0] != -m[2][0] {
		t.Errorf("45 degrees about y is not symmetric: %v", m)
	}
	if d := m.Det(); math.Abs(d-1) > 1e-15 {
		t.Errorf("det %g", d)
	}
}

func TestEulerGeneralAngleIsOrthonormal(t *testing.T) {
	m := EulerXYZ(0.3, -1.1, 2.2)
	p := m.Mul(m.Transpose())
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			want := 0.0
			if i == j {
				want = 1
			}
			if math.Abs(p[i][j]-want) > 1e-15 {
				t.Fatalf("R*R^T[%d][%d] = %g", i, j, p[i][j])
			}
		}
	}
}
