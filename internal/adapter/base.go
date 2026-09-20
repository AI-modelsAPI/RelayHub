package adapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"relayhub/internal/domain"
)

const (
	DefaultQuotaPerUnit = int64(500000)
	JWTSkew             = 60 * time.Second
)

var (
	cstZone = time.FixedZone("Asia/Shanghai", 8*3600)
	usdReg  = regexp.MustCompile(`[＄$]\s*([0-9]+(?:\.[0-9]+)?)`)
)

// AccountCredential represents the plaintext secret stored for a channel.
type AccountCredential struct {
	Token        string `json:"token"`
	SiteCookie   string `json:"site_cookie"`
	SiteUserID   string `json:"site_user_id"`
	LastCheckin  string `json:"last_checkin,omitempty"`
	CheckedToday bool   `json:"checked_today,omitempty"`
}

// AccountState represents the 4-state enum from autosign Engine.acctState.
type AccountState int

const (
	StateNeedAuth AccountState = 0
	StateChecked  AccountState = 1
	StatePending  AccountState = 2
	StateWeb      AccountState = 3
)

// BaseNewAPIAdapter provides shared HTTP logic for all New API family adapters.
type BaseNewAPIAdapter struct {
	Client         *http.Client
	Secrets        SecretResolver
	SiteKey        string
	SiteName       string
	CheckinType    string // "newapi", "login", "web"
	NeedsRelogin   bool
	DisableCheckin bool

	refreshLocks sync.Map // accountRef -> *sync.Mutex
}

func (b *BaseNewAPIAdapter) getLock(accountKey string) *sync.Mutex {
	v, _ := b.refreshLocks.LoadOrStore(accountKey, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (b *BaseNewAPIAdapter) ResolveCredential(ctx context.Context, channel domain.Channel) (*AccountCredential, error) {
	if channel.CredentialRef == "" {
		return nil, fmt.Errorf("%w: credential_ref is empty", ErrSecretRequired)
	}
	if b.Secrets == nil {
		return nil, fmt.Errorf("%w: no secret store configured on adapter", ErrSecretRequired)
	}
	raw, err := b.Secrets.Get(ctx, channel.CredentialRef)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve credential %q: %w", channel.CredentialRef, err)
	}

	cred := &AccountCredential{}
	// If raw is a JSON structure
	if err := json.Unmarshal(raw, cred); err == nil && (cred.Token != "" || cred.SiteCookie != "") {
		return cred, nil
	}
	// Otherwise treat raw as a plaintext token string
	tokenStr := strings.TrimSpace(string(raw))
	if tokenStr == "" {
		return nil, fmt.Errorf("%w: credential plaintext is empty", ErrSecretRequired)
	}
	cred.Token = tokenStr
	return cred, nil
}

func (b *BaseNewAPIAdapter) SaveCredential(ctx context.Context, channel domain.Channel, cred *AccountCredential) error {
	if b.Secrets == nil || channel.CredentialRef == "" {
		return nil
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	return b.Secrets.Put(ctx, channel.CredentialRef, data)
}

// HasRefreshCookie checks if cookie has new_api_refresh.
func HasRefreshCookie(cookie string) bool {
	if cookie == "" {
		return false
	}
	for _, pair := range strings.Split(cookie, ";") {
		if strings.HasPrefix(strings.TrimSpace(pair), "new_api_refresh=") {
			return true
		}
	}
	return false
}

// MergeCookies merges Set-Cookie headers into oldCookie, omitting deleted ones.
func MergeCookies(oldCookie string, setCookies []string) string {
	values := make(map[string]string)
	if oldCookie != "" {
		for _, pair := range strings.Split(oldCookie, ";") {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) == 2 && parts[0] != "" {
				values[parts[0]] = parts[1]
			}
		}
	}
	for _, raw := range setCookies {
		pair := strings.TrimSpace(strings.SplitN(raw, ";", 2)[0])
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 && parts[0] != "" {
			name := parts[0]
			val := parts[1]
			if val == "" || strings.EqualFold(val, "deleted") {
				delete(values, name)
			} else {
				values[name] = val
			}
		}
	}
	var pairs []string
	for k, v := range values {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, "; ")
}

// ExpMs returns the expiration timestamp of a JWT in milliseconds. Returns 0 if invalid.
func ExpMs(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0
	}
	pl := parts[1]
	if pad := len(pl) % 4; pad != 0 {
		pl += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.URLEncoding.DecodeString(pl)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(pl)
		if err != nil {
			return 0
		}
	}
	var payload struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Exp <= 0 {
		return 0
	}
	return payload.Exp * 1000
}

