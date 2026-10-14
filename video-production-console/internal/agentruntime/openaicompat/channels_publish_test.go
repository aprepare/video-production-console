package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const channelsDraftJSON = `{"continuous_script":"存款到期要想清楚，家里的钱要守住。现在就把这门课拍下来。","titles":[],"short_titles":["手里有存款的人别乱动","银行柜台少了一样东西","管好家里的钱才有底气"],"descriptions":["认知#楼市#认知提升#经济趋势","#楼市#家庭财务#经济趋势","养老#认知提升#家庭财务#经济趋势"],"topics":["#楼市","#认知提升","#经济趋势","#家庭财务"],"cover_titles":["存款到期别乱动","家里的钱要守住"],"cta":"现在就把这门课拍下来。"}`

func channelsMode(t *testing.T) flowSpec {
	t.Helper()
	var mode flowSpec
	if err := json.Unmarshal([]byte(`{"style":"finance_v2","finance":true,"publish":"channels_v2","editorial_rules":""}`), &mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

func TestChannelsPublishValidatesItsOwnContract(t *testing.T) {
	mode := channelsMode(t)
	draft, err := parseRemixDraft(channelsDraftJSON)
	if err != nil {
		t.Fatal(err)
	}
	if issues := publishFieldIssuesFor(draft, mode); len(issues) != 0 {
		t.Fatalf("valid channels draft rejected: %v", issues)
	}
	if !mode.FinanceMode() {
		t.Fatal("v2 lost finance behavior")
	}
	if got := unsupportedNumericValuesFor("", "课程5元", "", mode); len(got) != 0 {
		t.Fatalf("finance price whitelist changed: %v", got)
	}
	cases := []struct {
		name, field, want string
		value             []string
	}{
		{"punctuation", "short_titles", "标点", []string{"存款，别乱动", "银行柜台少了什么", "管钱的人要留心"}},
		{"symbol", "short_titles", "符号", []string{"存款￥别乱动", "银行柜台少了什么", "管钱的人要留心"}},
		{"hash", "short_titles", "符号", []string{"存款#别乱动", "银行柜台少了什么", "管钱的人要留心"}},
		{"space", "short_titles", "空白", []string{"存款 别乱动", "银行柜台少了什么", "管钱的人要留心"}},
		{"edge space", "short_titles", "空白", []string{" 存款别乱动", "银行柜台少了什么", "管钱的人要留心"}},
		{"unicode space", "short_titles", "空白", []string{"存款\u00a0别乱动", "银行柜台少了什么", "管钱的人要留心"}},
		{"long title", "short_titles", "16", []string{strings.Repeat("钱", 17), "银行柜台少了什么", "管钱的人要留心"}},
		{"missing short", "short_titles", "3", []string{"存款别乱动", "银行柜台少了什么"}},
		{"extra short", "short_titles", "3", []string{"存款别乱动", "银行柜台少了什么", "管钱的人要留心", ""}},
		{"long description", "descriptions", "descriptions", []string{"这是不能作为简介的长句#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势"}},
		{"description data", "descriptions", "descriptions", []string{"2026#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势"}},
		{"spaced topics", "descriptions", "descriptions", []string{"认知#楼市 #认知提升#经济趋势", "#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势"}},
		{"few hashtags", "descriptions", "descriptions", []string{"#认知提升#经济趋势", "#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势"}},
		{"missing description", "descriptions", "3", []string{"#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势"}},
		{"missing cover", "cover_titles", "cover_titles", nil},
		{"long cover", "cover_titles", "12", []string{strings.Repeat("钱", 13), "家里的钱要守住"}},
		{"punctuated cover", "cover_titles", "cover_titles", []string{"存款别乱动！", "家里的钱要守住"}},
		{"blank cover", "cover_titles", "cover_titles", []string{"", "家里的钱要守住"}},
		{"titles", "titles", "titles", []string{"额外标题"}},
		{"blank titles", "titles", "titles", []string{""}},
		{"topic mismatch", "topics", "topics", []string{"#楼市", "#存款", "#经济趋势"}},
		{"duplicate topics", "topics", "topics", []string{"#楼市", "#认知提升", "#经济趋势", "#家庭财务", "#楼市"}},
		{"course in cover", "cover_titles", "课程推广", []string{"财富觉醒方法论", "家里的钱要守住"}},
		{"course in title", "short_titles", "课程推广", []string{"去橱窗里看一看", "银行柜台少了什么", "管钱的人要留心"}},
		{"course in topics", "topics", "课程推广", []string{"#楼市", "#五块钱", "#经济趋势"}},
		{"course in description", "descriptions", "课程推广", []string{"橱窗#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势", "#楼市#认知提升#经济趋势"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var obj map[string]any
			_ = json.Unmarshal([]byte(channelsDraftJSON), &obj)
			obj[tc.field] = tc.value
			raw, _ := json.Marshal(obj)
			draft, err := parseRemixDraft(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(publishFieldIssuesFor(draft, mode), "；"); !strings.Contains(got, tc.want) {
				t.Fatalf("missing %s issue: %s", tc.want, got)
			}
		})
	}
	draft.ShortTitles[0] = strings.Repeat("钱", 16)
	if issues := publishFieldIssuesFor(draft, mode); len(issues) != 0 {
		t.Fatalf("16-rune boundary rejected: %v", issues)
	}
}

func TestChannelsPublishContextRepairAndDeliveryKeepCoverTitles(t *testing.T) {
	dir := t.TempDir()
	ctx := currentEditorialContextFor(dir, channelsMode(t))
	if err := saveEditorialContext(dir, ctx); err != nil {
		t.Fatal(err)
	}
	draft, _ := parseRemixDraft(channelsDraftJSON)
	if got := publishFieldIssuesFor(draft, loadEditorialContext(dir).mode()); len(got) != 0 {
		t.Fatalf("context dropped channels mode: %v", got)
	}
	broken := `{"continuous_script":"正文原样保留。","titles":[],"short_titles":[],"descriptions":[],"topics":[],"cta":"","extra":"保留扩展"}`
	client := &sequenceClient{responses: []string{channelsDraftJSON}}
	got, note := repairPublishFields(client, "m", "", "sys", "user", broken, dir)
	if len(client.requests) != 1 || !strings.Contains(note, "已定向返工") {
		t.Fatalf("repair requests=%d note=%s", len(client.requests), note)
	}
	prompt := client.requests[0].Messages[3].Content
	for _, field := range []string{"cover_titles", "16", "12", "descriptions", "titles=[]"} {
		if !strings.Contains(prompt, field) {
			t.Fatalf("repair contract missing %s", field)
		}
	}
	if strings.Contains(prompt, "6 到 15") || strings.Contains(prompt, "第 1 个从 #财经") {
		t.Fatalf("legacy publishing rules leaked into channels repair: %s", prompt)
	}
	var obj map[string]json.RawMessage
	_ = json.Unmarshal([]byte(got), &obj)
	if string(obj["continuous_script"]) != `"正文原样保留。"` || string(obj["extra"]) != `"保留扩展"` || string(obj["cover_titles"]) != `["存款到期别乱动","家里的钱要守住"]` {
		t.Fatalf("repair lost fields: %s", got)
	}
	if err := writeRemixDeliverable(dir, "test", "remix.standard", got, nil, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "publishing_package.json"))
	if err != nil {
		t.Fatal(err)
	}
	obj = nil
	_ = json.Unmarshal(raw, &obj)
	var titles []string
	_ = json.Unmarshal(obj["cover_titles"], &titles)
	if !reflect.DeepEqual(titles, []string{"存款到期别乱动", "家里的钱要守住"}) {
		t.Fatalf("delivery lost covers: %s", raw)
	}
	if v, ok := publishingPackageFromDraft(remixDraft{}, "")["cover_titles"]; !ok || !reflect.DeepEqual(v, []string{}) {
		t.Fatalf("empty cover_titles must be []: %#v", v)
	}
}

func TestChannelsReviewerUsesNewContractAndKeepsCoverTitles(t *testing.T) {
	for _, omitCover := range []bool{false, true} {
		t.Run(map[bool]string{false: "revised-cover", true: "preserved-cover"}[omitCover], func(t *testing.T) {
			dir := t.TempDir()
			before, _ := parseRemixDraft(channelsDraftJSON)
			before.Titles = []string{"这个字段应该清空"}
			draftJSON, _ := json.Marshal(before)
			revised, _ := parseRemixDraft(channelsDraftJSON)
			if omitCover {
				revised.CoverTitles = nil
			} else {
				revised.CoverTitles = []string{"先守好家底", "存款不能乱动"}
			}
			reply, _ := json.Marshal(map[string]any{
				"verdict": "fixed", "summary": "按视频号规则整理发布字段。",
				"issues": []map[string]string{{"where": "titles", "problem": "应为空数组", "fix": "清空"}}, "revised": revised,
			})
			client := &reviewerFakeClient{reply: string(reply)}
			out := ReviewRemixDraft(ReviewOptions{Client: client, Model: "m", Source: "原文", DraftJSON: string(draftJSON), OutputDir: dir,
				WorkflowJSON: `{"style":"finance_v2","finance":true,"publish":"channels_v2","editorial_rules":"","nodes":[{"id":"writer","type":"writer"}]}`,
				SystemPrompt: "保留这条已保存的审稿要求。\n" + PublishingContract,
			})
			if out.Record.Verdict != "fixed" || len(client.requests) != 1 {
				t.Fatalf("review=%+v calls=%d", out.Record, len(client.requests))
			}
			system := client.requests[0].Messages[0].Content
			if !strings.Contains(system, ChannelsPublishingContract) || !strings.Contains(system, "保留这条已保存的审稿要求") || !strings.Contains(system, `"cover_titles":[]`) || strings.Contains(system, PublishingContract) || strings.Contains(system, SharedEditorialPolicy) {
				t.Fatalf("review used conflicting rules: %s", system)
			}
			got, err := parseRemixDraft(out.RevisedJSON)
			wantCover := revised.CoverTitles
			if omitCover {
				wantCover = before.CoverTitles
			}
			if err != nil || len(got.Titles) != 0 || !reflect.DeepEqual(got.CoverTitles, wantCover) || got.ContinuousScript != before.ContinuousScript {
				t.Fatalf("revised fields=%+v err=%v", got, err)
			}
		})
	}
}

func TestChannelsContractDoesNotChangeLegacyValidation(t *testing.T) {
	draft := remixDraft{ShortTitles: []string{"家里的钱放哪里", "房贷这笔账算清", "普通家庭别乱动"}, Descriptions: []string{"一户人的存款怎么放。", "房贷要算清。"}, Topics: []string{"#财经", "#存款", "#房贷"}}
	if issues := publishFieldIssuesFor(draft, flowSpec{Style: "finance_course"}); len(issues) != 0 {
		t.Fatalf("legacy finance changed: %v", issues)
	}
	draft.Topics = []string{"#门店", "#海鲜", "#晚餐"}
	if issues := publishFieldIssuesFor(draft, flowSpec{Style: "local_store"}); len(issues) != 0 {
		t.Fatalf("universal mode changed: %v", issues)
	}
}
