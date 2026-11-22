package pbrcolor

import "testing"

func TestSRGBRoundTripsEncodedBytes(t *testing.T) {
	for i := range 256 {
		channel := uint8(i)
		if roundTrip := SRGB(float64(float32(Linear(channel)))); roundTrip != channel {
			t.Fatalf("sRGB byte %d came back as %d", channel, roundTrip)
		}
	}
}