// NeedsExchange returns true if token is empty or expires within JWTSkew.
func NeedsExchange(token string) bool {
	if token == "" {
		return true
	}
	exp := ExpMs(token)
	if exp <= 0 {
		return false // Non-JWT or undecodable, rely on 401
	}
	nowMs := time.Now().UnixMilli()
	return nowMs+JWTSkew.Milliseconds() >= exp
}

// RefreshAccessToken exchanges a new access token using new_api_refresh cookie.
func (b *BaseNewAPIAdapter) RefreshAccessToken(ctx context.Context, channel domain.Channel, cred *AccountCredential, failedToken string) (string, error) {
	lockKey := channel.AccountRef
	if lockKey == "" {
		lockKey = channel.ID
	}
	mu := b.getLock(lockKey)
	mu.Lock()
	defer mu.Unlock()

	// Re-check after lock
	current := cred.Token
	if failedToken != "" {
		if current != "" && current != failedToken {
			return current, nil
		}
	} else {
		if current != "" && !NeedsExchange(current) {
			return current, nil
		}
	}

	if !HasRefreshCookie(cred.SiteCookie) {
		return "", errors.New("no refresh cookie available")
	}

	baseURL := strings.TrimRight(channel.BaseURL, "/")
	reqURL := baseURL + "/api/user/auth/refresh"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("Cookie", cred.SiteCookie)

	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res struct {
		Success bool `json:"success"`
		Data    struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &res)

	if resp.StatusCode != http.StatusOK || !res.Success || res.Data.AccessToken == "" {
		return "", fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, res.Message)
	}

	newCookie := MergeCookies(cred.SiteCookie, resp.Header.Values("Set-Cookie"))
	cred.Token = res.Data.AccessToken
	if newCookie != "" {
		cred.SiteCookie = newCookie
	}
	_ = b.SaveCredential(ctx, channel, cred)

	return cred.Token, nil
}

