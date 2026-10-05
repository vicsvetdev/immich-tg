package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"immich-tg/internal/fakeimmich"
	"immich-tg/internal/faketelegram"
)

const partnerID = "22222222-2222-2222-2222-222222222222"

// watchStart is the harness clock's time when the service starts.
var watchStart = time.Date(2026, 9, 26, 17, 4, 5, 0, time.UTC)

// after is the time d after the Watch Start.
func after(d time.Duration) time.Time { return watchStart.Add(d) }

// textPost is the caption of the Post for the Video with the given id,
// Recording Date and Upload Date, and the n-th Share Link created.
func textPost(recordingDate, uploadDate, id string, n int) string {
	return fmt.Sprintf("📅 Recorded: %s\n⬆️ Uploaded: %s\n▶️ <a href=\"%s/api/assets/%s/original?key=%s\">Watch in original quality</a>\n🌐 <a href=\"%s\">Open in Immich</a>",
		recordingDate, uploadDate, publicURL, id, fakeimmich.ShareLinkKey(n), sharePage(n))
}

// sharePage is the URL of the n-th Share Link's page.
func sharePage(n int) string { return publicURL + "/share/" + fakeimmich.ShareLinkKey(n) }

// sharePageLink finds the Share Link page's link in a Post's caption.
var sharePageLink = regexp.MustCompile(`<a href="([^"]*/share/[^"]*)">`)

// posts returns the captions of the Posts published to the Video Channel:
// the caption of each video Post and the text of each text message, leaving
// out the calls Telegram rejected. It checks that each is in HTML parse mode,
// and that each text message keeps link previews enabled, previewing its
// Share Link page.
func (h *harness) posts() []string {
	h.t.Helper()
	var captions []string
	for _, c := range h.telegram.Posts(videoChannel) {
		if c.Fields["parse_mode"] != "HTML" {
			h.t.Errorf("Video Channel call %s with parse_mode %q, want HTML", c.Method, c.Fields["parse_mode"])
		}
		switch c.Method {
		case "sendVideo":
			if opts, ok := c.Fields["link_preview_options"]; ok {
				h.t.Errorf("video Post has link_preview_options %q, want none", opts)
			}
			captions = append(captions, c.Fields["caption"])
		case "sendMessage":
			var page string
			if m := sharePageLink.FindStringSubmatch(c.Fields["text"]); m != nil {
				page = m[1]
			}
			want, _ := json.Marshal(map[string]string{"url": page})
			if got := c.Fields["link_preview_options"]; got != string(want) {
				h.t.Errorf("Link-only Post has link_preview_options %q, want %s", got, want)
			}
			captions = append(captions, c.Fields["text"])
		default:
			h.t.Errorf("Video Channel call %s, want sendVideo or sendMessage", c.Method)
		}
	}
	return captions
}

// logLines returns every JSON log line with the given message, in order.
func (h *harness) logLines(msg string) []map[string]any {
	h.t.Helper()
	var found []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(h.stdout.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			h.t.Fatalf("log line is not JSON: %q", line)
		}
		if entry["msg"] == msg {
			found = append(found, entry)
		}
	}
	return found
}

func assertPosts(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
		t.Errorf("Video Channel Posts:\n got %q\nwant %q", got, want)
	}
}

// mustJSON decodes a JSON literal as generic JSON, like the fake's recorded
// bodies.
func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad JSON literal %s: %v", s, err)
	}
	return v
}

func TestReadyVideoIsPostedWithCaptionAndShareLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.immich.AddAsset(fakeimmich.Asset{
		ID:            "video-1",
		CreatedAt:     after(time.Minute),
		LocalDateTime: time.Date(2026, 9, 26, 19, 4, 0, 0, time.UTC),
		Transcoded:    true,
	})

	h.start()
	h.poll()

	assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", "26 Sep 2026, 17:05", "video-1", 1))
	links := h.immich.ShareLinkBodies()
	want := mustJSON(t, `{"type":"INDIVIDUAL","assetIds":["video-1"],"allowDownload":true,"showMetadata":true,"allowUpload":false}`)
	if len(links) != 1 || !reflect.DeepEqual(links[0], want) {
		t.Errorf("Share Link requests = %v, want one %v", links, want)
	}
	if calls := h.telegram.CallsTo(logChannel); len(calls) != 1 {
		t.Errorf("got %d Log Channel calls, want only the started message: %+v", len(calls), calls)
	}
}

