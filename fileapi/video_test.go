package fileapi

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVideo(t *testing.T) {
	f := newFakeServer(t)
	var polls atomic.Int32
	f.handle("GET /open/v1/files/7/video", func(w http.ResponseWriter, r *http.Request) {
		status := VideoRunning
		if polls.Add(1) >= 2 {
			status = VideoReady
		}
		ok(w, map[string]any{"asset_id": 3, "status": status, "progress": 50, "profiles": []string{"720p"}, "playable": status == VideoReady,
			"renditions": []map[string]any{{"name": "720p", "width": 1280, "height": 720, "bandwidth": 2800000, "average_bandwidth": 1900000, "segments": 5, "bytes": 999}},
			"encrypted":  true, "thumbnails": map[string]any{"interval_ms": 1000, "width": 160, "height": 90, "columns": 10, "count": 4, "sheets": 1, "bytes": 12}})
	})
	f.handle("POST /open/v1/files/7/video/transcode", func(w http.ResponseWriter, r *http.Request) {
		var body map[string][]string
		readJSON(t, r, &body)
		if strings.Join(body["profiles"], ",") != "360p,720p" {
			t.Errorf("profiles = %v", body)
		}
		ok(w, map[string]any{"asset_id": 3, "status": VideoPending})
	})
	f.handle("POST /open/v1/files/7/video/cancel", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"asset_id": 3, "status": VideoCanceled})
	})
	f.handle("DELETE /open/v1/files/7/video", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })
	f.handle("GET /open/v1/files/7/play", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("expires") != "600" || q.Get("max_visits") != "2" || q.Get("revocable") != "true" || q.Get("password") != "Ab12" {
			t.Errorf("play query = %v", q)
		}
		ok(w, map[string]any{"status": VideoReady, "playable": true, "url": "https://x/v/tok/master.m3u8", "link_id": "abc", "duration_ms": 4000,
			"thumbnails_url": "https://x/v/tok/thumbs.vtt", "password_protected": true,
			"renditions": []map[string]any{{"name": "720p"}}})
	})
	c := f.client(t)
	ctx := context.Background()
	if v, err := c.Transcode(ctx, 7, "360p", "720p"); err != nil || v.Status != VideoPending {
		t.Fatalf("transcode = %+v, %v", v, err)
	}
	v, err := c.WaitVideo(ctx, 7, 10*time.Millisecond)
	if err != nil || !v.Done() || !v.Playable || v.Renditions[0].Height != 720 || v.Renditions[0].AverageBandwidth != 1900000 || polls.Load() != 2 ||
		!v.Encrypted || v.Thumbnails == nil || v.Thumbnails.Count != 4 || v.Thumbnails.IntervalMS != 1000 || v.Thumbnails.Width != 160 {
		t.Fatalf("wait = %+v, %v", v, err)
	}
	if v, err := c.CancelTranscode(ctx, 7); err != nil || v.Status != VideoCanceled {
		t.Fatalf("cancel = %+v, %v", v, err)
	}
	if err := c.DeleteVideo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	p, err := c.Play(ctx, 7, &PlayOptions{Expires: 10 * time.Minute, MaxVisits: 2, Revocable: true, Password: "Ab12"})
	if err != nil || !p.Playable || !strings.HasSuffix(p.URL, "/master.m3u8") || p.LinkID != "abc" || p.ThumbnailsURL != "https://x/v/tok/thumbs.vtt" || !p.PasswordProtected {
		t.Fatalf("play = %+v, %v", p, err)
	}
}

func TestSubtitles(t *testing.T) {
	f := newFakeServer(t)
	f.handle("PUT /open/v1/files/7/subtitles/zh-CN", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		readJSON(t, r, &body)
		if body["content"] != "1\n00:00:01,000 --> 00:00:02,000\n你好" || body["label"] != "简体中文" || body["default"] != true {
			t.Errorf("body = %v", body)
		}
		ok(w, map[string]any{"lang": "zh-CN", "label": "简体中文", "default": true, "bytes": 50})
	})
	f.handle("GET /open/v1/files/7/subtitles", func(w http.ResponseWriter, r *http.Request) {
		ok(w, []map[string]any{{"lang": "zh-CN", "label": "简体中文", "default": true}, {"lang": "en", "label": "en"}})
	})
	f.handle("GET /open/v1/files/7/subtitles/en", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"lang": "en", "content": "WEBVTT\n"})
	})
	f.handle("DELETE /open/v1/files/7/subtitles/en", func(w http.ResponseWriter, r *http.Request) { ok(w, nil) })
	c := f.client(t)
	ctx := context.Background()
	s, err := c.PutSubtitle(ctx, 7, "zh-CN", "1\n00:00:01,000 --> 00:00:02,000\n你好", &SubtitleOptions{Label: "简体中文", Default: true})
	if err != nil || s.Lang != "zh-CN" || !s.Default {
		t.Fatalf("put = %+v, %v", s, err)
	}
	if list, err := c.Subtitles(ctx, 7); err != nil || len(list) != 2 || list[1].Lang != "en" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if s, err := c.Subtitle(ctx, 7, "en"); err != nil || s.Content != "WEBVTT\n" {
		t.Fatalf("get = %+v, %v", s, err)
	}
	if err := c.DeleteSubtitle(ctx, 7, "en"); err != nil {
		t.Fatal(err)
	}
}

