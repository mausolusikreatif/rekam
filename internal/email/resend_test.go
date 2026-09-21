package email

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendConfigured(t *testing.T) {
	cases := []struct {
		name string
		r    Resend
		want bool
	}{
		{"zero value", Resend{}, false},
		{"key only", Resend{APIKey: "re_123"}, false},
		{"from only", Resend{From: "rekam <noreply@rekam.dev>"}, false},
		{"both set", Resend{APIKey: "re_123", From: "rekam <noreply@rekam.dev>"}, true},
	}
	for _, c := range cases {
		if got := c.r.Configured(); got != c.want {
			t.Errorf("%s: Configured() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResendSendPostsExpectedRequest(t *testing.T) {
	var gotMethod, gotAuth, gotContentType string
	var gotBody map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"fake-message-id"}`))
	}))
	defer fake.Close()

	r := Resend{
		APIKey:   "re_test_key",
		From:     "rekam <noreply@rekam.dev>",
		Endpoint: fake.URL,
		Client:   fake.Client(),
	}
	err := r.Send(context.Background(), Message{
		To:      "user@example.com",
		Subject: "Confirm your rekam account",
		Text:    "Confirm here: https://example.com/ui/confirm?token=abc",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer re_test_key" {
		t.Errorf("Authorization = %q, want Bearer re_test_key", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["from"] != "rekam <noreply@rekam.dev>" {
		t.Errorf("from = %v, want the configured sender", gotBody["from"])
	}
	to, ok := gotBody["to"].([]any)
	if !ok || len(to) != 1 || to[0] != "user@example.com" {
		t.Errorf("to = %v, want [user@example.com]", gotBody["to"])
	}
	if gotBody["subject"] != "Confirm your rekam account" {
		t.Errorf("subject = %v", gotBody["subject"])
	}
	if !strings.Contains(gotBody["text"].(string), "token=abc") {
		t.Errorf("text = %v, want it to contain the confirm link", gotBody["text"])
	}
}

func TestResendSendSurfacesAPIErrors(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"name":"validation_error","message":"invalid ` + "`from`" + ` field"}`))
	}))
	defer fake.Close()

	r := Resend{APIKey: "re_test_key", From: "rekam <noreply@rekam.dev>", Endpoint: fake.URL, Client: fake.Client()}
	err := r.Send(context.Background(), Message{To: "user@example.com", Subject: "x", Text: "y"})
	if err == nil {
		t.Fatal("Send: want an error for a non-2xx response, got nil")
	}
	if !strings.Contains(err.Error(), "invalid `from` field") {
		t.Errorf("Send error = %q, want it to surface Resend's error detail", err.Error())
	}
}

func TestResendSendNotConfiguredErrors(t *testing.T) {
	var r Resend
	if err := r.Send(context.Background(), Message{To: "a@b.com", Subject: "x", Text: "y"}); err == nil {
		t.Fatal("Send on an unconfigured Resend: want an error, got nil")
	}
}
