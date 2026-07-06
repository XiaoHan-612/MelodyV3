package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ═══════════════════════════════════════════════
// B站 WBI 签名
// ═══════════════════════════════════════════════

var wbiIdx = []int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4,
	22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

// filterValue 过滤特殊字符
func filterValue(v string) string {
	return strings.NewReplacer("!", "", "'", "", "(", "", ")", "", "*", "").Replace(v)
}

type bilibiliSigner struct {
	key       string
	fetchTime time.Time
	mu        sync.Mutex
}

var globalSigner = &bilibiliSigner{}

func (s *bilibiliSigner) fetchKey() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 缓存 1 小时
	if s.key != "" && time.Since(s.fetchTime) < time.Hour {
		return
	}

	req, err := http.NewRequest("GET", "https://api.bilibili.com/x/web-interface/wbi/index/nav", nil)
	if err != nil {
		s.key = ""
		return
	}

	// 设置完整的请求头
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.bilibili.com")
	req.Header.Set("Origin", "https://www.bilibili.com")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.key = ""
		return
	}
	defer resp.Body.Close()

	// 读取响应体用于调试
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.key = ""
		return
	}

	var data struct {
		Data struct {
			WbiImg struct {
				ImgURL string `json:"img_url"`
				SubURL string `json:"sub_url"`
			} `json:"wbi_img"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		fmt.Printf("Failed to parse WBI response: %v\n", err)
		fmt.Printf("Response: %s\n", string(body))
		s.key = ""
		return
	}

	// 提取密钥
	img := strings.Split(strings.Split(data.Data.WbiImg.ImgURL, "/")[len(strings.Split(data.Data.WbiImg.ImgURL, "/"))-1], ".")[0]
	sub := strings.Split(strings.Split(data.Data.WbiImg.SubURL, "/")[len(strings.Split(data.Data.WbiImg.SubURL, "/"))-1], ".")[0]
	raw := img + sub

	b := make([]byte, len(wbiIdx))
	for i, idx := range wbiIdx {
		b[i] = raw[idx]
	}

	s.key = string(b)
	s.fetchTime = time.Now()
	fmt.Printf("WBI key fetched: %s\n", s.key)
}

func (s *bilibiliSigner) sign(params map[string]string) string {
	s.fetchKey()

	// 添加时间戳
	params["wts"] = strconv.FormatInt(time.Now().Unix(), 10)

	// 过滤特殊字符
	for k, v := range params {
		params[k] = filterValue(v)
	}

	// 排序参数
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 构建URL编码的查询字符串
	var parts []string
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(params[k]))
	}
	query := strings.Join(parts, "&")

	// 如果没有key，直接返回查询字符串
	if s.key == "" {
		return query
	}

	// 计算签名（MD5）
	h := md5.Sum([]byte(query + s.key))
	params["w_rid"] = fmt.Sprintf("%x", h)

	// 返回完整的查询字符串
	return query + "&w_rid=" + params["w_rid"]
}

