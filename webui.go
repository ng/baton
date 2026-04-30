package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

//go:embed static/*
var staticFS embed.FS

type WebUI struct {
	cfg    *Config
	conn   *Connection
	server *http.Server
}

func NewWebUI(cfg *Config, conn *Connection) *WebUI {
	return &WebUI{cfg: cfg, conn: conn}
}

func (w *WebUI) Start() error {
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

	mux.HandleFunc("/upload", w.handleUpload)
	mux.HandleFunc("/ports", w.handlePorts)
	mux.HandleFunc("/status", w.handleStatus)

	w.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", w.cfg.Web.Port),
		Handler: mux,
	}

	return w.server.ListenAndServe()
}

func (w *WebUI) Stop() {
	if w.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		w.server.Shutdown(ctx)
	}
}

func (w *WebUI) handleUpload(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST only", http.StatusMethodNotAllowed)
		return
	}

	r.ParseMultipartForm(100 << 20) // 100MB max

	file, header, err := r.FormFile("file")
	if err != nil {
		jsonError(rw, "no file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	tmpDir := os.TempDir()
	tmpPath := filepath.Join(tmpDir, header.Filename)
	out, err := os.Create(tmpPath)
	if err != nil {
		jsonError(rw, "failed to create temp file", http.StatusInternalServerError)
		return
	}
	io.Copy(out, file)
	out.Close()
	defer os.Remove(tmpPath)

	remotePath, err := TransferFile(w.cfg, tmpPath, w.cfg.Transfer.Inbox)
	if err != nil {
		jsonError(rw, fmt.Sprintf("upload failed: %v", err), http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]string{
		"remote_path": remotePath,
		"filename":    header.Filename,
	})
}

func (w *WebUI) handlePorts(rw http.ResponseWriter, r *http.Request) {
	ports, err := ListForwardedPorts(w.cfg)
	if err != nil {
		jsonError(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]interface{}{
		"ports": ports,
	})
}

func (w *WebUI) handleStatus(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(map[string]interface{}{
		"connected": w.conn.IsAlive(),
		"host":      w.conn.host,
	})
}

func jsonError(rw http.ResponseWriter, msg string, code int) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	json.NewEncoder(rw).Encode(map[string]string{"error": msg})
}
