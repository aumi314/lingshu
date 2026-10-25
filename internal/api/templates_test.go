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
