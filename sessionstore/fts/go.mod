// A NESTED module, deliberately.
//
// This backend needs a SQLite driver, and golm's whole claim is that embedding
// it imposes no dependency tree on the host program. Keeping it in the main
// module would put modernc.org/sqlite into the requirements of every program
// that runs `go get github.com/leelsey/golm`, whether or not it ever wanted
// full-text search. Its own module means the cost is paid only by the
// deployments that ask for it.
module github.com/leelsey/golm/sessionstore/fts

go 1.27

// The golm requirement names the version this module is released alongside. It
// has to be a REAL one: a `replace` in a dependency's go.mod is ignored by
// whoever imports it, so the replace below resolves the requirement for work
// inside this repository and does nothing at all for a consumer. A placeholder
// here is a module nobody outside can resolve. Move both lines together at
// every release.
require (
	github.com/leelsey/golm v0.1.0
	modernc.org/sqlite v1.58.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.47.0 // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/leelsey/golm => ../..
