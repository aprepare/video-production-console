package openaicompat

import "testing"

func TestChineseSmallNumbers(t *testing.T) {
	cases := map[string]string{
		"老百姓攒了20年的积蓄":            "老百姓攒了二十年的积蓄",
		"十个人里8个会划走，2个停下来":       "十个人里八个会划走，两个停下来",
		"第6次到了，前面5次你抓住过几回":     "第六次到了，前面五次你抓住过几回",
		"第2次机会，2次机会":              "第二次机会，两次机会",
		"隔了1套房、1辆车":                "隔了一套房、一辆车",
		"3份文件同一天生效，3个死结":        "三份文件同一天生效，三个死结",
		"9个课时，五块钱":                 "九个课时，五块钱",
		"12个月以后，过3年再看":            "十二个月以后，过三年再看",
		"2026年9月1号生效，利率0.95%，存款173万亿": "2026年9月1号生效，利率0.95%，存款173万亿",
		"98年、08年、15年那三轮":           "98年、08年、15年那三轮",
		"100万一套，翻1倍":                "100万一套，翻一倍",
		"存了50块，每天26块":               "存了50块，每天26块",
	}
	for in, want := range cases {
		got, _ := chineseSmallNumbers(in)
		if got != want {
			t.Errorf("chineseSmallNumbers(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}

func TestChineseNumber(t *testing.T) {
	for n, want := range map[int]string{0: "零", 2: "两", 10: "十", 12: "十二", 20: "二十", 26: "二十六", 99: "九十九"} {
		if got := chineseNumber(n, false); got != want {
			t.Errorf("chineseNumber(%d) = %q, want %q", n, got, want)
		}
	}
	if got := chineseNumber(2, true); got != "二" {
		t.Errorf("ordinal 2 = %q, want 二", got)
	}
}
