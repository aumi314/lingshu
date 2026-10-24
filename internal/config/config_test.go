package config

import "testing"

func TestExplicitPaymentPublicURL(t *testing.T) {
	t.Setenv("LINGSHU_DATABASE_URL", "postgres://local/test")
	for _, u := range []string{"", "https://gateway.example/", "http://localhost:8300"} {
		t.Setenv("LINGSHU_PUBLIC_URL", u)
		if _, err := Load(); err != nil {
			t.Fatal(u, err)
		}
	}
	for _, u := range []string{"http://public.example", "https://secret@example.com", "https://example.com?host=evil"} {
		t.Setenv("LINGSHU_PUBLIC_URL", u)
		if _, err := Load(); err == nil {
			t.Fatal("invalid public URL accepted", u)
		}
	}
}
