// Package rig is the rigging kernel: bind poses and inverse binds, skin
// weights built by ordered steps, morph targets, clip keys and linear blend
// skinning.
//
// Every distance is in metres and every angle in radians. All arithmetic is
// float64 and goes through package det for sin, cos, acos and sqrt, and every
// multiply that feeds an add or a subtract is wrapped in float64(...), so the
// same inputs give the same bits on every architecture
// (TestNoUnwrappedMultiply checks the wrapping).
//
// Frames. A joint's bind position and bind orientation are given in the model
// frame, like the geometry. The local rest of a joint is the bind of its
// parent inverted and then composed with its own: L = B_p^-1 B_j. The inverse
// bind of a joint is the inverse of its world rest, in model space, so
// skinning an unmorphed vertex in the original bind pose is the identity.
package rig
