package weixin

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"
)

// OAuthToken 是公众号网页授权令牌。
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	OpenID       string `json:"openid"`
	Scope        string `json:"scope"`
	UnionID      string `json:"unionid"`
}

// UserInfo 是公众号网页授权用户信息。
type UserInfo struct {
	OpenID     string   `json:"openid"`
	Nickname   string   `json:"nickname"`
	Sex        int      `json:"sex"`
	Province   string   `json:"province"`
	City       string   `json:"city"`
	Country    string   `json:"country"`
	HeadImgURL string   `json:"headimgurl"`
	Privilege  []string `json:"privilege"`
	UnionID    string   `json:"unionid"`
}

// JSSDKConfig 是微信 JS-SDK 初始化配置。
type JSSDKConfig struct {
	AppID     string `json:"appId"`
	Timestamp int64  `json:"timestamp"`
	NonceStr  string `json:"nonceStr"`
	Signature string `json:"signature"`
}

// AuthorizeURL 生成公众号网页授权地址。
// scope 为空时使用 snsapi_base；redirectURI 为空时使用配置中的 oauth_redirect。
func (c *Client) AuthorizeURL(redirectURI, scope, state string) string {
	if redirectURI == "" {
		redirectURI = c.app.OAuthRedirect
	}
	if scope == "" {
		scope = "snsapi_base"
	}
	query := url.Values{}
	query.Set("appid", c.app.AppID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", scope)
	query.Set("state", state)
	return "https://open.weixin.qq.com/connect/oauth2/authorize?" + query.Encode() + "#wechat_redirect"
}

// ExchangeCode 使用网页授权 code 换取 access_token 与 openid。
func (c *Client) ExchangeCode(ctx context.Context, code string) (*OAuthToken, error) {
	if err := c.requireKind(KindOfficial); err != nil {
		return nil, err
	}
	var response OAuthToken
	path := fmt.Sprintf("/sns/oauth2/access_token?appid=%s&secret=%s&code=%s&grant_type=authorization_code",
		urlQueryEscape(c.app.AppID), urlQueryEscape(c.app.AppSecret), urlQueryEscape(code))
	if err := c.getJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	if response.OpenID == "" {
		return nil, fmt.Errorf("weixin: oauth response missing openid")
	}
	return &response, nil
}

// RefreshOAuthToken 刷新公众号网页授权令牌。
func (c *Client) RefreshOAuthToken(ctx context.Context, refreshToken string) (*OAuthToken, error) {
	if err := c.requireKind(KindOfficial); err != nil {
		return nil, err
	}
	var response OAuthToken
	path := fmt.Sprintf("/sns/oauth2/refresh_token?appid=%s&grant_type=refresh_token&refresh_token=%s",
		urlQueryEscape(c.app.AppID), urlQueryEscape(refreshToken))
	if err := c.getJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// OAuthUserInfo 拉取网页授权用户信息。
func (c *Client) OAuthUserInfo(ctx context.Context, accessToken, openID string) (*UserInfo, error) {
	if err := c.requireKind(KindOfficial); err != nil {
		return nil, err
	}
	var response UserInfo
	path := fmt.Sprintf("/sns/userinfo?access_token=%s&openid=%s&lang=zh_CN",
		urlQueryEscape(accessToken), urlQueryEscape(openID))
	if err := c.getJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// JSAPITicket 返回公众号 JS-SDK ticket，自动缓存与刷新。
func (c *Client) JSAPITicket(ctx context.Context) (string, error) {
	if err := c.requireKind(KindOfficial); err != nil {
		return "", err
	}
	return c.ticket.get(ctx, func(ctx context.Context) (string, time.Duration, error) {
		var ticket string
		var ttl time.Duration
		err := c.withAccessToken(ctx, func(token string) error {
			var response struct {
				Ticket    string `json:"ticket"`
				ExpiresIn int64  `json:"expires_in"`
			}
			path := fmt.Sprintf("/cgi-bin/ticket/getticket?access_token=%s&type=jsapi", urlQueryEscape(token))
			if err := c.getJSON(ctx, path, &response); err != nil {
				return err
			}
			ticket = response.Ticket
			ttl = time.Duration(response.ExpiresIn) * time.Second
			return nil
		})
		if err != nil {
			return "", 0, err
		}
		if ticket == "" {
			return "", 0, fmt.Errorf("weixin: empty jsapi ticket for app %q", c.app.Name)
		}
		return ticket, ttl, nil
	})
}

// JSSDKConfig 生成指定页面的 JS-SDK 初始化配置。
// pageURL 必须是调用页面的完整 URL，且不包含 # 片段。
func (c *Client) JSSDKConfig(ctx context.Context, pageURL string) (*JSSDKConfig, error) {
	ticket, err := c.JSAPITicket(ctx)
	if err != nil {
		return nil, err
	}
	timestamp := time.Now().Unix()
	nonce := randomNonce(16)
	raw := fmt.Sprintf("jsapi_ticket=%s&noncestr=%s&timestamp=%d&url=%s", ticket, nonce, timestamp, pageURL)
	sum := sha1.Sum([]byte(raw))
	return &JSSDKConfig{
		AppID:     c.app.AppID,
		Timestamp: timestamp,
		NonceStr:  nonce,
		Signature: hex.EncodeToString(sum[:]),
	}, nil
}
