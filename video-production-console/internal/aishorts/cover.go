package aishorts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// coverStyleKey 封面跟项目画风走；混合策略没有自己的画风，用它的主表达纸张拼贴。
func coverStyleKey(short *Short) string {
	if short == nil {
		return StyleByKey("").Key
	}
	if short.Style == financeEditorial {
		return "paper_collage"
	}
	return StyleByKey(short.Style).Key
}

// coverImagePrompt 按大标题出一张封面图：生图时就把标题印上去，不走分镜那条禁字规则。
func coverImagePrompt(headline, styleKey string) string {
	headline = strings.TrimSpace(headline)
	key := styleKey
	if key == financeEditorial {
		key = "paper_collage"
	}
	preset := StyleByKey(key)
	var b strings.Builder
	b.WriteString("主标题「" + headline + "」。把这几个汉字准确印在画面上方，大号黄字（#FFDE00）黑边、清晰可读，不要改字、不要加副标题、不要英文。")
	b.WriteString("画面：一张视频号封面，中国普通人生活里能对上主标题意思的一个具体瞬间，主体大、对比强、构图饱满，中景或近景；人物和关键物件放在中下部，上方给主标题留出干净位置。")
	b.WriteString("构图：9:16 原生竖屏全幅，真正按竖屏设计，前景—中景—远景层次清楚。")
	b.WriteString(preset.Prompt)
	b.WriteString("中文主标题必须直接印入图内并保持准确可读。除主标题外不要再出现任何文字、字母、数字、乱码、水印、二维码、招牌或标语。")
	b.WriteString(styledPeopleRule(preset.Key))
	b.WriteString(explainerSafetyRule)
	b.WriteString("本图是封面，必须保留上方主标题汉字，不要做成无字海报。")
	return b.String()
}

func fillCoverPrompt(short *Short) {
	if short == nil || !short.IsExplainer() {
		return
	}
	headline := strings.TrimSpace(short.Headline)
	if headline == "" && (short.Cover == nil || short.Cover.Path == "") {
		return
	}
	if short.Cover == nil {
		short.Cover = &Cover{}
	}
	if short.Cover.Status == "" && short.Cover.Path == "" {
		short.Cover.Status = ShotPending
	}
	if headline == "" {
		short.Cover.Stale = short.Cover.Path != ""
		return
	}
	key := coverStyleKey(short)
	short.Cover.Prompt = coverImagePrompt(headline, key)
	if short.Cover.Path == "" {
		short.Cover.Stale = false
		return
	}
	short.Cover.Stale = short.Cover.HeadlineUsed != headline || short.Cover.StyleKey != key ||
		(short.Cover.PromptUsed != "" && short.Cover.PromptUsed != short.Cover.Prompt)
}

func (s *Service) tryLockCover(id string) bool {
	_, loaded := s.coverBusy.LoadOrStore(id, true)
	return !loaded
}

func (s *Service) unlockCover(id string) { s.coverBusy.Delete(id) }

func (s *Service) coverLocked(id string) bool {
	_, busy := s.coverBusy.Load(id)
	return busy
}

// recoverCoverIfInterrupted 进程中途退出时封面会停在 running；下次读取时标失败，让人能再点一次。
func (s *Service) recoverCoverIfInterrupted(short *Short) *Short {
	if short == nil || short.Cover == nil || short.Cover.Status != ShotRunning || s.coverLocked(short.ID) {
		return short
	}
	updated, err := s.store.Update(short.ID, func(x *Short) error {
		if x.Cover != nil && x.Cover.Status == ShotRunning && !s.coverLocked(x.ID) {
			x.Cover.Status = ShotFailed
			x.Cover.Error = "封面生成已中断，再点一次生成封面"
		}
		return nil
	})
	if err != nil {
		return short
	}
	return updated
}

// GenerateCover 按当前大标题和项目画风出一张封面；后台跑，不改短片 status，不挡分镜生图。
func (s *Service) GenerateCover(id string) error {
	short, err := s.store.Get(id)
	if err != nil {
		return err
	}
	if !short.IsExplainer() {
		return errors.New("封面图目前只支持解说模式")
	}
	if strings.TrimSpace(short.Headline) == "" {
		return errors.New("先填写顶部大标题，再生成封面")
	}
	if !s.tryLockCover(id) {
		return ErrBusy
	}
	if _, err := s.store.Update(id, func(x *Short) error {
		if x.Cover == nil {
			x.Cover = &Cover{}
		}
		x.Cover.Status = ShotRunning
		x.Cover.Error = ""
		x.Cover.Stale = false
		x.Cover.Prompt = coverImagePrompt(strings.TrimSpace(x.Headline), coverStyleKey(x))
		return nil
	}); err != nil {
		s.unlockCover(id)
		return err
	}
	go s.runCover(id)
	return nil
}

func (s *Service) runCover(id string) {
	defer s.unlockCover(id)
	ctx := context.Background()
	fail := func(msg string) {
		_, _ = s.store.Update(id, func(x *Short) error {
			if x.Cover == nil {
				x.Cover = &Cover{}
			}
			x.Cover.Status = ShotFailed
			x.Cover.Error = msg
			return nil
		})
	}
	short, err := s.store.Get(id)
	if err != nil {
		fail(err.Error())
		return
	}
	rt, gen, _, err := s.clients(ctx)
	if err != nil {
		fail(err.Error())
		return
	}
	headline := strings.TrimSpace(short.Headline)
	key := coverStyleKey(short)
	prompt := coverImagePrompt(headline, key)
	release, limitErr := s.imageGate.acquire(ctx, concurrencyOr(rt.ImageConcurrency, 20))
	if limitErr != nil {
		fail(limitErr.Error())
		return
	}
	defer release()
	jpeg, err := gen.GenerateImageSize(ctx, imageModelFor(short, rt), prompt, nil, "1152x2048")
	if err != nil {
		fail("封面生图失败：" + err.Error())
		return
	}
	dir := s.store.AssetDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err.Error())
		return
	}
	ext := ".jpg"
	switch http.DetectContentType(jpeg) {
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	}
	path := filepath.Join(dir, fmt.Sprintf("cover_%s%s", uuid.NewString(), ext))
	if err := os.WriteFile(path, jpeg, 0o644); err != nil {
		fail(err.Error())
		return
	}
	_, _ = s.store.Update(id, func(x *Short) error {
		if x.Cover == nil {
			x.Cover = &Cover{}
		}
		x.Cover.Path, x.Cover.Status, x.Cover.Error = path, ShotDone, ""
		x.Cover.Prompt, x.Cover.PromptUsed = prompt, prompt
		x.Cover.HeadlineUsed, x.Cover.StyleKey, x.Cover.Stale = headline, key, false
		return nil
	})
}
