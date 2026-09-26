package faketelegram_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"immich-tg/internal/faketelegram"
)

const token = "42:token"

func TestRecordsMultipartFieldsAndFiles(t *testing.T) {
	s := faketelegram.New(t, token)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("chat_id", "-100123")
	mw.WriteField("supports_streaming", "true")
	part, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="video"; filename="abc.mp4"`},
		"Content-Type":        {"video/mp4"},
	})
	part.Write([]byte("mp4 bytes"))
	mw.Close()

	resp, err := http.Post(s.URL()+"/bot"+token+"/sendVideo", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	calls := s.Calls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	c := calls[0]
	if c.Method != "sendVideo" || c.ChatID != "-100123" || c.Fields["supports_streaming"] != "true" || c.Chunked {
		t.Errorf("call = %+v", c)
	}
	video := c.Files["video"]
	if video.Filename != "abc.mp4" || video.ContentType != "video/mp4" || string(video.Data) != "mp4 bytes" {
		t.Errorf("video part = %+v", video)
	}
}

func TestRecordsJSONFields(t *testing.T) {
	s := faketelegram.New(t, token)

	resp, err := http.Post(s.URL()+"/bot"+token+"/sendMessage", "application/json",
		strings.NewReader(`{"chat_id":-100123,"text":"hi","disable_notification":true}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	c := s.CallsTo("-100123")
	if len(c) != 1 || c[0].Fields["text"] != "hi" || c[0].Fields["disable_notification"] != "true" {
		t.Errorf("calls = %+v", c)
	}
}

func TestQueuedRepliesAreUsedInOrderThenSuccess(t *testing.T) {
	s := faketelegram.New(t, token)
	s.Enqueue("sendMessage", faketelegram.RateLimited(3))
	s.Enqueue("sendMessage", faketelegram.BareStatus(http.StatusRequestEntityTooLarge))

	want := []struct {
		status int
		body   string
	}{
		{http.StatusTooManyRequests, `"retry_after":3`},
		{http.StatusRequestEntityTooLarge, ""},
		{http.StatusOK, `"ok":true`},
	}
	for i, w := range want {
		resp, err := http.Post(s.URL()+"/bot"+token+"/sendMessage", "application/json", strings.NewReader(`{"chat_id":"1","text":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != w.status || !strings.Contains(string(body), w.body) || (w.body == "" && len(body) != 0) {
			t.Errorf("reply %d = %d %q, want %d containing %q", i, resp.StatusCode, body, w.status, w.body)
		}
	}
	if n := len(s.Calls()); n != 3 {
		t.Errorf("recorded %d calls, want 3", n)
	}
}

func TestRejectsWrongToken(t *testing.T) {
	s := faketelegram.New(t, token)

	resp, err := http.Post(s.URL()+"/botwrong/getMe", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if c := s.Calls(); len(c) != 1 || c[0].Method != "getMe" || c[0].Token != "wrong" {
		t.Errorf("calls = %+v, want the rejected getMe", c)
	}
}

func TestSetBotMemberChangesGetChatMember(t *testing.T) {
	s := faketelegram.New(t, token)
	s.SetBotMember("-100123", "member", false)

	member := func(chatID string) string {
		t.Helper()
		resp, err := http.Post(s.URL()+"/bot"+token+"/getChatMember", "application/json",
			strings.NewReader(`{"chat_id":"`+chatID+`","user_id":7000000001}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	if got := member("-100123"); !strings.Contains(got, `"status":"member"`) || strings.Contains(got, "can_post_messages") {
		t.Errorf("changed chat = %s, want a plain member", got)
	}
	if got := member("-100456"); !strings.Contains(got, `"status":"administrator"`) || !strings.Contains(got, `"can_post_messages":true`) {
		t.Errorf("other chat = %s, want an administrator allowed to post", got)
	}
	if calls := s.CallsTo("-100123"); len(calls) != 0 {
		t.Errorf("CallsTo = %+v, want getChatMember left out", calls)
	}
}

func TestRecordsChunkedBody(t *testing.T) {
	s := faketelegram.New(t, token)
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte(`{"chat_id":"-100123","text":"hi"}`))
		pw.Close()
	}()

	resp, err := http.Post(s.URL()+"/bot"+token+"/sendMessage", "application/json", pr)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if c := s.Calls()[0]; !c.Chunked || c.Fields["text"] != "hi" {
		t.Errorf("call = %+v, want a chunked sendMessage", c)
	}
}
