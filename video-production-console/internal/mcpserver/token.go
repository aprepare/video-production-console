package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// TokenStore 管理 MCP 令牌：存在数据目录 mcp-token.txt，首次读取时生成，可重置。
type TokenStore struct {
	path string
	mu   sync.Mutex
}

func NewTokenStore(dataRoot string) *TokenStore {
	return &TokenStore{path: filepath.Join(dataRoot, "mcp-token.txt")}
}

// Current 返回当前令牌，没有就生成一个。
func (s *TokenStore) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if raw, err := os.ReadFile(s.path); err == nil {
		if token := strings.TrimSpace(string(raw)); token != "" {
			return token
		}
	}
	token, err := s.writeNew()
	if err != nil {
		return ""
	}
	return token
}

// Rotate 作废旧令牌并生成新令牌。
func (s *TokenStore) Rotate() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeNew()
}

func (s *TokenStore) writeNew() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := "jy_" + hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return "", err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return "", errors.Join(err, errors.New("save MCP token"))
	}
	return token, nil
}
