package aishorts

import (
	"strings"
	"testing"
)

func TestVisualIntentPeopleAndEmptyScenesStayFaithful(t *testing.T) {
	for _, scene := range []string{
		"年轻上班族下班后与父母交流，一起查看手机，普通客厅自然光",
		"夫妻共同核对家庭收支，一人查看手机、一人记录，侧面中景",
		"劳动者回家后核对家庭支出，桌边中景",
		"无人空镜，银行外立面与街道，晨光",
	} {
		for _, style := range []string{"documentary", "warm_realism", "poster", "collage"} {
			prompt := explainerImagePrompt(Shot{StyleKey: style, Scene: scene})
			if !strings.Contains(prompt, scene) {
				t.Fatal("scene changed")
			}
			if strings.Contains(prompt, "不要出现任何人物") || strings.Contains(prompt, "不额外添加人物") || strings.Contains(prompt, "中国中老年人") {
				t.Fatalf("injected conflicting subject/age restriction: %s", prompt)
			}
		}
	}
}
