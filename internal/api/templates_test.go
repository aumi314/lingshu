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
