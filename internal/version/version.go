// Package version holds the product name and version shared by the
// command line tool, the service and the window.
package version

// Version is replaced at build time:
//
//	go build -ldflags "-X github.com/burnersen/TimeSync/internal/version.Version=2.0.0"
var Version = "dev"

const (
	// Product is the name shown to users.
	Product = "TimeSync"

	// Repository is where the source code and the releases live.
	Repository = "https://github.com/burnersen/TimeSync"
)
