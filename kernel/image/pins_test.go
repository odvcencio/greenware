package image

// Pinned values, first computed on linux/amd64 and checked on darwin/arm64.
// The grain digests are of the preimages (0,0,1,55,2,3,4,lane) for lanes 0
// and 1; grainBits is the binary64 of Grain(55, 2, 3, 4, 1.2).
const (
	grainLane0 = "54433fdb086b0ef7347e1e7186d8bb31fb46b5bafa13b77f478dd612602a172b"
	grainLane1 = "c0ab268e045932bb31c480c38145bcef34f11c14d55e0d89a026f5a60d219f2b"
	grainBits  = 0x3f9e0ed2b46f1b99
)

// pngPins are the SHA-256 of EncodePNG over the images of testImages.
var pngPins = map[string]string{
	"flat":         "93c767eb5aa0353475e45306c0e200ba302a56b4f9399299f70cf0e621b9a2da",
	"gradient":     "b36a79ae31caa24a918356e41816a7d9628119e8dde728e77e16fea306e16f26",
	"noise":        "66e1765d8f59f738ab52c2ab567c055ceaf0cc3496ac8e8fc26c62249887a72d",
	"one":          "b84195a7deb0f986f35512763dfde7b3265b295cf9f9b5d975d7f18a57d36f2e",
	"narrow-noise": "5288799dbc31c1f3ebe7fd4eb56a9385af2ebcfcc048201beabfea08f9b6e1fe",
}
