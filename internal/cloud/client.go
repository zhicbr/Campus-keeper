package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultHubURL = "https://keeper.egami.online"

type Client struct {
	hubURL     string
	httpClient *http.Client
}

func NewClient(hubURL string) *Client {
	if strings.TrimSpace(hubURL) == "" {
		hubURL = DefaultHubURL
	}
	hubURL = strings.TrimRight(hubURL, "/")

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Never route cloud auth through broken local proxies
	transport.DisableKeepAlives = true

	return &Client{
		hubURL: hubURL,
		httpClient: &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		},
	}
}

func (c *Client) SetHTTPClient(client *http.Client) {
	c.httpClient = client
}

type ShareResponse struct {
	Success          bool   `json:"success"`
	ShareID          string `json:"shareId"`
	EncryptedPayload string `json:"encryptedPayload"`
	Operator         string `json:"operator"`
	Memo             string `json:"memo"`
	ExpiresAt        int64  `json:"expiresAt"`
	Error            string `json:"error,omitempty"`
	Message          string `json:"message,omitempty"`
}

type PublishRequest struct {
	ShareID          string `json:"share_id"`
	EncryptedPayload string `json:"encrypted_payload"`
	Operator         string `json:"operator"`
	Memo             string `json:"memo"`
	DurationHours    int    `json:"duration_hours"`
}

type PublishResponse struct {
	Success   bool   `json:"success"`
	ShareID   string `json:"shareId"`
	ExpiresAt int64  `json:"expiresAt"`
	Memo      string `json:"memo"`
	Error     string `json:"error,omitempty"`
}

type ShareSummary struct {
	ShareID   string `json:"shareId"`
	Memo      string `json:"memo"`
	Operator  string `json:"operator"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	ShareCode string `json:"shareCode,omitempty"`
}

type ListSharesResponse struct {
	Success bool           `json:"success"`
	Shares  []ShareSummary `json:"shares"`
	Error   string         `json:"error,omitempty"`
}

// FetchShare retrieves the encrypted payload for a shareID (public read-only).
func (c *Client) FetchShare(ctx context.Context, shareID string) (*ShareResponse, error) {
	endpoint := fmt.Sprintf("%s/api/share/%s", c.hubURL, shareID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接云端分享中枢失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("读取云端响应失败: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("该好友分享码不存在或已被所有者吊销")
	}
	if resp.StatusCode == http.StatusGone {
		return nil, errors.New("该好友分享码已过期")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("云端返回异常状态码 %d: %s", resp.StatusCode, string(body))
	}

	var result ShareResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析云端响应失败: %w", err)
	}

	return &result, nil
}

// PublishShare pushes a new encrypted share to Cloudflare (Owner authenticated).
func (c *Client) PublishShare(ctx context.Context, adminToken string, req PublishRequest) (*PublishResponse, error) {
	endpoint := fmt.Sprintf("%s/api/share/publish", c.hubURL)
	payloadBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("连接云端发布服务失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("云端发布失败 (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var res PublishResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

// RevokeShare removes a share from Cloudflare (Owner authenticated).
func (c *Client) RevokeShare(ctx context.Context, adminToken, shareID string) error {
	endpoint := fmt.Sprintf("%s/api/share/revoke", c.hubURL)
	payload, _ := json.Marshal(map[string]string{"share_id": shareID})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("连接云端吊销服务失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("云端吊销失败 (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// ListMyShares retrieves the list of shares created by this owner.
func (c *Client) ListMyShares(ctx context.Context, adminToken string) ([]ShareSummary, error) {
	endpoint := fmt.Sprintf("%s/api/share/my", c.hubURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("获取分享列表失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取分享列表失败 (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var res ListSharesResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}

	return res.Shares, nil
}
