package agentrouter

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

	"relayhub/internal/adapter"
	"relayhub/internal/domain"
)

type Adapter struct {
	*adapter.BaseNewAPIAdapter
}

func New(client *http.Client) *Adapter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Adapter{
		BaseNewAPIAdapter: &adapter.BaseNewAPIAdapter{
			Client:         client,
			SiteKey:        "agentrouter",
			SiteName:       "AgentRouter",
			CheckinType:    "login",
			NeedsRelogin:   true,
			DisableCheckin: false,
		},
	}
}

func NewWithSecrets(client *http.Client, sec adapter.SecretResolver) *Adapter {
	a := New(client)
	a.Secrets = sec
	return a
}

// AffTransfer transfers referral quota (POST /api/user/aff_transfer).
// Unit is fetched from /api/status. Minimum transfer is $1 (quota >= unit).
func (a *Adapter) AffTransfer(ctx context.Context, channel domain.Channel) (bool, string, error) {
	unit := a.FetchStatusQuotaPerUnit(ctx, channel)

	code, body, err := a.CallWithAuth(ctx, channel, http.MethodGet, "/api/user/self", nil)
	if err != nil || code != http.StatusOK {
		return false, "", fmt.Errorf("failed to fetch user self: %d", code)
	}

	uData, err := adapter.ParseUserSelf(body)
	if err != nil {
		return false, "", err
	}

	if uData.AffQuota <= 0 {
		return false, "无可划转的邀请额度", nil
	}
	if uData.AffQuota < unit {
		return false, "邀请额度不足 $1，跳过划转", nil
	}

	payload, _ := json.Marshal(map[string]int64{"quota": uData.AffQuota})
	reqURL := strings.TrimRight(channel.BaseURL, "/") + "/api/user/aff_transfer"
	cred, err := a.ResolveCredential(ctx, channel)
	if err != nil {
		return false, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cred.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cred.Token)
	}
	if cred.SiteCookie != "" {
		req.Header.Set("Cookie", cred.SiteCookie)
	}

	resp, err := a.HTTPClient(channel, 15*time.Second).Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(respBytes, &res)

	if resp.StatusCode == http.StatusOK && res.Success {
		return true, "划转成功", nil
	}
	if res.Message != "" {
		return false, res.Message, errors.New(res.Message)
	}
	return false, fmt.Sprintf("HTTP %d", resp.StatusCode), fmt.Errorf("aff transfer failed with status %d", resp.StatusCode)
}
