package spokenlines

import "strings"

// anchorRunes 用原文开头这么多个内容字去口播结果里定位正文起点；太短会误中说明句里的词。
const anchorRunes = 8

// StripPreamble 去掉模型拼在口播稿开头、原文之前的说明文字
// （如「正在按规则将连续文案切分为口播稿，不另写新内容。」）。
// 口播稿只切分原文，正文必然从原文开头写起：在结果里找到原文开头那几个字，删掉它前面的内容。
// 找不到（模型改了开头）就原样返回，交给后面的配音校验报错，不冒险删正文。
func StripPreamble(reply, source string) string {
	anchor := []rune(comparable(source))
	if len(anchor) < anchorRunes {
		return reply
	}
	key := anchor[:anchorRunes]
	// offsets[i] 是 kept[i] 在 reply 里的字节位置。
	var kept []rune
	var offsets []int
	for offset, r := range reply {
		if isComparableRune(r) {
			kept = append(kept, r)
			offsets = append(offsets, offset)
		}
	}
	for i := 0; i+len(key) <= len(kept); i++ {
		if string(kept[i:i+len(key)]) != string(key) {
			continue
		}
		if i == 0 {
			return reply
		}
		// 从正文第一个字往前回退到同一行里紧挨着它的数字（如「2026年」的 2026），别把它们切掉。
		cut := offsets[i]
		lineStart := strings.LastIndex(reply[:cut], "\n") + 1
		prefix := []rune(reply[lineStart:cut])
		back := len(prefix)
		for back > 0 && isLeadingDigitRune(prefix[back-1]) {
			back--
		}
		cut = lineStart + len(string(prefix[:back]))
		return strings.TrimLeft(reply[cut:], " \t\r\n")
	}
	return reply
}

func isLeadingDigitRune(r rune) bool {
	return (r >= '0' && r <= '9') || r == '.' || strings.ContainsRune("零〇一二三四五六七八九十百千万亿两点", r)
}

func isComparableRune(r rune) bool {
	return isContentRune(r) && !strings.ContainsRune("零〇一二三四五六七八九十百千万亿两点", r)
}

// comparable 只留汉字和字母，去掉数字、汉字数字、标点和换行，
// 让「二零二六年」与「2026年」、切行后的口播与连续原文能直接比对。
func comparable(text string) string {
	var b strings.Builder
	for _, r := range text {
		if isComparableRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
