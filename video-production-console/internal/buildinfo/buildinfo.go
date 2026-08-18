package buildinfo

import "fmt"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func String() string {
	return fmt.Sprintf("video-production-console %s (commit %s, built %s)", Version, Commit, BuildTime)
}
