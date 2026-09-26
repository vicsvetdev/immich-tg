// Package faketelegram is an in-test Telegram Bot API server. It records
// every call the service makes and can answer with errors on demand.
package faketelegram

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// BotID and BotUsername identify the fake bot, as returned by getMe.
const (
	BotID       = 7000000001
	BotUsername = "immich_tg_bot"
)

// Call is one Bot API request as the server received it, including requests
// that were rejected.
type Call struct {
	Method string
	// Token is the bot token from the request path.
	Token  string
	ChatID string
	// Fields holds every non-file parameter, whether it was sent as a query
	// parameter, JSON, a urlencoded form or a multipart field. Non-string
	// JSON values are kept in their JSON form, e.g. "true" or "42".
	Fields map[string]string
	// Files holds the multipart file parts, keyed by part name.
	Files map[string]File
	// Chunked is set when the body came with chunked transfer encoding.
	Chunked bool
	// Status is the HTTP status the call was answered with, or 0 if it got no
	// answer: see Disconnect and Stall.
	Status int
}

// File is a multipart file part.
type File struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Reply is a canned HTTP response for one call.
type Reply struct {
	Status int
	Body   string

	disconnect, stall, success bool
	// release, if set, holds the reply back until it is closed.
	release <-chan struct{}
}

// After holds r back until release is closed: the server reads and records
// the call at once, but answers it only then, or never if the client goes
// away first.
func (r Reply) After(release <-chan struct{}) Reply {
	r.release = release
	return r
}

// Success is the usual successful answer, for queueing one held back with
// After.
func Success() Reply {
	return Reply{success: true}
}

// Disconnect is no response at all: the server reads the request, then closes
// the connection, as if the Bot API server went away.
func Disconnect() Reply {
	return Reply{disconnect: true}
}

// Stall is a server that stops reading the request, never answers and keeps
// the connection open until the client gives up.
func Stall() Reply {
	return Reply{stall: true}
}

// JSONError is a Bot API error in its JSON form.
func JSONError(code int, description string) Reply {
	return errorReply(code, description, nil)
}

func errorReply(code int, description string, parameters map[string]any) Reply {
	body := map[string]any{"ok": false, "error_code": code, "description": description}
	if parameters != nil {
		body["parameters"] = parameters
	}
	data, _ := json.Marshal(body)
	return Reply{Status: code, Body: string(data)}
}

// RateLimited is a 429 asking the caller to wait retryAfter seconds.
func RateLimited(retryAfter int) Reply {
	return errorReply(http.StatusTooManyRequests, fmt.Sprintf("Too Many Requests: retry after %d", retryAfter),
		map[string]any{"retry_after": retryAfter})
}

// BareStatus is an error from the Bot API server's HTTP layer: a status code
// with an empty body.
func BareStatus(status int) Reply {
	return Reply{Status: status}
}

// Server is a fake Bot API server for one bot token.
type Server struct {
	token string
	srv   *httptest.Server

	mu        sync.Mutex
	calls     []Call
	replies   map[string][]Reply
	members   map[string]map[string]any
	messageID int

	// closed is closed when the test ends, releasing stalled calls and held
	// replies.
	closed chan struct{}
}

// New starts a fake Bot API server accepting token. It is closed when the
// test ends.
func New(t testing.TB, token string) *Server {
	s := &Server{token: token, replies: map[string][]Reply{}, members: map[string]map[string]any{}, closed: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() { close(s.closed) }) // before Close, which waits for the handlers
	return s
}

// URL is the base URL to use as TELEGRAM_API_URL.
func (s *Server) URL() string { return s.srv.URL }

// Calls returns every call received so far, in order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// CallsTo returns the calls that publish to chatID, in order: every call
// addressed to it except getChatMember, which only reads the chat. Use Calls
// to see those too.
func (s *Server) CallsTo(chatID string) []Call {
	var out []Call
	for _, c := range s.Calls() {
		if c.ChatID == chatID && c.Method != "getChatMember" {
			out = append(out, c)
		}
	}
	return out
}

// SetBotMember changes what getChatMember answers for the bot in chatID. By
// default the bot is an administrator allowed to post messages.
func (s *Server) SetBotMember(chatID, status string, canPostMessages bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	member := map[string]any{"status": status, "user": botUser()}
	if status == "administrator" {
		member["can_post_messages"] = canPostMessages
	}
	s.members[chatID] = member
}

// Posts returns the calls that published to chatID successfully, in order.
func (s *Server) Posts(chatID string) []Call {
	var out []Call
	for _, c := range s.CallsTo(chatID) {
		if c.Status == http.StatusOK {
			out = append(out, c)
		}
	}
	return out
}

