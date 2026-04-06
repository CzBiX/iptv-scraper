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

func buildFormPost(url string, data url.Values) *http.Request {
	req, _ := http.NewRequest("POST", url, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
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

	jsessionID, err := c.getUserToken(authenticator, ctcToken, tokenURL)
	if err != nil {
		return fmt.Errorf("jsessionID: %w", err)
	}

	baseURL, err := url.Parse(tokenURL)
	if err != nil {
		return fmt.Errorf("failed to parse base URL: %w", err)
	}

	c.saveCache(baseURL, jsessionID)
	c.jsessionID = jsessionID
	c.baseURL = baseURL
	return nil
}

func (c *AuthClient) getTemplateString(tmplStr string) string {
	configMap := c.cfg.mapping()
	return os.Expand(tmplStr, func(key string) string {
		if val, ok := configMap[key]; ok {
			return fmt.Sprintf("%v", val)
		}
		return ""
	})
}

func buildRelativeURL(base, path string) string {
	baseURL, _ := url.Parse(base)

	path = "./" + strings.TrimLeft(path, "/")
	return baseURL.ResolveReference(&url.URL{Path: path}).String()
}

func (c *AuthClient) getCTCToken() (string, string, error) {
	loginUrl := c.getTemplateString(c.cfg.LoginURL)
	slog.Debug("Login URL", "url", loginUrl)

	req, _ := http.NewRequest("GET", c.proxy(loginUrl), nil)
	formPosted := false
	page := ""
	redirectTimes := 0

	for redirectTimes < 2 {
		resp, err := c.doReq(req)
		if err != nil {
			return "", "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusFound {
			jumpUrl := resp.Header.Get("Location")
			loginUrl, _ = strings.CutPrefix(jumpUrl, "/http/")
			loginUrl = "http://" + loginUrl

			req, _ = http.NewRequest("GET", c.proxy(loginUrl), nil)
			redirectTimes++
			continue
		}

		if resp.StatusCode >= 400 {
			return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		page = string(body)

		// some login pages have an extra form step, if we find a form action, we need to submit it before we can get the token
		actionURL, err := findFormAction(page)
		if err != nil {
			// No form found, assume it's the token page
			break
		}

		if formPosted {
			break
		}
		formPosted = true

		slashIndex := strings.LastIndex(loginUrl, "/")
		actionURL = loginUrl[:slashIndex+1] + actionURL

		formData := parseHiddenInputs(page)
		req = buildFormPost(c.proxy(actionURL), formData)
	}

	if redirectTimes >= 2 {
		return "", "", fmt.Errorf("Too many redirects, possible redirect loop")
	}

	token, err := extract(`EncryptToken = "(.+?)"`, page)
	if err != nil {
		return "", "", err
	}
	slog.Debug("CTCGetAuthInfo", "token", token)

	tokenURL, err := findFormAction(page)
	if err != nil {
		return "", "", err
	}
	tokenURL = buildRelativeURL(loginUrl, tokenURL)
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

func (c *AuthClient) getUserToken(authenticator, ctcToken, tokenURL string) (string, error) {
	data := url.Values{}
	data.Set("UserID", c.cfg.UserID)
	data.Set("Lang", "0")
	data.Set("SupportHD", "1")
	data.Set("NetUserID", fmt.Sprintf("tv%s@itv", c.cfg.UserID))
	data.Set("Authenticator", authenticator)
	data.Set("STBType", "B860AV1.1-T2")
	data.Set("STBVersion", "V81511329.1012")
	data.Set("conntype", "ipoe")
	data.Set("STBID", c.cfg.StbID)
	data.Set("templateName", "")
	data.Set("areaId", "")
	data.Set("userToken", ctcToken)
	data.Set("userGroupId", "")
	data.Set("productPackageId", "-1")
	data.Set("mac", c.cfg.Mac)
	data.Set("UserField", "0")
	data.Set("SoftwareVersion", "V81511329.1012")
	data.Set("IsSmartStb", "0")
	data.Set("desktopId", "")
	data.Set("stbmaker", "")
	data.Set("VIP", "")

	req := buildFormPost(c.proxy(tokenURL), data)
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

	token, _ := extract(`'UserToken','(.+?)'`, page) // Might be optional based on original code 'if not token'
	if token == "" {
		return "", fmt.Errorf("Failed to obtain UserToken")
	}

	slog.Debug("UserToken", "token", token)

	var jsessionID string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "JSESSIONID" {
			jsessionID = cookie.Value
			break
		}
	}
	slog.Info("JSESSIONID", "jsessionid", jsessionID)

	return jsessionID, nil
}

func (c *AuthClient) getChannelData() ([]string, error) {
	rel, _ := c.baseURL.Parse("/EPG/jsp/getchannellistHWCTC.jsp")
	data := url.Values{}
	data.Set("SupportHD", "1")
	data.Set("Lang", "1")

	req := buildFormPost(c.proxy(rel.String()), data)
	req.AddCookie(&http.Cookie{Name: "JSESSIONID", Value: c.jsessionID})

	resp, err := c.doReq(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	page := string(body)

	re := regexp.MustCompile(`CTCSetConfig\('Channel','(.+?)'\);`)
	matches := re.FindAllStringSubmatch(page, -1)
	var channels []string
	for _, m := range matches {
		channels = append(channels, m[1])
	}
	return channels, nil
}

func (c *AuthClient) getEPGData(channelID string, date string) ([]byte, error) {
	reqURL, _ := c.baseURL.Parse(fmt.Sprintf("/EPG/jsp/gdhdpublic/Ver.3/common/data.jsp?Action=channelProgramList&channelId=%s&date=%s", channelID, date))

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
