// Package web carries the operator interface's built frontend.
//
// The embed directive lives here rather than in internal/admin for a reason
// that is a Go rule and not a preference: //go:embed cannot reach outside the
// directory containing the file that declares it, and no path with `..` in it
// is allowed. internal/admin would have to name ../../web/dist, which does not
// compile. So the package that owns the directory owns the directive, and
// internal/admin imports it.
//
// `all:` rather than a bare `dist` so that the tree is embedded whole — the
// prefix is what includes files whose names begin with a dot or an underscore,
// and Vite writes neither today. It is here so that a future asset that does
// start with one is embedded rather than silently missing from the binary.
//
// dist/ is a build artefact and is not committed, with one exception: the
// .gitkeep below. Without a file in the directory the pattern matches nothing
// and `go build` fails outright — which would mean a backend-only checkout
// could not compile until somebody had run npm, and the failure would point at
// a missing directory rather than at the missing build step. With the
// placeholder the embed succeeds and the admin serves a page explaining what to
// run. See internal/admin/spa.go.
package web

import "embed"

// Dist is the built frontend: index.html, assets/, and whatever else Vite
// emitted. Served by internal/admin/spa.go.
//
//go:embed all:dist
var Dist embed.FS