func TestTranscodeUploadOptions(t *testing.T) {
	f := newFakeServer(t)
	f.storage()
	f.handle("POST /open/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("transcode") != "true" || r.FormValue("profiles") != "720p,1080p" {
			t.Errorf("form = %v %v", r.MultipartForm, err)
		}
		ok(w, map[string]any{"id": 1, "video": map[string]any{"status": "pending"}})
	})
	f.handle("POST /open/v1/files/presign-upload", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		readJSON(t, r, &body)
		if body["transcode"] != true {
			t.Errorf("presign body = %v", body)
		}
		ok(w, map[string]any{"file": map[string]any{"id": 2}, "upload": f.presigned("/storage/obj", 2, "")})
	})
	f.handle("POST /open/v1/files/2/complete", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"id": 2}) })
	c := f.client(t)
	ctx := context.Background()
	res, err := c.Upload(ctx, "a.mp4", bytes.NewReader([]byte("v")), 1, &UploadOptions{Transcode: true, Profiles: []string{"720p", "1080p"}})
	if err != nil || res.Video == nil || res.Video.Status != VideoPending {
		t.Fatalf("upload = %+v, %v", res, err)
	}
	if _, err := c.UploadDirect(ctx, "b.mp4", strings.NewReader("vv"), 2, &DirectUploadOptions{Transcode: true}); err != nil {
		t.Fatal(err)
	}
	tok, _ := c.NewUploadToken(&UploadTokenOptions{Profiles: []string{"360p"}})
	if p := tokenPayload(tok.Token); !strings.Contains(p, `"transcode":true`) || !strings.Contains(p, `"profiles":["360p"]`) {
		t.Fatalf("token = %s", p)
	}
}

func TestVideoEvent(t *testing.T) {
	body := []byte(`{"id":"e3","type":"video.ready","created_at":"2026-10-13T00:00:00Z","data":{"asset_id":3,"client_id":"fa_x","status":"ready","file_ids":[7,8],"renditions":["360p","720p"],"duration_ms":4000,"bytes":999}}`)
	e, err := VerifyWebhook("s", signForTest("s", time.Now().Unix(), body), body, 0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Video()
	if err != nil || e.Type != EventVideoReady || len(v.FileIDs) != 2 || v.Renditions[1] != "720p" || v.Bytes != 999 {
		t.Fatalf("video event = %+v, %v", v, err)
	}
	if _, err := e.File(); err == nil {
		t.Fatal("video event is not a file event")
	}
}

func TestTranscodeWith(t *testing.T) {
	f := newFakeServer(t)
	var bodies []map[string]any
	f.handle("POST /open/v1/files/7/video/transcode", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		readJSON(t, r, &body)
		bodies = append(bodies, body)
		ok(w, map[string]any{"asset_id": 3, "status": VideoReady, "codecs": []string{"h264", "hevc"}, "dash": true,
			"watermark":  map[string]any{"text": "FileApi", "position": "ne", "opacity": 60, "size": 5},
			"renditions": []map[string]any{{"name": "720p", "codec": "h264"}, {"name": "720p-hevc", "codec": "hevc"}, {"name": "audio", "codec": "aac"}}})
	})
	f.handle("GET /open/v1/files/7/play", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"status": VideoReady, "playable": true, "url": "https://x/v/tok/master.m3u8", "dash_url": "https://x/v/tok/manifest.mpd"})
	})
	c := f.client(t)
	ctx := context.Background()
	v, err := c.TranscodeWith(ctx, 7, &TranscodeOptions{Profiles: []string{"720p"}, Codecs: []string{CodecH264, CodecHEVC},
		Watermark: &VideoWatermark{Text: "FileApi", Position: "ne"}})
	if err != nil || !v.Dash || v.Watermark == nil || v.Watermark.Text != "FileApi" || v.Renditions[1].Codec != CodecHEVC || v.Renditions[2].Codec != CodecAAC {
		t.Fatalf("transcode = %+v, %v", v, err)
	}
	if b := bodies[0]; len(b["codecs"].([]any)) != 2 || b["watermark"].(map[string]any)["position"] != "ne" || b["watermark"].(map[string]any)["image_file_id"] != nil {
		t.Fatalf("body = %v", b)
	}
	if _, err := c.TranscodeWith(ctx, 7, &TranscodeOptions{NoWatermark: true}); err != nil || len(bodies[1]["watermark"].(map[string]any)) != 0 || bodies[1]["codecs"] != nil {
		t.Fatalf("no watermark body = %v, %v", bodies[1], err)
	}
	if _, err := c.Transcode(ctx, 7); err != nil || bodies[2]["watermark"] != nil {
		t.Fatalf("default body = %v, %v", bodies[2], err)
	}
	if p, err := c.Play(ctx, 7, nil); err != nil || p.DashURL != "https://x/v/tok/manifest.mpd" {
		t.Fatalf("play = %+v, %v", p, err)
	}
}
