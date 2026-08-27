// Package recipes carries the built-in image recipes as data. Adding an image
// to terrarium is adding a .yaml file here - no code changes.
//
// The files live at the repo root rather than beside the loader because
// go:embed cannot reach outside its own package directory, and a contributor
// looking for "the list of images" should find it without reading Go.
package recipes

import "embed"

//go:embed *.yaml
var FS embed.FS
