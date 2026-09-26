package faketelegram_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

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
	calls := s.Calls()
	if len(calls) != 3 {
		t.Fatalf("recorded %d calls, want 3", len(calls))
	}
	for i, w := range want {
		if calls[i].Status != w.status {
			t.Errorf("call %d recorded with status %d, want %d", i, calls[i].Status, w.status)
		}
	}
	if posts := s.Posts("1"); len(posts) != 1 || posts[0].Status != http.StatusOK {
		t.Errorf("Posts = %+v, want only the successful call", posts)
	}
}

func TestDisconnectClosesTheConnectionWithoutAnswer(t *testing.T) {
	s := faketelegram.New(t, token)
	s.Enqueue("sendMessage", faketelegram.Disconnect())

	resp, err := http.Post(s.URL()+"/bot"+token+"/sendMessage", "application/json", strings.NewReader(`{"chat_id":"1","text":"x"}`))
	if err == nil {
		resp.Body.Close()
		t.Fatalf("got HTTP %d, want no answer", resp.StatusCode)
	}
	if calls := s.Calls(); len(calls) != 1 || calls[0].Status != 0 || calls[0].ChatID != "1" {
		t.Errorf("recorded %+v, want the call without a status", calls)
	}
}

func TestHeldReplyIsAnsweredOnRelease(t *testing.T) {
	s := faketelegram.New(t, token)
	release := make(chan struct{})
	s.Enqueue("sendMessage", faketelegram.JSONError(400, "Bad Request: chat not found").After(release))

	answered := make(chan int, 1)
	go func() {
		resp, err := http.Post(s.URL()+"/bot"+token+"/sendMessage", "application/json", strings.NewReader(`{"chat_id":"1","text":"x"}`))
		if err != nil {
			answered <- 0
			return
		}
		resp.Body.Close()
		answered <- resp.StatusCode
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Calls()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the call was not recorded while held")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case status := <-answered:
		t.Fatalf("answered %d before the release", status)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	if status := <-answered; status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if calls := s.Calls(); len(calls) != 1 || calls[0].Status != http.StatusBadRequest || calls[0].ChatID != "1" {
		t.Errorf("recorded %+v, want the rejected call", calls)
	}
}

func TestQueuedSuccessAnswersAsUsual(t *testing.T) {
	s := faketelegram.New(t, token)
	s.Enqueue("sendMessage", faketelegram.Success())
	s.Enqueue("sendMessage", faketelegram.BareStatus(http.StatusBadGateway))

	for _, want := range []int{http.StatusOK, http.StatusBadGateway} {
		resp, err := http.Post(s.URL()+"/bot"+token+"/sendMessage", "application/json", strings.NewReader(`{"chat_id":"1","text":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("status = %d, want %d", resp.StatusCode, want)
		}
	}
	if posts := s.Posts("1"); len(posts) != 1 {
		t.Errorf("Posts = %+v, want the successful call", posts)
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