func TestWaitingVideoIsPostedOnceReady(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.start()
	h.poll()

	h.immich.AddAsset(fakeimmich.Asset{ID: "video-1", CreatedAt: after(20 * time.Second)})
	h.clock.Advance(30 * time.Second)
	h.poll()

	assertPosts(t, h.posts())
	if n := len(h.immich.ShareLinkBodies()); n != 0 {
		t.Errorf("created %d Share Links for a Waiting Video, want 0", n)
	}
	waiting := h.logLines("New Video waiting")
	if len(waiting) != 1 || waiting[0]["asset_id"] != "video-1" || waiting[0]["first_seen"] != "2026-09-26T17:04:35Z" {
		t.Errorf("waiting log lines = %v, want video-1 first seen at 2026-09-26T17:04:35Z", waiting)
	}

	h.clock.Advance(30 * time.Second)
	h.poll()
	assertPosts(t, h.posts())

	h.immich.SetTranscoded("video-1")
	h.clock.Advance(30 * time.Second)
	h.poll()
	assertPosts(t, h.posts(), textPost("26 Sep 2026, 17:04", "26 Sep 2026, 17:04", "video-1", 1))
}

func TestVideosFromBeforeWatchStartAreExcluded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// A Watch Start with sub-millisecond precision: the search starts at the
	// millisecond before it, so the service must exclude earlier uploads itself.
	h.clock.Advance(500 * time.Microsecond)
	start := h.clock.Now()
	for _, a := range []fakeimmich.Asset{
		{ID: "last-week", CreatedAt: start.Add(-7 * 24 * time.Hour)},
		{ID: "a-second-before", CreatedAt: start.Add(-time.Second)},
		{ID: "just-before", CreatedAt: start.Add(-300 * time.Microsecond)},
		{ID: "at-watch-start", CreatedAt: start, LocalDateTime: time.Date(2020, 1, 2, 3, 4, 0, 0, time.UTC)},
	} {
		a.Transcoded = true
		h.immich.AddAsset(a)
	}

	h.start()
	h.poll()
	h.clock.Advance(30 * time.Second)
	h.poll()

	assertPosts(t, h.posts(), textPost("02 Jan 2020, 03:04", "26 Sep 2026, 17:04", "at-watch-start", 1))
	links := h.immich.ShareLinkBodies()
	if len(links) != 1 || !reflect.DeepEqual(links[0]["assetIds"], []any{"at-watch-start"}) {
		t.Errorf("Share Link requests = %v, want one for at-watch-start", links)
	}
}

func TestPartnerVideosAreIgnored(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.immich.AddAsset(fakeimmich.Asset{ID: "partner", OwnerID: partnerID, CreatedAt: after(time.Minute), Transcoded: true})

	h.start()
	h.poll()
	h.clock.Advance(30 * time.Second)
	h.poll()

	assertPosts(t, h.posts())
	if n := len(h.immich.ShareLinkBodies()); n != 0 {
		t.Errorf("created %d Share Links for a partner's Video, want 0", n)
	}
	if lines := h.logLines("New Video waiting"); len(lines) != 0 {
		t.Errorf("partner's Video is tracked: %v", lines)
	}
}

