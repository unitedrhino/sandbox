package sandboxsdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 是 sandbox HTTP API 的 Go SDK 客户端
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient 创建一个新的 sandbox 客户端
// baseURL 例如 "http://localhost:8080"
func NewClient(baseURL string) *Client {
	return NewClientWithHTTP(baseURL, &http.Client{Timeout: 30 * time.Second})
}

// NewClientWithHTTP 使用自定义 http.Client 创建客户端
func NewClientWithHTTP(baseURL string, hc *http.Client) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{baseURL: baseURL, httpClient: hc}
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

func (c *Client) decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sandbox API error: %d %s: %s", resp.StatusCode, resp.Status, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// Healthz 检查服务健康状态
func (c *Client) Healthz(ctx context.Context) (*HealthResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/healthz", nil)
	if err != nil {
		return nil, err
	}
	var result HealthResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Readyz 检查服务就绪状态
func (c *Client) Readyz(ctx context.Context) (*ReadyzResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/readyz", nil)
	if err != nil {
		return nil, err
	}
	var result ReadyzResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetStatus 获取运行时状态
func (c *Client) GetStatus(ctx context.Context) (*StatusPayload, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/runtime/status", nil)
	if err != nil {
		return nil, err
	}
	var result StatusPayload
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListSkills 列出所有 Skill
func (c *Client) ListSkills(ctx context.Context) ([]SkillInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/runtime/skills", nil)
	if err != nil {
		return nil, err
	}
	var result []SkillInfo
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ReloadSkills 重新加载 Skill 列表
func (c *Client) ReloadSkills(ctx context.Context) (*ReloadSkillsResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/runtime/skills/reload", nil)
	if err != nil {
		return nil, err
	}
	var result ReloadSkillsResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSkill 获取指定 Skill 信息
func (c *Client) GetSkill(ctx context.Context, name string) (*SkillInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/runtime/skills/"+name, nil)
	if err != nil {
		return nil, err
	}
	var result SkillInfo
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ActivateSkill 激活指定 Skill 的指定版本
func (c *Client) ActivateSkill(ctx context.Context, name, version string) (*SkillActivateResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/runtime/skills/"+name+"/activate", SkillActivateRequest{Version: version})
	if err != nil {
		return nil, err
	}
	var result SkillActivateResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// StartRuntime 启动运行时
func (c *Client) StartRuntime(ctx context.Context) (*StartStopResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/runtime/start", nil)
	if err != nil {
		return nil, err
	}
	var result StartStopResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// StopRuntime 停止运行时
func (c *Client) StopRuntime(ctx context.Context) (*StartStopResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/runtime/stop", nil)
	if err != nil {
		return nil, err
	}
	var result StartStopResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Exec 同步执行命令
func (c *Client) Exec(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/runtime/exec", req)
	if err != nil {
		return nil, err
	}
	var result ExecResult
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListTasks 列出所有任务
func (c *Client) ListTasks(ctx context.Context) ([]Task, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/runtime/tasks", nil)
	if err != nil {
		return nil, err
	}
	var result []Task
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// SubmitTask 提交异步任务
func (c *Client) SubmitTask(ctx context.Context, req ExecRequest) (*Task, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/runtime/tasks", req)
	if err != nil {
		return nil, err
	}
	var result Task
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetTask 获取任务详情
func (c *Client) GetTask(ctx context.Context, id string) (*Task, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/runtime/tasks/"+id, nil)
	if err != nil {
		return nil, err
	}
	var result Task
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelTask 取消任务
func (c *Client) CancelTask(ctx context.Context, id string) (*Task, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/runtime/tasks/"+id+"/cancel", nil)
	if err != nil {
		return nil, err
	}
	var result Task
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SubscribeEvents 订阅 SSE 事件流
// 返回一个只读 channel，当 context 取消或流结束时 channel 关闭
func (c *Client) SubscribeEvents(ctx context.Context) (<-chan Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/runtime/stream", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("sandbox API error: %d %s", resp.StatusCode, resp.Status)
	}

	events := make(chan Event, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		var currentEvent Event
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if err := json.Unmarshal([]byte(data), &currentEvent); err == nil {
					select {
					case events <- currentEvent:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return events, nil
}