// CallWithAuth executes request with bearer token or cookie, handling token refresh on expiry/401.
func (b *BaseNewAPIAdapter) CallWithAuth(ctx context.Context, channel domain.Channel, method, path string, reqBody []byte) (int, []byte, error) {
	cred, err := b.ResolveCredential(ctx, channel)
	if err != nil {
		return 0, nil, err
	}

	// Active renewal before request if refresh cookie exists and token expiring
	if HasRefreshCookie(cred.SiteCookie) && NeedsExchange(cred.Token) {
		_, _ = b.RefreshAccessToken(ctx, channel, cred, "")
	}

	doReq := func(token, cookie string) (int, []byte, http.Header, error) {
		baseURL := strings.TrimRight(channel.BaseURL, "/")
		fullURL := baseURL + path
		var rdr io.Reader
		if len(reqBody) > 0 {
			rdr = bytes.NewReader(reqBody)
		}
		req, err := http.NewRequestWithContext(ctx, method, fullURL, rdr)
		if err != nil {
			return 0, nil, nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		if cred.SiteUserID != "" {
			req.Header.Set("X-User-Id", cred.SiteUserID)
		}
		if len(reqBody) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		client := b.Client
		if client == nil {
			client = &http.Client{Timeout: 15 * time.Second}
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, nil, err
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return resp.StatusCode, respBytes, resp.Header, nil
	}

	status, body, _, err := doReq(cred.Token, cred.SiteCookie)
	if err != nil {
		return status, body, err
	}

	// If 401 and we have refresh cookie, retry once
	if status == http.StatusUnauthorized && HasRefreshCookie(cred.SiteCookie) {
		newToken, rErr := b.RefreshAccessToken(ctx, channel, cred, cred.Token)
		if rErr == nil && newToken != "" {
			status, body, _, err = doReq(newToken, cred.SiteCookie)
		}
	}

	return status, body, err
}

// FetchStatusQuotaPerUnit reads /api/status to get quota_per_unit.
func (b *BaseNewAPIAdapter) FetchStatusQuotaPerUnit(ctx context.Context, channel domain.Channel) int64 {
	baseURL := strings.TrimRight(channel.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/status", nil)
	if err != nil {
		return DefaultQuotaPerUnit
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return DefaultQuotaPerUnit
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res struct {
		Data struct {
			QuotaPerUnit int64 `json:"quota_per_unit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err == nil && res.Data.QuotaPerUnit > 0 {
		return res.Data.QuotaPerUnit
	}
	return DefaultQuotaPerUnit
}

// UserSelfData represents fields extracted from /api/user/self.
type UserSelfData struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Quota       int64  `json:"quota"`
	UsedQuota   int64  `json:"used_quota"`
	AffQuota    int64  `json:"aff_quota"`
	AccessToken string `json:"access_token"`
	CheckedIn   bool   `json:"checked_in"`
	HasUserWrap bool   `json:"-"`
}

func ParseUserSelf(body []byte) (*UserSelfData, error) {
	var wrap struct {
		Success bool `json:"success"`
		Data    struct {
			User *UserSelfData `json:"user"`
			*UserSelfData
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	if wrap.Data.User != nil {
		wrap.Data.User.HasUserWrap = true
		return wrap.Data.User, nil
	}
	if wrap.Data.UserSelfData != nil {
		return wrap.Data.UserSelfData, nil
	}
	return nil, errors.New("empty user data in response")
}

func BeijingTodayStr() string {
	return time.Now().In(cstZone).Format("2006-01-02")
}

func BeijingMonthStr() string {
	return time.Now().In(cstZone).Format("2006-01")
}

func BeijingStartOfDayTimestamp() int64 {
	now := time.Now().In(cstZone)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, cstZone)
	return start.Unix()
}

// CheckinStatus queries GET /api/user/checkin?month=YYYY-MM for today's checkin record.
func (b *BaseNewAPIAdapter) CheckinStatus(ctx context.Context, channel domain.Channel, unit int64) (checked bool, rewardUSD float64, rewardKnown bool, err error) {
	if unit <= 0 {
		unit = DefaultQuotaPerUnit
	}
	month := BeijingMonthStr()
	path := "/api/user/checkin?month=" + month
	code, body, err := b.CallWithAuth(ctx, channel, http.MethodGet, path, nil)
	if err != nil || code != http.StatusOK {
		return false, 0, false, err
	}

	var res struct {
		Success bool `json:"success"`
		Data    struct {
			Stats struct {
				CheckedInToday bool `json:"checked_in_today"`
				Records        []struct {
					CheckinDate  string  `json:"checkin_date"`
					Date         string  `json:"date"`
					QuotaAwarded float64 `json:"quota_awarded"`
					Quota        float64 `json:"quota"`
				} `json:"records"`
			} `json:"stats"`
			Records []struct {
				CheckinDate  string  `json:"checkin_date"`
				Date         string  `json:"date"`
				QuotaAwarded float64 `json:"quota_awarded"`
				Quota        float64 `json:"quota"`
			} `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return false, 0, false, err
	}

	today := BeijingTodayStr()
	records := res.Data.Stats.Records
	if len(records) == 0 {
		records = res.Data.Records
	}

	for _, r := range records {
		d := r.CheckinDate
		if d == "" {
			d = r.Date
		}
		if d == today {
			raw := r.QuotaAwarded
			if raw == 0 {
				raw = r.Quota
			}
			var usd float64
			if raw >= 1000 {
				usd = math.Round(raw/float64(unit)*100.0) / 100.0
			} else {
				usd = raw
			}
			return true, usd, true, nil
		}
	}
	return false, 0, false, nil
}

// TodayBonus queries GET /api/log/self?type=4&limit=30 as fallback.
func (b *BaseNewAPIAdapter) TodayBonus(ctx context.Context, channel domain.Channel) (checked bool, rewardUSD float64, rewardKnown bool, err error) {
	path := "/api/log/self?type=4&limit=30&page=1"
	code, body, err := b.CallWithAuth(ctx, channel, http.MethodGet, path, nil)
	if err != nil || code != http.StatusOK {
		return false, 0, false, err
	}

	var res struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct {
				CreatedAt   string `json:"created_at"`
				Time        string `json:"time"`
				Content     string `json:"content"`
				Description string `json:"description"`
				Quota       int64  `json:"quota"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return false, 0, false, err
	}

	today := BeijingTodayStr()
	for _, it := range res.Data.Items {
		text := it.Content
		if text == "" {
			text = it.Description
		}
		if !IsCheckinLogText(text) {
			continue
		}
		timeStr := it.CreatedAt
		if timeStr == "" {
			timeStr = it.Time
		}
		// Match today's date in CST
		if strings.HasPrefix(timeStr, today) {
			usd := ParseUsdInText(text)
			if usd >= 0 {
				return true, usd, true, nil
			}
			return true, 0, false, nil
		}
		// Since logs are reverse chronological, if the first checkin log is not today, then today has none
		break
	}

	return false, 0, false, nil
}

func IsCheckinLogText(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	if !strings.Contains(text, "签到") && !strings.Contains(lower, "check-in") && !strings.Contains(lower, "checkin") {
		return false
	}
	if strings.Contains(text, "注册") || strings.Contains(text, "邀请") || strings.Contains(text, "兑换") {
		return false
	}
	return true
}

func ParseUsdInText(text string) float64 {
	m := usdReg.FindStringSubmatch(text)
	if len(m) > 1 {
		val, err := strconv.ParseFloat(m[1], 64)
		if err == nil {
			return math.Round(val*100.0) / 100.0
		}
	}
	return -1
}

// TodayUsage queries GET /api/data/self.
func (b *BaseNewAPIAdapter) TodayUsage(ctx context.Context, channel domain.Channel, unit int64) float64 {
	if unit <= 0 {
		unit = DefaultQuotaPerUnit
	}
	start := BeijingStartOfDayTimestamp()
	end := time.Now().Unix()
	path := fmt.Sprintf("/api/data/self?start_timestamp=%d&end_timestamp=%d&default_time=hour", start, end)
	code, body, err := b.CallWithAuth(ctx, channel, http.MethodGet, path, nil)
	if err != nil || code != http.StatusOK {
		return -1
	}

	var res struct {
		Data struct {
			Data []struct {
				Quota float64 `json:"quota"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return -1
	}
	var sum float64
	for _, it := range res.Data.Data {
		sum += it.Quota
	}
	return math.Round((sum/float64(unit))*100.0) / 100.0
}

// Common Validate implementation.
func (b *BaseNewAPIAdapter) Validate(ctx context.Context, channel domain.Channel) error {
	if strings.TrimSpace(channel.BaseURL) == "" {
		return fmt.Errorf("%w: base_url is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(channel.CredentialRef) == "" {
		return fmt.Errorf("%w: credential_ref is required", ErrInvalidConfig)
	}
	return nil
}

// Common Refresh implementation.
func (b *BaseNewAPIAdapter) Refresh(ctx context.Context, channel domain.Channel) error {
	cred, err := b.ResolveCredential(ctx, channel)
	if err != nil {
		return err
	}
	if HasRefreshCookie(cred.SiteCookie) {
		_, err := b.RefreshAccessToken(ctx, channel, cred, "")
		return err
	}
	// For session cookie sites, make a GET /api/user/self to refresh
	code, body, err := b.CallWithAuth(ctx, channel, http.MethodGet, "/api/user/self", nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("refresh failed with status %d: %s", code, string(body))
	}
	// Capture access_token if present in response
	if uData, err := ParseUserSelf(body); err == nil && uData.AccessToken != "" && uData.AccessToken != cred.Token {
		cred.Token = uData.AccessToken
		_ = b.SaveCredential(ctx, channel, cred)
	}
	return nil
}

// Common Balance implementation.
func (b *BaseNewAPIAdapter) Balance(ctx context.Context, channel domain.Channel) (BalanceResult, error) {
	unit := b.FetchStatusQuotaPerUnit(ctx, channel)
	code, body, err := b.CallWithAuth(ctx, channel, http.MethodGet, "/api/user/self", nil)
	if err != nil {
		return BalanceResult{}, err
	}
	if code != http.StatusOK {
		return BalanceResult{}, fmt.Errorf("balance request failed with status %d: %s", code, string(body))
	}
	uData, err := ParseUserSelf(body)
	if err != nil {
		return BalanceResult{}, fmt.Errorf("failed to parse balance data: %w", err)
	}

	// Capture token if provided in self response
	if cred, cErr := b.ResolveCredential(ctx, channel); cErr == nil {
		if uData.AccessToken != "" && uData.AccessToken != cred.Token {
			cred.Token = uData.AccessToken
			_ = b.SaveCredential(ctx, channel, cred)
		}
	}

	availUSD := math.Round((float64(uData.Quota)/float64(unit))*100.0) / 100.0
	usedUSD := math.Round((float64(uData.UsedQuota)/float64(unit))*100.0) / 100.0
	todayUSD := b.TodayUsage(ctx, channel, unit)

	name := uData.DisplayName
	if name == "" {
		name = uData.Username
	}

	return BalanceResult{
		Remaining:    uData.Quota,
		Total:        uData.Quota + uData.UsedQuota,
		AvailableUSD: availUSD,
		UsedUSD:      usedUSD,
		TodayUsedUSD: todayUSD,
		QuotaPerUnit: unit,
		Username:     name,
	}, nil
}

// Common Models implementation.
func (b *BaseNewAPIAdapter) Models(ctx context.Context, channel domain.Channel) ([]ModelInfo, error) {
	return nil, ErrUnsupportedOperation
}

// Common Health implementation.
func (b *BaseNewAPIAdapter) Health(ctx context.Context, channel domain.Channel) (HealthResult, error) {
	start := time.Now()
	_, err := b.Balance(ctx, channel)
	latency := time.Since(start)
	if err != nil {
		return HealthResult{Status: "degraded", Latency: latency, Message: err.Error()}, nil
	}
	return HealthResult{Status: "healthy", Latency: latency}, nil
}

// CheckIn handles the strict checkin logic conforming to autosign Engine.checkin.
func (b *BaseNewAPIAdapter) CheckIn(ctx context.Context, channel domain.Channel) (CheckInResult, error) {
	if b.DisableCheckin {
		return CheckInResult{
			Success:     false,
			NeedWebview: true,
			Message:     "该站启用人机验证，需在后台签到窗口完成",
		}, ErrNeedWebview
	}

	if b.CheckinType == "newapi" {
		// All newapi sites in this scope (JustDoWork, SeekAI, KKtoken) have Turnstile and cannot be checked in via pure HTTP
		return CheckInResult{
			Success:     false,
			NeedWebview: true,
			Message:     "该站启用人机验证，需在后台签到窗口完成",
		}, ErrNeedWebview
	}

	if b.CheckinType == "web" {
		return CheckInResult{
			Success: false,
			Message: "该站不开放签到接口，请点站点名打开网页手动操作",
		}, ErrUnsupportedOperation
	}

	unit := b.FetchStatusQuotaPerUnit(ctx, channel)

	// Step 1: Check checkinStatus (calendar)
	csChecked, csUSD, csKnown, _ := b.CheckinStatus(ctx, channel, unit)
	if csChecked {
		msg := "今日已签到"
		if !csKnown || csUSD <= 0 {
			msg = "今日已签到（本站无奖励）"
		}
		return CheckInResult{
			Success:     true,
			Already:     true,
			RewardUSD:   csUSD,
			RewardKnown: csKnown,
			Message:     msg,
		}, nil
	}

	// Step 2: Check todayBonus (logs) fallback
	tbChecked, tbUSD, tbKnown, _ := b.TodayBonus(ctx, channel)
	if tbChecked {
		msg := "登录即签到 · 今日奖励已到账"
		if !tbKnown || tbUSD <= 0 {
			msg = "登录即签到 · 今日已签（无奖励）"
		}
		return CheckInResult{
			Success:     true,
			Already:     true,
			RewardUSD:   tbUSD,
			RewardKnown: tbKnown,
			Message:     msg,
		}, nil
	}

	// Step 3: GET /api/user/self
	code, body, err := b.CallWithAuth(ctx, channel, http.MethodGet, "/api/user/self", nil)
	if err != nil || code != http.StatusOK {
		return CheckInResult{
			Success: false,
			Message: fmt.Sprintf("站点响应异常 (HTTP %d)", code),
		}, fmt.Errorf("self query failed: %d", code)
	}

	uData, _ := ParseUserSelf(body)
	// For NON-relogin sites only (e.g. GoRouter), checked_in boolean can serve as fallback
	if !b.NeedsRelogin && uData != nil && uData.CheckedIn {
		return CheckInResult{
			Success: true,
			Already: true,
			Message: "登录即签到 · 站点已标记今日已签",
		}, nil
	}

	// Crucial rule: NEVER report Success: true if there is no server-side record for today!
	if b.NeedsRelogin {
		return CheckInResult{
			Success:      false,
			Already:      false,
			NeedsRelogin: true,
			Message:      "站点无今日签到记录，需重新登录触发奖励发放",
		}, ErrNeedsRelogin
	}

	return CheckInResult{
		Success: false,
		Already: false,
		Message: "登录保活完成（未检测到今日签到记录，未标记已签）",
	}, nil
}
