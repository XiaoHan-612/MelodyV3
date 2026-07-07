package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// generateUUID 生成标准UUID
func generateUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// 如果随机数生成失败，使用时间戳作为后备方案
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			time.Now().Unix(),
			time.Now().UnixNano()%0xffff,
			time.Now().UnixNano()%0xffff,
			time.Now().UnixNano()%0xffff,
			time.Now().UnixNano()%0xffffffffffff)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant is 10
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:]))
}

// ═══════════════════════════════════════════════
// 数据结构
// ═══════════════════════════════════════════════

type Song struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Duration int    `json:"duration"`
	Cover    string `json:"cover"`
	Source   string `json:"source"`
	URL      string `json:"url,omitempty"`
	PlayCount int   `json:"play_count,omitempty"` // 播放量
}

type Lyric struct {
	LRC      string `json:"lrc"`
	TLRC     string `json:"tlrc"`
	QRC      string `json:"qrc"`
}

// ═══════════════════════════════════════════════
// 音乐源接口
// ═══════════════════════════════════════════════

type MusicSource interface {
	Search(keyword string) ([]Song, error)
	GetURL(id string) (string, error)
	GetLyric(id string) (*Lyric, error)
}

// ═══════════════════════════════════════════════
// 酷狗音乐
// ═══════════════════════════════════════════════

type KugouSource struct{}

