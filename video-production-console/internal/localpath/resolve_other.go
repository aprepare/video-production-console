//go:build !windows

package localpath

import "path/filepath"

// Resolve 返回现存路径解析符号链接后的真实位置。
func Resolve(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
