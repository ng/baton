package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleStatus(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Connection.Host = "test@gitpod.io"
	conn := NewConnection(cfg, cfg.Connection.Host)
	w := NewWebUI(cfg, conn)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	w.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp["host"] != "test@gitpod.io" {
		t.Errorf("expected host test@gitpod.io, got %v", resp["host"])
	}

	if _, ok := resp["connected"]; !ok {
		t.Error("expected 'connected' field in response")
	}
}

func TestHandlePorts(t *testing.T) {
	cfg := DefaultConfig()
	conn := NewConnection(cfg, "test")
	w := NewWebUI(cfg, conn)

	req := httptest.NewRequest(http.MethodGet, "/ports", nil)
	rec := httptest.NewRecorder()
	w.handlePorts(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
}

func TestHandleUploadWrongMethod(t *testing.T) {
	cfg := DefaultConfig()
	conn := NewConnection(cfg, "test")
	w := NewWebUI(cfg, conn)

	req := httptest.NewRequest(http.MethodGet, "/upload", nil)
	rec := httptest.NewRecorder()
	w.handleUpload(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestServeIndex(t *testing.T) {
	cfg := DefaultConfig()
	conn := NewConnection(cfg, "test")
	w := NewWebUI(cfg, conn)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(rw, "not found", 404)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Write(data)
	})
	_ = w

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("expected html content type, got %s", ct)
	}

	body := rec.Body.String()
	if len(body) < 100 {
		t.Error("expected substantial HTML content")
	}
}
