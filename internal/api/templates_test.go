package api

import "testing"

func TestOpenAIPlatformTemplate(t *testing.T) {
	for _, template := range channelTemplates {
		if template.ID != "openai-platform" {
			continue
		}
		if template.Name != "OpenAI Platform" || template.BaseURLChat != "https://api.openai.com/v1" || template.BaseURLResponses != "https://api.openai.com/v1" || template.BaseURLMessages != "" || !template.HasModelList {
			t.Fatalf("unexpected OpenAI Platform template: %+v", template)
		}
		return
	}
	t.Fatal("OpenAI Platform template is missing")
}

func TestStandardTemplate(t *testing.T) {
	for _, template := range channelTemplates {
		if template.ID != "standard" {
			continue
		}
		if template.Name != "标准模板" || template.BaseURLChat != "" || template.BaseURLMessages != "" || template.BaseURLResponses != "" || !template.HasModelList {
			t.Fatalf("unexpected standard template: %+v", template)
		}
		if channelTemplates[0].ID != "standard" {
			t.Fatal("standard template must appear first")
		}
		return
	}
	t.Fatal("standard template is missing")
}

func TestOpenCodeZenTemplate(t *testing.T) {
	for _, template := range channelTemplates {
		if template.ID != "opencode-zen" {
			continue
		}
		if template.Name != "OpenCode Zen" || template.BaseURLChat != "https://opencode.ai/inference/openai/v1" {
			t.Fatalf("unexpected OpenCode Zen template: %+v", template)
		}
		// 只启用 Chat 协议，其余协议地址留空
		if template.BaseURLMessages != "" || template.BaseURLResponses != "" {
			t.Fatalf("OpenCode Zen template must be chat only: %+v", template)
		}
		// 上游有模型列表接口，模型直接拉取，不预置
		if !template.HasModelList || len(template.PresetModels) != 0 {
			t.Fatalf("unexpected OpenCode Zen model list config: %+v", template)
		}
		return
	}
	t.Fatal("OpenCode Zen template is missing")
}