func TestOnlyVideosOnTheTimelineArePosted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, a := range []fakeimmich.Asset{
		{ID: "motion-photo-clip", Visibility: "hidden"},
		{ID: "archived", Visibility: "archive"},
		{ID: "locked", Visibility: "locked"},
		{ID: "trashed", Trashed: true},
		{ID: "photo", Type: "IMAGE"},
		{ID: "video", LocalDateTime: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)},
	} {
		a.CreatedAt = after(time.Minute)
		a.Transcoded = true
		h.immich.AddAsset(a)
	}

	h.start()
	h.poll()

	assertPosts(t, h.posts(), textPost("01 Sep 2026, 12:00", "26 Sep 2026, 17:05", "video", 1))
}

func TestSearchRequests(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// candidates and ready are the two searches of a poll from windowStart.
	candidates := func(windowStart string) map[string]any {
		return mustJSON(t, `{"filter":{"type":{"eq":"VIDEO"},"visibility":{"eq":"timeline"},"trashedAt":{"eq":null},"createdAt":{"gte":"`+windowStart+`"}},"size":1000}`)
	}
	ready := func(windowStart string) map[string]any {
		return mustJSON(t, `{"filter":{"type":{"eq":"VIDEO"},"visibility":{"eq":"timeline"},"trashedAt":{"eq":null},"createdAt":{"gte":"`+windowStart+`"},"isEncoded":{"eq":true}},"size":1000}`)
	}
	// poll runs one poll and checks it searched from windowStart.
	poll := func(step, windowStart string) {
		t.Helper()
		before := len(h.immich.SearchBodies())
		h.poll()
		h.clock.Advance(30 * time.Second)
		got := h.immich.SearchBodies()[before:]
		want := []map[string]any{candidates(windowStart), ready(windowStart)}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: search bodies\n got %v\nwant %v", step, got, want)
		}
	}

	h.start()
	poll("nothing seen yet: the window starts at the Watch Start", "2026-09-26T17:04:05.000Z")

	h.immich.AddAsset(fakeimmich.Asset{ID: "a", CreatedAt: after(10 * time.Minute), Transcoded: true})
	poll("still nothing seen", "2026-09-26T17:04:05.000Z")
	poll("newest seen minus the overlap", "2026-09-26T17:09:05.000Z")

	h.immich.AddAsset(fakeimmich.Asset{ID: "b", CreatedAt: after(20 * time.Minute)})
	h.immich.AddAsset(fakeimmich.Asset{ID: "c", CreatedAt: after(40*time.Minute + 123*time.Millisecond), Transcoded: true})
	poll("b and c are found", "2026-09-26T17:09:05.000Z")
	poll("the Waiting b holds the window back", "2026-09-26T17:24:05.000Z")

	h.immich.SetTranscoded("b")
	poll("b is posted", "2026-09-26T17:24:05.000Z")
	poll("newest seen minus the overlap again", "2026-09-26T17:39:05.123Z")

	for _, r := range h.immich.Requests() {
		if r.Header.Get("x-api-key") != apiKey {
			t.Errorf("%s %s has x-api-key %q, want %q", r.Method, r.Path, r.Header.Get("x-api-key"), apiKey)
		}
	}
	// The Source User is resolved during the startup checks, before any search.
	resolvedFirst := false
	for _, r := range h.immich.Requests() {
		if r.Method == http.MethodGet && r.Path == "/api/users/me" {
			resolvedFirst = true
			break
		}
		if r.Path == "/api/search/metadata" {
			break
		}
	}
	if !resolvedFirst {
		t.Errorf("GET /api/users/me did not come before the first search")
	}
}

func TestSearchFollowsPages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.immich.SetPageSize(2)
	var want []string
	for i := 1; i <= 5; i++ {
		recorded := time.Date(2026, 9, i, 10, 0, 0, 0, time.UTC)
		h.immich.AddAsset(fakeimmich.Asset{
			ID:            fmt.Sprintf("video-%d", i),
			CreatedAt:     after(time.Duration(i) * time.Minute),
			LocalDateTime: recorded,
			Transcoded:    true,
		})
		want = append(want, textPost(recorded.Format("02 Jan 2006, 15:04"), after(time.Duration(i)*time.Minute).Format("02 Jan 2006, 15:04"), fmt.Sprintf("video-%d", i), i))
	}

	h.start()
	h.poll()

	assertPosts(t, h.posts(), want...)
	var cursors []any
	for _, b := range h.immich.SearchBodies() {
		cursors = append(cursors, b["cursor"])
	}
	wantCursors := []any{nil, "cursor-2", "cursor-4", nil, "cursor-2", "cursor-4"}
	if !reflect.DeepEqual(cursors, wantCursors) {
		t.Errorf("search cursors = %v, want %v", cursors, wantCursors)
	}
}

