package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ═══════════════════════════════════════════════
// 服务器配置
// ═══════════════════════════════════════════════

type ServerConfig struct {
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

var defaultConfig = ServerConfig{
	Port:         21345,
	ReadTimeout:  30 * time.Second,
	WriteTimeout: 60 * time.Second,
}

// ═══════════════════════════════════════════════
// HTTP 服务器
// ═══════════════════════════════════════════════

type Server struct {
	config ServerConfig
	mux    *http.ServeMux
	server *http.Server
}

func NewServer(config ServerConfig) *Server {
	s := &Server{
		config: config,
		mux:    http.NewServeMux(),
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// 静态文件
	s.mux.HandleFunc("/", s.handleIndex)

	// API 路由
	s.mux.HandleFunc("/api/search", s.handleSearch)
	s.mux.HandleFunc("/api/song/url", s.handleSongURL)
	s.mux.HandleFunc("/api/song/lyric", s.handleSongLyric)
	s.mux.HandleFunc("/api/song/lyric/search", s.handleLyricSearch)
	s.mux.HandleFunc("/api/playlist", s.handlePlaylist)

	// 代理路由
	s.mux.HandleFunc("/proxy/", s.handleProxy)
	s.mux.HandleFunc("/audio-proxy/", s.handleAudioProxy)
	s.mux.HandleFunc("/bl-audio/", s.handleBLAudio)
	s.mux.HandleFunc("/bl/", s.handleBLAPI)
}

func (s *Server) Start() error {
	s.server = &http.Server{
		Addr:         fmt.Sprintf("127.0.0.1:%d", s.config.Port),
		Handler:      s.corsMiddleware(s.loggingMiddleware(s.mux)),
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
	}

	fmt.Printf("MelodyV3 → http://localhost:%d\n", s.config.Port)
	return s.server.ListenAndServe()
}

func (s *Server) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// ═══════════════════════════════════════════════
// 中间件
// ═══════════════════════════════════════════════

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		fmt.Printf("[%s] %s %s %v\n", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}

// ═══════════════════════════════════════════════
// 请求处理
// ═══════════════════════════════════════════════

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}

	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write(data)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	source := r.URL.Query().Get("source")

	if keyword == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"error": "keyword is required",
		})
		return
	}

	var results []Song
	var err error

	switch source {
	case "kg":
		results, err = kgSource.Search(keyword)
	case "ne":
		results, err = neSource.Search(keyword)
	case "bl":
		results, err = blSource.Search(keyword)
	default:
		// 并发搜索所有源
		results, err = searchAll(keyword)
	}

	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	s.jsonResponse(w, http.StatusOK, results)
}

func (s *Server) handleSongURL(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	source := r.URL.Query().Get("source")

	if id == "" || source == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"error": "id and source are required",
		})
		return
	}

	url, err := getSongURL(source, id)
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	s.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"url": url,
	})
}

func (s *Server) handleSongLyric(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	source := r.URL.Query().Get("source")

	if id == "" || source == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"error": "id and source are required",
		})
		return
	}

	lyric, err := getSongLyric(source, id)
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	s.jsonResponse(w, http.StatusOK, lyric)
}

func (s *Server) handleLyricSearch(w http.ResponseWriter, r *http.Request) {
	title := r.URL.Query().Get("title")
	artist := r.URL.Query().Get("artist")

	if title == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"error": "title is required",
		})
		return
	}

	keyword := title
	if artist != "" {
		keyword = title + " " + artist
	}

	lyric, err := searchLyric(keyword)
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	s.jsonResponse(w, http.StatusOK, lyric)
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		playlist := loadPlaylist()
		s.jsonResponse(w, http.StatusOK, playlist)

	case "POST":
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
				"error": "invalid request body",
			})
			return
		}

		var playlist []Song
		if err := json.Unmarshal(body, &playlist); err != nil {
			s.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
				"error": "invalid JSON",
			})
			return
		}

		if err := savePlaylist(playlist); err != nil {
			s.jsonResponse(w, http.StatusInternalServerError, map[string]interface{}{
				"error": "failed to save playlist: " + err.Error(),
			})
			return
		}

		s.jsonResponse(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})

	default:
		s.jsonResponse(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"error": "method not allowed",
		})
	}
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	targetURL := strings.TrimPrefix(r.URL.Path, "/proxy/")
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	body, contentType, status := httpGet(targetURL, "")
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	w.Write(body)
}

func (s *Server) handleBLAudio(w http.ResponseWriter, r *http.Request) {
	targetURL := "https://" + strings.TrimPrefix(r.URL.Path, "/bl-audio/")
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")

	// 转发Range请求头，支持音频seek
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "Gateway Error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 复制响应头
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		w.Header().Set("Content-Range", cr)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(resp.StatusCode)

	// 流式复制响应体
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}

func (s *Server) handleAudioProxy(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")
	if targetURL == "" {
		http.Error(w, "Missing url parameter", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://www.bilibili.com")

	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "Gateway Error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		w.Header().Set("Content-Range", cr)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(resp.StatusCode)

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
}

func (s *Server) handleBLAPI(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/bl/")
	if r.URL.RawQuery != "" {
		raw += "?" + r.URL.RawQuery
	}

	// 解析参数
	params := make(map[string]string)
	if idx := strings.IndexByte(raw, '?'); idx >= 0 {
		query := raw[idx+1:]
		raw = raw[:idx]
		for _, kv := range strings.Split(query, "&") {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				params[parts[0]] = parts[1]
			}
		}
	}

	// 签名
	qs := globalSigner.sign(params)
	targetURL := "https://api.bilibili.com" + raw + "?" + qs

	body, contentType, status := httpGet(targetURL, "https://www.bilibili.com")
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	w.Write(body)
}

// ═══════════════════════════════════════════════
// 工具函数
// ═══════════════════════════════════════════════

func (s *Server) jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func httpGet(targetURL, referer string) ([]byte, string, int) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return []byte("{}"), "application/json", 400
	}

	req.Header.Set("User-Agent", ua)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return []byte("{}"), "application/json", 502
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []byte("{}"), "application/json", 500
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}

	return body, contentType, resp.StatusCode
}