// Enqueue makes the next call to method answer with r instead of success.
// Replies queued for the same method are used in order.
func (s *Server) Enqueue(method string, r Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies[method] = append(s.replies[method], r)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	token, method, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/bot"), "/")
	s.mu.Lock()
	if q := s.replies[method]; len(q) > 0 && q[0].stall {
		s.replies[method] = q[1:]
		s.calls = append(s.calls, Call{Method: method, Token: token})
		s.mu.Unlock()
		select {
		case <-r.Context().Done():
		case <-s.closed:
		}
		// The body is unread.
		dropConnection(w)
		return
	}
	s.mu.Unlock()

	call, parseErr := parseCall(r)
	call.Method, call.Token = method, token
	call.Chunked = slices.Contains(r.TransferEncoding, "chunked")

	s.mu.Lock()
	var reply Reply
	switch {
	case !strings.HasPrefix(r.URL.Path, "/bot") || method == "":
		reply = JSONError(http.StatusNotFound, "Not Found")
	case token != s.token:
		reply = JSONError(http.StatusUnauthorized, "Unauthorized")
	case parseErr != nil:
		reply = JSONError(http.StatusBadRequest, "Bad Request: "+parseErr.Error())
	case len(s.replies[method]) > 0:
		reply, s.replies[method] = s.replies[method][0], s.replies[method][1:]
		if reply.success {
			reply = s.success(call).After(reply.release)
		}
	default:
		reply = s.success(call)
	}
	call.Status = reply.Status
	s.calls = append(s.calls, call)
	s.mu.Unlock()

	if reply.release != nil {
		select {
		case <-reply.release:
		case <-r.Context().Done():
			return
		case <-s.closed:
			return
		}
	}

	if reply.disconnect {
		dropConnection(w)
		return
	}
	writeReply(w, reply)
	if parseErr != nil {
		// The body was not read to its end, often because the client gave
		// up.
		dropConnection(w)
	}
}

// dropConnection closes the connection of the call being answered. For a call
// whose body is unread, this also spares the server's half-second wait before
// closing it.
func dropConnection(w http.ResponseWriter) {
	if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
		conn.Close()
	}
}

// success builds a plausible successful result for call. s.mu must be held.
func (s *Server) success(call Call) Reply {
	var result any
	switch call.Method {
	case "getMe":
		result = botUser()
	case "getChatMember":
		result = map[string]any{"status": "administrator", "user": botUser(), "can_post_messages": true}
		if m, ok := s.members[call.ChatID]; ok && call.Fields["user_id"] == strconv.Itoa(BotID) {
			result = m
		}
	default:
		s.messageID++
		result = map[string]any{
			"message_id": s.messageID,
			"date":       0,
			"chat":       map[string]any{"id": call.ChatID, "type": "channel"},
		}
	}
	body, _ := json.Marshal(map[string]any{"ok": true, "result": result})
	return Reply{Status: http.StatusOK, Body: string(body)}
}

func botUser() map[string]any {
	return map[string]any{"id": BotID, "is_bot": true, "first_name": "immich-tg", "username": BotUsername}
}

func writeReply(w http.ResponseWriter, r Reply) {
	if r.Body != "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(r.Status)
	io.WriteString(w, r.Body)
}

// parseCall reads the request's parameters. On error, the call holds what was
// read so far.
func parseCall(r *http.Request) (call Call, err error) {
	call = Call{Fields: map[string]string{}, Files: map[string]File{}}
	defer func() { call.ChatID = call.Fields["chat_id"] }()
	for k, v := range r.URL.Query() {
		call.Fields[k] = v[0]
	}

	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mediaType {
	case "application/json":
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return call, fmt.Errorf("decode JSON: %w", err)
		}
		for k, raw := range body {
			var str string
			if json.Unmarshal(raw, &str) == nil {
				call.Fields[k] = str
			} else {
				call.Fields[k] = string(raw)
			}
		}
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			return call, fmt.Errorf("parse form: %w", err)
		}
		for k, v := range r.PostForm {
			call.Fields[k] = v[0]
		}
	case "multipart/form-data":
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return call, fmt.Errorf("read multipart: %w", err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				return call, fmt.Errorf("read part %q: %w", part.FormName(), err)
			}
			if part.FileName() != "" {
				call.Files[part.FormName()] = File{
					Filename:    part.FileName(),
					ContentType: part.Header.Get("Content-Type"),
					Data:        data,
				}
			} else {
				call.Fields[part.FormName()] = string(data)
			}
		}
	}
	return call, nil
}
