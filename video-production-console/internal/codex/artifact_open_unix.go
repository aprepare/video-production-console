//go:build !windows

package codex

import (
	"fmt"
	"os"
	"path/filepath"
)

func openVerifiedArtifact(path, root string) (*os.File, string, os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, "", nil, err
	}
	link, err := os.Readlink("/proc/self/fd/" + fmt.Sprint(f.Fd()))
	if err != nil {
		_ = f.Close()
		return nil, "", nil, err
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		_ = f.Close()
		return nil, "", nil, err
	}
	return f, resolved, info, nil
}
