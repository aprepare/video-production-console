package cover

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

var libraryMu sync.Mutex

// SaveToLibrary 同名内容复用；不同内容递增版本，绝不覆盖已有封面。
func SaveToLibrary(src, dir, date, account, title string) (string, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	base := strings.Join([]string{safeFilenamePart(date), safeFilenamePart(account), safeFilenamePart(title)}, "_")
	libraryMu.Lock()
	defer libraryMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for version := 1; ; version++ {
		name := base
		if version > 1 {
			name += fmt.Sprintf("_v%d", version)
		}
		path := filepath.Join(dir, name+".png")
		if existing, err := os.ReadFile(path); err == nil {
			if sha256.Sum256(existing) == digest {
				return path, nil
			}
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			version-- // 其他进程刚创建同名文件，重新检查内容。
			continue
		}
		if err != nil {
			return "", err
		}
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(path)
			if writeErr != nil {
				return "", writeErr
			}
			return "", closeErr
		}
		return path, nil
	}
}

func safeFilenamePart(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimRight(strings.TrimSpace(value), ". ")
	if value == "" {
		return "未命名"
	}
	return value
}
