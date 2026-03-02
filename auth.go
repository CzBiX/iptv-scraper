package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	cacheFile = "cache.json"
	cacheTTL  = 30 * time.Minute
)

type CacheData struct {
	BaseURL    string `json:"base_url"`
	JSessionID string `json:"jsession_id"`
	SavedAt    int64  `json:"saved_at"`
}

type AuthClient struct {
	cfg    *Config
	client *http.Client

	jsessionID string
	baseURL    *url.URL
}

func NewAuthClient(cfg *Config) *AuthClient {
	return &AuthClient{
		cfg: cfg,
		client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // Don't auto-follow redirects, we handle them manually
			},
			Timeout: 10 * time.Second,
		},
	}
}

func (c *AuthClient) loadCache() *CacheData {
	if _, err := os.Stat(cacheFile); os.IsNotExist(err) {
		slog.Debug("No cache file found")
		return nil
	}

	data, err := os.ReadFile(cacheFile)
	if err != nil {
		slog.Error("Failed to read cache file", "err", err)
		return nil
	}

	var cache CacheData
	if err := json.Unmarshal(data, &cache); err != nil {
		slog.Error("Failed to parse cache file", "err", err)
		return nil
	}

	age := time.Since(time.Unix(cache.SavedAt, 0))
	if age > cacheTTL {
		slog.Info("Cache expired", "age", age.Seconds(), "ttl", cacheTTL.Seconds())
		return nil
	}

	slog.Debug("Cache is valid", "age", age.Seconds())
	return &cache
}

func (c *AuthClient) saveCache(baseURL *url.URL, jsessionID string) {
	cache := CacheData{
		BaseURL:    baseURL.String(),
		JSessionID: jsessionID,
		SavedAt:    time.Now().Unix(),
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		slog.Error("Failed to marshal cache", "err", err)
		return
	}
	if err := os.WriteFile(cacheFile, data, 0644); err != nil {
		slog.Error("Failed to write cache file", "err", err)
		return
	}
	slog.Debug("Session cached", "file", cacheFile)
}

