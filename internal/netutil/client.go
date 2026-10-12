package netutil

import (
	"bytes"
	"checkin/internal/logx"
	"checkin/internal/util"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

type Client struct {
	HTTP     *http.Client
	Attempts int
	Backoff  time.Duration
}

func New(timeout time.Duration) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout}, Attempts: 3, Backoff: time.Second}
}
func (c *Client) Close() { c.HTTP.CloseIdleConnections() }
func (c *Client) Request(ctx context.Context, method, url string, headers map[string]string, body any, label string) (util.M, int, error) {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
	}
	attempts := c.Attempts
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		req, e := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(encoded))
		if e != nil {
			return nil, 0, fmt.Errorf("%s 请求地址无效", label)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if body != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, e := c.HTTP.Do(req)
		status := 0
		var data []byte
		if e == nil {
			status = resp.StatusCode
			data, e = io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
			resp.Body.Close()
		}
		retry := e != nil || status == 429 || status == 500 || status == 502 || status == 503 || status == 504
		if retry && attempt < attempts-1 && ctx.Err() == nil {
			delay := c.Backoff * time.Duration(1<<attempt)
			if c.Backoff > 0 {
				delay += time.Duration(rand.Int63n(int64(c.Backoff)))
			}
			logx.Warn("%s 请求失败（HTTP %d），%.1fs 后重试（第 %d/%d 次）", label, status, delay.Seconds(), attempt+1, attempts)
			if e = util.Sleep(ctx, delay); e != nil {
				return nil, status, e
			}
			continue
		}
		if e != nil {
			if ctx.Err() != nil {
				return nil, status, ctx.Err()
			}
			return nil, status, fmt.Errorf("%s 网络请求失败（请检查网络连接）", label)
		}
		var m util.M
		if util.DecodeJSON(data, &m) != nil || m == nil {
			return nil, status, fmt.Errorf("%s 响应非 JSON 对象（HTTP %d）", label, status)
		}
		return m, status, nil
	}
	return nil, 0, fmt.Errorf("%s 请求失败", label)
}