func (k *KugouSource) Search(keyword string) ([]Song, error) {
	apiURL := fmt.Sprintf(
		"http://mobilecdn.kugou.com/api/v3/search/song?keyword=%s&page=1&pagesize=20",
		url.QueryEscape(keyword),
	)

	body, _, status := httpGet(apiURL, "")
	if status != 200 {
		return nil, fmt.Errorf("kugou search failed: %d", status)
	}

	var result struct {
		Data struct {
			Songs []struct {
				Hash     string `json:"hash"`
				SongName string `json:"songname"`
				SingerName string `json:"singername"`
				Duration int    `json:"duration"`
				AlbumID  string `json:"album_id"`
				AlbumName string `json:"album_name"`
			} `json:"info"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	songs := make([]Song, 0, len(result.Data.Songs))
	for _, s := range result.Data.Songs {
		songs = append(songs, Song{
			ID:       s.Hash,
			Title:    s.SongName,
			Artist:   s.SingerName,
			Album:    s.AlbumName,
			Duration: s.Duration,
			Source:   "kg",
		})
	}

	return songs, nil
}

func (k *KugouSource) GetURL(hash string) (string, error) {
	apiURL := fmt.Sprintf(
		"http://m.kugou.com/app/i/getSongInfo.php?cmd=playInfo&hash=%s",
		strings.ToUpper(hash),
	)

	body, _, status := httpGet(apiURL, "http://m.kugou.com")
	if status != 200 {
		return "", fmt.Errorf("kugou get url failed: %d", status)
	}

	var result struct {
		URL string `json:"url"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if result.URL == "" {
		return "", fmt.Errorf("no url found")
	}

	return result.URL, nil
}

func (k *KugouSource) GetLyric(hash string) (*Lyric, error) {
	// 第一步：搜索歌词候选
	searchURL := fmt.Sprintf(
		"http://lyrics.kugou.com/search?ver=1&man=yes&client=pc&hash=%s",
		strings.ToUpper(hash),
	)

	body, _, status := httpGet(searchURL, "http://m.kugou.com")
	if status != 200 {
		return nil, fmt.Errorf("kugou lyric search failed: %d", status)
	}

	var searchResult struct {
		Candidates []struct {
			ID        string `json:"id"`
			AccessKey string `json:"accesskey"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(body, &searchResult); err != nil {
		return nil, err
	}

	if len(searchResult.Candidates) == 0 {
		return &Lyric{}, nil
	}

	c := searchResult.Candidates[0]

	// 第二步：下载歌词
	dlURL := fmt.Sprintf(
		"http://lyrics.kugou.com/download?ver=1&client=pc&fmt=lrc&id=%s&accesskey=%s",
		c.ID, c.AccessKey,
	)

	body, _, status = httpGet(dlURL, "http://m.kugou.com")
	if status != 200 {
		return nil, fmt.Errorf("kugou lyric download failed: %d", status)
	}

	var dlResult struct {
		Content string `json:"content"`
	}

	if err := json.Unmarshal(body, &dlResult); err != nil {
		return nil, err
	}

	content := dlResult.Content
	if content == "" {
		return &Lyric{}, nil
	}

	// content 不以 [ 开头说明是 base64 编码的
	if !strings.HasPrefix(content, "[") {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err == nil {
			content = string(decoded)
		}
	}

	return &Lyric{
		LRC: content,
	}, nil
}

// ═══════════════════════════════════════════════
// 网易云音乐
// ═══════════════════════════════════════════════

type NeteaseSource struct{}

func (n *NeteaseSource) Search(keyword string) ([]Song, error) {
	apiURL := "https://music.163.com/api/search/get/web"
	data := fmt.Sprintf("s=%s&type=1&offset=0&total=true&limit=20", url.QueryEscape(keyword))

	req, _ := http.NewRequest("POST", apiURL, strings.NewReader(data))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://music.163.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Result struct {
			Songs []struct {
				ID      int    `json:"id"`
				Name    string `json:"name"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name string `json:"name"`
				} `json:"album"`
				Duration int `json:"duration"`
			} `json:"songs"`
		} `json:"result"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	songs := make([]Song, 0, len(result.Result.Songs))
	for _, s := range result.Result.Songs {
		artists := make([]string, 0, len(s.Artists))
		for _, a := range s.Artists {
			artists = append(artists, a.Name)
		}

		songs = append(songs, Song{
			ID:       fmt.Sprintf("%d", s.ID),
			Title:    s.Name,
			Artist:   strings.Join(artists, "/"),
			Album:    s.Album.Name,
			Duration: s.Duration / 1000,
			Source:   "ne",
		})
	}

	return songs, nil
}

func (n *NeteaseSource) GetURL(id string) (string, error) {
	apiURL := fmt.Sprintf(
		"https://music.163.com/api/song/enhance/player/url?id=%s&ids=[%s]&br=320000",
		id, id,
	)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://music.163.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("netease get url failed: %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if len(result.Data) == 0 || result.Data[0].URL == "" {
		return "", fmt.Errorf("no url found")
	}

	return result.Data[0].URL, nil
}

func (n *NeteaseSource) GetLyric(id string) (*Lyric, error) {
	apiURL := fmt.Sprintf(
		"https://music.163.com/api/song/lyric?id=%s&lv=1&tv=1",
		id,
	)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://music.163.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("netease lyric failed: %d", resp.StatusCode)
	}

	var result struct {
		LRC struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		TLRC struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return &Lyric{
		LRC:  result.LRC.Lyric,
		TLRC: result.TLRC.Lyric,
	}, nil
}

// ═══════════════════════════════════════════════
// B站音乐
// ═══════════════════════════════════════════════

type BilibiliSource struct{}

func (b *BilibiliSource) Search(keyword string) ([]Song, error) {
	params := map[string]string{
		"keyword": keyword,
		"page":    "1",
		"pagesize": "20",
		"search_type": "video",
	}

	qs := globalSigner.sign(params)
	apiURL := "https://api.bilibili.com/x/web-interface/wbi/search/type?" + qs

	// 使用更完整的请求头
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	// 生成随机buvid3
	buvid3 := generateUUID()
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cookie", "buvid3="+buvid3+"; b_nut="+strconv.FormatInt(time.Now().Unix(), 10))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("bilibili search failed: %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			Result []struct {
				Bvid    string `json:"bvid"`
				Title   string `json:"title"`
				Author  string `json:"author"`
				Duration string `json:"duration"`
				Pic     string `json:"pic"`
				Play    int    `json:"play"` // 播放量
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	songs := make([]Song, 0, len(result.Data.Result))
	for _, s := range result.Data.Result {
		// 解析时长 "mm:ss"
		duration := 0
		parts := strings.Split(s.Duration, ":")
		if len(parts) == 2 {
			var min, sec int
			fmt.Sscanf(parts[0], "%d", &min)
			fmt.Sscanf(parts[1], "%d", &sec)
			duration = min*60 + sec
		}

		// 清理标题中的 HTML 标签
		title := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s.Title, "")

		songs = append(songs, Song{
			ID:       s.Bvid,
			Title:    title,
			Artist:   s.Author,
			Duration: duration,
			Cover:    "https:" + s.Pic,
			Source:   "bl",
			PlayCount: s.Play, // 播放量
		})
	}

	// 按播放量从高到低排序
	sort.Slice(songs, func(i, j int) bool {
		return songs[i].PlayCount > songs[j].PlayCount
	})

	return songs, nil
}

func (b *BilibiliSource) GetURL(bvid string) (string, error) {
	// 先获取视频信息
	apiURL := fmt.Sprintf(
		"https://api.bilibili.com/x/web-interface/view?bvid=%s",
		bvid,
	)

	// 使用完整的请求头和Cookie
	buvid3 := generateUUID()
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")
	req.Header.Set("Cookie", "buvid3="+buvid3+"; b_nut="+strconv.FormatInt(time.Now().Unix(), 10))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("bilibili get video info failed: %d, body: %s", resp.StatusCode, string(body))
	}

	var info struct {
		Code int `json:"code"`
		Data struct {
			CID int64 `json:"cid"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}

	if info.Code != 0 {
		return "", fmt.Errorf("bilibili get video info error: code=%d", info.Code)
	}

	// 获取音频流
	params := map[string]string{
		"bvid":   bvid,
		"cid":    fmt.Sprintf("%d", info.Data.CID),
		"fnval":  "16",
		"fourk":  "1",
	}

	qs := globalSigner.sign(params)
	playURL := "https://api.bilibili.com/x/player/playurl?" + qs

	req2, err := http.NewRequest("GET", playURL, nil)
	if err != nil {
		return "", err
	}

	req2.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req2.Header.Set("Referer", "https://www.bilibili.com")
	req2.Header.Set("Origin", "https://www.bilibili.com")
	req2.Header.Set("Cookie", "buvid3="+buvid3+"; b_nut="+strconv.FormatInt(time.Now().Unix(), 10))

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()

	body2, err := io.ReadAll(resp2.Body)
	if err != nil {
		return "", err
	}

	if resp2.StatusCode != 200 {
		return "", fmt.Errorf("bilibili get play url failed: %d, body: %s", resp2.StatusCode, string(body2))
	}

	var playResult struct {
		Code int `json:"code"`
		Data struct {
			Dash struct {
				Audio []struct {
					BaseURL string `json:"base_url"`
				} `json:"audio"`
			} `json:"dash"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body2, &playResult); err != nil {
		return "", err
	}

	if playResult.Code != 0 {
		return "", fmt.Errorf("bilibili get play url error: code=%d", playResult.Code)
	}

	if len(playResult.Data.Dash.Audio) == 0 {
		return "", fmt.Errorf("no audio stream found")
	}

	return playResult.Data.Dash.Audio[0].BaseURL, nil
}

func (b *BilibiliSource) GetLyric(bvid string) (*Lyric, error) {
	// B站没有歌词API，返回空
	return &Lyric{}, nil
}

// ═══════════════════════════════════════════════
// 全局实例
// ═══════════════════════════════════════════════

var (
	kgSource = &KugouSource{}
	neSource = &NeteaseSource{}
	blSource = &BilibiliSource{}
)

// ═══════════════════════════════════════════════
// 搜索聚合
// ═══════════════════════════════════════════════

func searchAll(keyword string) ([]Song, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allSongs []Song
	var lastErr error

	sources := []struct {
		name   string
		source MusicSource
	}{
		{"kg", kgSource},
		{"ne", neSource},
		{"bl", blSource},
	}

	for _, s := range sources {
		wg.Add(1)
		go func(src MusicSource, name string) {
			defer wg.Done()
			songs, err := src.Search(keyword)
			if err != nil {
				mu.Lock()
				lastErr = err
				mu.Unlock()
				return
			}
			mu.Lock()
			allSongs = append(allSongs, songs...)
			mu.Unlock()
		}(s.source, s.name)
	}

	wg.Wait()

	if len(allSongs) == 0 && lastErr != nil {
		return nil, lastErr
	}

	// 按来源分组排序
	sort.Slice(allSongs, func(i, j int) bool {
		if allSongs[i].Source != allSongs[j].Source {
			sourceOrder := map[string]int{"kg": 0, "ne": 1, "bl": 2}
			return sourceOrder[allSongs[i].Source] < sourceOrder[allSongs[j].Source]
		}
		return allSongs[i].Title < allSongs[j].Title
	})

	return allSongs, nil
}

func getSongURL(source, id string) (string, error) {
	switch source {
	case "kg":
		return kgSource.GetURL(id)
	case "ne":
		return neSource.GetURL(id)
	case "bl":
		return blSource.GetURL(id)
	default:
		return "", fmt.Errorf("unknown source: %s", source)
	}
}

func getSongLyric(source, id string) (*Lyric, error) {
	switch source {
	case "kg":
		return kgSource.GetLyric(id)
	case "ne":
		return neSource.GetLyric(id)
	case "bl":
		return blSource.GetLyric(id)
	default:
		return nil, fmt.Errorf("unknown source: %s", source)
	}
}

// searchLyric 从酷狗和网易云并发搜索歌词
func searchLyric(keyword string) (*Lyric, error) {
	type result struct {
		lyric *Lyric
		err   error
	}

	ch := make(chan result, 2)

	// 并发搜索酷狗
	go func() {
		songs, err := kgSource.Search(keyword)
		if err != nil || len(songs) == 0 {
			ch <- result{nil, err}
			return
		}
		lyric, err := kgSource.GetLyric(songs[0].ID)
		ch <- result{lyric, err}
	}()

	// 并发搜索网易云
	go func() {
		songs, err := neSource.Search(keyword)
		if err != nil || len(songs) == 0 {
			ch <- result{nil, err}
			return
		}
		lyric, err := neSource.GetLyric(songs[0].ID)
		ch <- result{lyric, err}
	}()

	// 优先返回有内容的结果
	var bestLyric *Lyric
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err != nil || r.lyric == nil {
			continue
		}
		if r.lyric.LRC != "" || r.lyric.QRC != "" {
			// 优先返回有翻译歌词的
			if bestLyric == nil || (r.lyric.TLRC != "" && bestLyric.TLRC == "") {
				bestLyric = r.lyric
			}
		}
	}

	if bestLyric != nil {
		return bestLyric, nil
	}
	return &Lyric{}, nil
}

// ═══════════════════════════════════════════════
// 歌单存储
// ═══════════════════════════════════════════════

func loadPlaylist() []Song {
	data, err := os.ReadFile(plFile())
	if err != nil {
		return []Song{}
	}

	// 尝试直接解析为新格式
	var playlist []Song
	if err := json.Unmarshal(data, &playlist); err == nil {
		// 检查是否需要迁移（如果第一个元素的Title为空，可能是旧格式）
		if len(playlist) > 0 && playlist[0].Title == "" && playlist[0].Artist == "" {
			return migratePlaylistData(data)
		}
		return playlist
	}

	// 如果直接解析失败，尝试迁移旧格式
	return migratePlaylistData(data)
}

// migratePlaylistData 迁移旧格式的歌单数据
func migratePlaylistData(data []byte) []Song {
	// 先解析为通用格式
	var rawPlaylist []map[string]interface{}
	if err := json.Unmarshal(data, &rawPlaylist); err != nil {
		return []Song{}
	}

	var playlist []Song
	for _, raw := range rawPlaylist {
		song := Song{}

		// 提取ID
		if id, ok := raw["id"].(string); ok {
			song.ID = id
		}

		// 提取标题（兼容name和title）
		if title, ok := raw["title"].(string); ok {
			song.Title = title
		} else if name, ok := raw["name"].(string); ok {
			song.Title = name
		}

		// 提取艺术家（兼容singer和artist）
		if artist, ok := raw["artist"].(string); ok {
			song.Artist = artist
		} else if singer, ok := raw["singer"].(string); ok {
			song.Artist = singer
		}

		// 提取专辑
		if album, ok := raw["album"].(string); ok {
			song.Album = album
		}

		// 提取时长
		if duration, ok := raw["duration"].(float64); ok {
			song.Duration = int(duration)
		}

		// 提取封面
		if cover, ok := raw["cover"].(string); ok {
			song.Cover = cover
		}

		// 提取来源
		if source, ok := raw["source"].(string); ok {
			song.Source = source
		}

		playlist = append(playlist, song)
	}

	// 保存迁移后的数据
	if len(playlist) > 0 {
		_ = savePlaylist(playlist)
	}

	return playlist
}

func savePlaylist(playlist []Song) error {
	data, err := json.MarshalIndent(playlist, "", "  ")
	if err != nil {
		fmt.Printf("savePlaylist marshal error: %v\n", err)
		return err
	}
	if err := os.WriteFile(plFile(), data, 0644); err != nil {
		fmt.Printf("savePlaylist write error: %v\n", err)
		return err
	}
	return nil
}