func TestRecordingDateIsFormattedWithoutTimeZoneConversion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		localDateTime time.Time
		want          string
	}{
		{time.Date(2026, 9, 26, 19, 4, 59, 0, time.UTC), "26 Sep 2026, 19:04"},
		{time.Date(2027, 1, 5, 0, 7, 0, 0, time.UTC), "05 Jan 2027, 00:07"},
		{time.Date(2026, 12, 31, 23, 59, 0, 999, time.UTC), "31 Dec 2026, 23:59"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.immich.AddAsset(fakeimmich.Asset{ID: "video", CreatedAt: after(time.Minute), LocalDateTime: tt.localDateTime, Transcoded: true})

			h.start()
			h.poll()

			assertPosts(t, h.posts(), textPost(tt.want, "26 Sep 2026, 17:05", "video", 1))
		})
	}
}

func TestUploadDateIsShownInTheServiceTimeZone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.location = time.FixedZone("UTC+10", 10*60*60)
	h.immich.AddAsset(fakeimmich.Asset{
		ID:            "video",
		CreatedAt:     time.Date(2026, 9, 26, 17, 5, 0, 0, time.UTC),
		LocalDateTime: time.Date(2026, 9, 26, 19, 4, 0, 0, time.UTC),
		Transcoded:    true,
	})

	h.start()
	h.poll()

	// The Upload Date crosses midnight in the service's time zone; the
	// Recording Date is not converted.
	assertPosts(t, h.posts(), textPost("26 Sep 2026, 19:04", "27 Sep 2026, 03:05", "video", 1))
}

func TestBurstIsPostedInUploadOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Immich returns them by Recording Date, newest first: second, third, first.
	h.immich.AddAsset(fakeimmich.Asset{ID: "third", CreatedAt: after(3 * time.Second), LocalDateTime: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), Transcoded: true})
	h.immich.AddAsset(fakeimmich.Asset{ID: "first", CreatedAt: after(time.Second), LocalDateTime: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Transcoded: true})
	h.immich.AddAsset(fakeimmich.Asset{ID: "second", CreatedAt: after(2 * time.Second), LocalDateTime: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), Transcoded: true})

	h.start()
	h.poll()

	assertPosts(t, h.posts(),
		textPost("01 Aug 2026, 00:00", "26 Sep 2026, 17:04", "first", 1),
		textPost("20 Sep 2026, 00:00", "26 Sep 2026, 17:04", "second", 2),
		textPost("10 Sep 2026, 00:00", "26 Sep 2026, 17:04", "third", 3),
	)
	var ids []any
	for _, b := range h.immich.ShareLinkBodies() {
		ids = append(ids, b["assetIds"])
	}
	if want := []any{[]any{"first"}, []any{"second"}, []any{"third"}}; !reflect.DeepEqual(ids, want) {
		t.Errorf("Share Links created for %v, want %v", ids, want)
	}
}

func TestVideoInOverlappingPollsIsPostedOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.immich.AddAsset(fakeimmich.Asset{ID: "video", CreatedAt: after(time.Minute), Transcoded: true})

	h.start()
	for range 12 { // six minutes: past the overlap
		h.poll()
		h.clock.Advance(30 * time.Second)
	}
	h.immich.AddAsset(fakeimmich.Asset{ID: "later", CreatedAt: after(10 * time.Minute), Transcoded: true})
	for range 12 {
		h.poll()
		h.clock.Advance(30 * time.Second)
	}

	assertPosts(t, h.posts(), textPost("26 Sep 2026, 17:05", "26 Sep 2026, 17:05", "video", 1), textPost("26 Sep 2026, 17:14", "26 Sep 2026, 17:14", "later", 2))
	if n := len(h.immich.ShareLinkBodies()); n != 2 {
		t.Errorf("created %d Share Links, want 2", n)
	}
}

