package aishorts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Store 把每条短片存成 <dataRoot>/ai_shorts/<id>.json，素材放在同名目录下。
// 文件存储足够：一条短片就是一个几十 KB 的文档，改动全在单条内部。
type Store struct {
	DataRoot string
	mu       sync.Mutex
}

var ErrNotFound = errors.New("ai short not found")

func (s *Store) dir() string { return filepath.Join(s.DataRoot, "ai_shorts") }

// AssetDir 是这条短片的素材目录（角色图、分镜图、视频、配音、草稿工作区）。
func (s *Store) AssetDir(id string) string { return filepath.Join(s.dir(), id) }

func (s *Store) path(id string) string { return filepath.Join(s.dir(), id+".json") }

func (s *Store) Save(short *Short) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(short, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(short.ID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(short.ID))
}

func (s *Store) Get(id string) (*Short, error) {
	if strings.ContainsAny(id, `/\`) || id == "" {
		return nil, ErrNotFound
	}
	raw, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var short Short
	if err := json.Unmarshal(raw, &short); err != nil {
		return nil, err
	}
	normalizeLegacy(&short)
	return &short, nil
}

// normalizeLegacy 把早期记录拉到当前约定。只改内存对象，下次 Save 时才落盘。
func normalizeLegacy(short *Short) {
	if short.IsExplainer() {
		if short.VisualSettings == nil {
			short.VisualSettings = legacyVisualSettings()
		}
		// 单画风沿用旧规则，混合策略保留每镜选择，分段画风和手动钉住的镜按各自规则；已生成视频保留。
		short.Style = StyleByKey(strings.TrimSpace(short.Style)).Key
		if len(short.Shots) > 0 && short.Shots[0].Role == "" {
			// 老记录没打过角色：补上，但不改它们已经生成好的画风（这一步只在内存里，用户改设置时才会真正重算）。
			assignShotRoles(short.Shots, segmentStylesOf(short).openingShots())
		}
		for i := range short.Shots {
			short.Shots[i].StyleKey = resolvedShotStyleFor(short, short.Shots[i])
			short.Shots[i].Hero = short.NeedsVideo(short.Shots[i])
		}
	} else if s := strings.TrimSpace(short.Style); s == "" || s == legacyStylePrompt {
		short.Style = DefaultStylePrompt
	}
	for i := range short.Shots {
		if strings.TrimSpace(short.Shots[i].Speaker) == "" {
			short.Shots[i].Speaker = SpeakerNarrator
		}
	}
	// 预览提示词每次读取都按当前规则重算；图真正用过的提示词另存在 ImagePromptUsed 里。
	fillAllPrompts(short, false)
	fillCoverPrompt(short)
}

// List 返回全部短片，最新在前。
func (s *Store) List() ([]*Short, error) {
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		if os.IsNotExist(err) {
			return []*Short{}, nil
		}
		return nil, err
	}
	out := make([]*Short, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		short, err := s.Get(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, short)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (s *Store) Delete(id string) error {
	if _, err := s.Get(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.RemoveAll(s.AssetDir(id))
	return os.Remove(s.path(id))
}

// Update 读-改-写一条短片，改动函数里拿到的是最新副本；并发的镜头任务都走这里，
// 不会互相覆盖。
func (s *Store) Update(id string, mutate func(*Short) error) (*Short, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var short Short
	if err := json.Unmarshal(raw, &short); err != nil {
		return nil, err
	}
	normalizeLegacy(&short)
	if err := mutate(&short); err != nil {
		return nil, err
	}
	short.UpdatedAt = now()
	out, err := json.MarshalIndent(&short, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp := s.path(id) + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, s.path(id)); err != nil {
		return nil, err
	}
	return &short, nil
}