func (c *AuthClient) doReq(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	var resp *http.Response
	var err error
	const maxRetries = 3

	for i := 0; i <= maxRetries; i++ {
		if i > 0 && req.GetBody != nil {
			req.Body, _ = req.GetBody()
		}

		resp, err = c.client.Do(req)
		if err != nil {
			return resp, err
		}

		if resp.StatusCode == http.StatusBadGateway && i < maxRetries {
			slog.Warn("Received 502 Bad Gateway, retrying", "url", req.URL.String(), "attempt", i+1)
			resp.Body.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		break
	}

	return resp, err
}

func (c *AuthClient) proxy(targetURL string) string {
	proxyURL := strings.Replace(targetURL, "://", "/", 1)
	return fmt.Sprintf("http://%s:5140/%s", c.cfg.RouteIP, proxyURL)
}

func (c *AuthClient) auth() error {
	cached := c.loadCache()
	if cached != nil {
		baseURL, err := url.Parse(cached.BaseURL)
		if err == nil {
			slog.Info("Restored session from cache", "base_url", baseURL)
			// Cookie must be attached to subsequent requests manually
			c.jsessionID = cached.JSessionID
			c.baseURL = baseURL
			return nil
		}
	}

	// Full authentication flow
	ctcToken, tokenURL, err := c.getCTCToken()
	if err != nil {
		return fmt.Errorf("getCTCToken: %w", err)
	}

	localIP, err := c.getIPTVIP()
	if err != nil {
		return fmt.Errorf("getIPTVIP: %w", err)
	}

	authenticator, err := c.makeAuthenticator(localIP, ctcToken)
	if err != nil {
		return fmt.Errorf("makeAuthenticator: %w", err)
	}

	_, nextURL, err := c.getUserToken(authenticator, tokenURL)
	if err != nil {
		return fmt.Errorf("getUserToken: %w", err)
	}

	jsessionID, baseURL, err := c.getSession(nextURL)
	if err != nil {
		return fmt.Errorf("getSession: %w", err)
	}

	c.saveCache(baseURL, jsessionID)
	c.jsessionID = jsessionID
	c.baseURL = baseURL
	return nil
}

func (c *AuthClient) getCTCToken() (string, string, error) {
	u := c.proxy(fmt.Sprintf("%s?UserID=%s&Action=Login", c.cfg.LoginURL, c.cfg.UserID))
	req, _ := http.NewRequest("GET", u, nil)
	resp, err := c.doReq(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	page := string(body)

	token, err := extract(`CTCGetAuthInfo\('(.+?)'\)`, page)
	if err != nil {
		return "", "", err
	}
	slog.Debug("CTCGetAuthInfo", "token", token)

	tokenURL, err := extract(`action="(.+?)"`, page)
	if err != nil {
		return "", "", err
	}
	slog.Debug("Token URL", "url", tokenURL)

	return token, tokenURL, nil
}

func (c *AuthClient) getIPTVIP() (string, error) {
	url := c.cfg.IPTVIPURL
	if url == "" {
		url = fmt.Sprintf("http://%s/cgi-bin/iptv", c.cfg.RouteIP)
	}
	req, _ := http.NewRequest("GET", url, nil)
	resp, err := c.doReq(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	address := string(data)

	return address, nil
}

func (c *AuthClient) makeAuthenticator(localIP, ctcToken string) (string, error) {
	randStr := fmt.Sprintf("%08d", rand.Intn(100000000))
	sessionRef := strings.Join([]string{
		randStr, ctcToken, c.cfg.UserID, c.cfg.StbID, localIP, c.cfg.Mac, "", "CTC",
	}, "$")

	authenticator, err := desECBEncrypt(sessionRef, c.cfg.Key)
	if err != nil {
		return "", err
	}

	slog.Debug("Authenticator", "authenticator", authenticator)
	return authenticator, nil
}

func (c *AuthClient) getUserToken(authenticator, tokenURL string) (string, string, error) {
	data := url.Values{}
	data.Set("UserID", c.cfg.UserID)
	data.Set("Authenticator", authenticator)

	req, _ := http.NewRequest("POST", c.proxy(tokenURL), strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.doReq(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	page := string(body)

	token, _ := extract(`'UserToken','(.+?)'`, page) // Might be optional based on original code 'if not token'
	if token == "" {
		return "", "", fmt.Errorf("Failed to obtain UserToken")
	}

	slog.Debug("UserToken", "token", token)

	nextURL, err := extract(`location='(.+?)'`, page)
	if err != nil {
		return "", "", err
	}
	slog.Debug("Next URL", "url", nextURL)

	return token, nextURL, nil
}

func (c *AuthClient) getSession(nextURL string) (string, *url.URL, error) {
	loadBalancedURL, err := c.getLoadBalancedURL(nextURL)
	if err != nil {
		return "", nil, err
	}
	return c.confirmAuth(loadBalancedURL)
}

func (c *AuthClient) getLoadBalancedURL(nextURL string) (string, error) {
	nextURL = strings.Replace(nextURL, "GetChannelList", "GetServiceEntry", 1)

	req, _ := http.NewRequest("GET", c.proxy(nextURL), nil)
	resp, err := c.doReq(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	page := string(body)

	nextURL, err = extract(`location='(.+?)'`, page)
	if err != nil {
		return "", fmt.Errorf("extract UserGroupNMB URL: %w", err)
	}

	req, _ = http.NewRequest("GET", c.proxy(nextURL), nil)
	resp, err = c.doReq(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	page = string(body)

	loginURL, err := extract(`'EPGDomainForLogin', '(.+?)'`, page)
	if err != nil {
		return "", fmt.Errorf("extract EPGDomainForLogin: %w", err)
	}
	if loginURL != c.cfg.LoginURL {
		return "", fmt.Errorf("New Login URL: %s", loginURL)
	}

	loadBalancedURL, err := extract(`location = '(.+?)'`, page)
	if err != nil {
		return "", fmt.Errorf("extract load-balanced URL: %w", err)
	}
	slog.Debug("Load-balanced URL", "url", loadBalancedURL)

	return loadBalancedURL, nil
}

func (c *AuthClient) confirmAuth(loadBalancedURL string) (string, *url.URL, error) {
	req, _ := http.NewRequest("GET", c.proxy(loadBalancedURL), nil)
	resp, err := c.doReq(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != 302 { // It might redirect
		return "", nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	page := string(body)

	var jsessionID string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "JSESSIONID" {
			jsessionID = cookie.Value
			break
		}
	}
	slog.Info("JSESSIONID", "jsessionid", jsessionID)

	baseURL, err := url.Parse(loadBalancedURL)
	if err != nil {
		return "", nil, err
	}
	// Strip query params
	baseURL.RawQuery = ""
	slog.Info("Base URL", "url", baseURL.String())

	postData := parseHiddenInputs(page)
	formData := url.Values{}
	for k, v := range postData {
		formData.Set(k, v)
	}

	u := baseURL.ResolveReference(&url.URL{Path: "funcportalauth.jsp"}).String()
	req, _ = http.NewRequest("POST", c.proxy(u), strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if jsessionID != "" {
		req.AddCookie(&http.Cookie{Name: "JSESSIONID", Value: jsessionID})
	}

	resp, err = c.doReq(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, _ = io.ReadAll(resp.Body)
	page = string(body)

	if !strings.Contains(page, jsessionID) {
		return "", nil, fmt.Errorf("Failed to auth: JSESSIONID not found in response")
	}

	return jsessionID, baseURL, nil
}

func (c *AuthClient) getChannelData() ([]string, error) {
	rel, _ := c.baseURL.Parse("frameset_builder.jsp")
	data := url.Values{
		"BUILD_ACTION":    {"FRAMESET_BUILDER"},
		"NEED_UPDATE_STB": {"1"},
		"MAIN_WIN_SRC":    {"/iptvepg/frame226/portal.jsp"},
		"hdmistatus":      {"undefined"},
	}

	req, _ := http.NewRequest("POST", c.proxy(rel.String()), strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "JSESSIONID", Value: c.jsessionID})

	resp, err := c.doReq(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	reader := transform.NewReader(resp.Body, simplifiedchinese.GBK.NewDecoder())
	body, _ := io.ReadAll(reader)
	page := string(body)

	re := regexp.MustCompile(`jsSetConfig\('Channel','(.+?)'\);`)
	matches := re.FindAllStringSubmatch(page, -1)
	var channels []string
	for _, m := range matches {
		channels = append(channels, m[1])
	}
	return channels, nil
}

func (c *AuthClient) getEPGData(channelID string, date string) ([]byte, error) {
	reqURL, _ := c.baseURL.Parse("/iptvepg/frame226/publicPage/datajsp/prevueList.jsp")

	q := reqURL.Query()
	q.Set("isJson", "-1")
	q.Set("isAjax", "1")
	q.Set("fields", "1")
	q.Set("channelID", channelID)
	q.Set("pageIndex", "1")
	q.Set("pageSize", "9999")
	q.Set("curdate", date)
	q.Set("isFristDate", "7")
	reqURL.RawQuery = q.Encode()

	req, _ := http.NewRequest("GET", c.proxy(reqURL.String()), nil)
	req.AddCookie(&http.Cookie{Name: "JSESSIONID", Value: c.jsessionID})

	resp, err := c.doReq(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}