func TestVideoIsNotRetriedAfterShareLinkFailure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.immich.FailShareLinks(http.StatusForbidden)
	h.immich.AddAsset(fakeimmich.Asset{ID: "video", CreatedAt: after(time.Minute), Transcoded: true})

	h.start()
	h.poll()
	h.clock.Advance(30 * time.Second)
	h.poll()

	assertPosts(t, h.posts())
	if n := len(h.immich.ShareLinkBodies()); n != 1 {
		t.Errorf("tried %d Share Links, want 1", n)
	}
}

func TestVideoIsNotRetriedAfterPostFailure(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.start()
	h.poll()
	h.immich.AddAsset(fakeimmich.Asset{ID: "video", CreatedAt: after(time.Minute), Transcoded: true})
	h.telegram.Enqueue("sendVideo", faketelegram.JSONError(400, "Bad Request: chat not found"))
	h.clock.Advance(30 * time.Second)
	h.poll()
	h.poll()

	calls := h.telegram.CallsTo(videoChannel)
	if len(calls) != 2 || calls[0].Method != "sendVideo" || calls[1].Method != "sendMessage" {
		t.Errorf("got Video Channel calls %+v, want the single failed Post and its Link-only Post", calls)
	}
}

func TestTrackedVideosStayBoundedOverManyPolls(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Well past the steady state of about 12 tracked Videos.
	const polls = 40

	h.start()
	h.poll()
	for i := 1; i <= polls; i++ {
		h.clock.Advance(30 * time.Second)
		h.immich.AddAsset(fakeimmich.Asset{ID: fmt.Sprintf("video-%d", i), CreatedAt: h.clock.Now(), Transcoded: true})
		h.poll()
	}

	if n := len(h.posts()); n != polls {
		t.Fatalf("got %d Posts, want %d", n, polls)
	}
	waiting := h.logLines("New Video waiting")
	if len(waiting) != polls {
		t.Fatalf("got %d waiting log lines, want %d", len(waiting), polls)
	}
	// One upload every 30s, and handled Videos are kept for the 5-minute
	// overlap: about 11 handled plus the new one.
	const bound = 12
	for _, line := range waiting {
		if tracked := line["tracked"].(float64); tracked > bound {
			t.Fatalf("tracking %v Videos at %v, want at most %d", tracked, line["asset_id"], bound)
		}
	}
}

func TestPollsRunEveryPollInterval(t *testing.T) {
	// Not parallel: it measures wall-clock time between polls.
	h := newHarness(t)
	const interval = 100 * time.Millisecond
	h.env["POLL_INTERVAL"] = interval.String()
	h.polls = nil // the service's own schedule

	began := time.Now()
	h.start()
	const wantPolls = 3
	deadline := time.Now().Add(waitLimit)
	for len(h.immich.SearchBodies()) < 2*wantPolls {
		if time.Now().After(deadline) {
			t.Fatalf("got %d searches within %s, want %d polls; output:\n%s",
				len(h.immich.SearchBodies()), waitLimit, wantPolls, h.stdout)
		}
		time.Sleep(interval / 10)
	}
	// Each poll comes one interval after the previous one, never earlier, and
	// not much later: the bound is loose to tolerate a slow test machine.
	elapsed := time.Since(began)
	if elapsed < wantPolls*interval {
		t.Errorf("%d polls took %s, want at least %s", wantPolls, elapsed, wantPolls*interval)
	}
	if limit := wantPolls * interval * 5; elapsed > limit {
		t.Errorf("%d polls took %s, want at most %s", wantPolls, elapsed, limit)
	}
}
