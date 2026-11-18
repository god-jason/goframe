package weixin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// Session 是小程序 code2Session 结果。
type Session struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
}

// Watermark 是微信加密数据水印。
type Watermark struct {
	Timestamp int64  `json:"timestamp"`
	AppID     string `json:"appid"`
}

// PhoneInfo 是小程序手机号信息。
type PhoneInfo struct {
	PhoneNumber     string     `json:"phoneNumber"`
	PurePhoneNumber string     `json:"purePhoneNumber"`
	CountryCode     string     `json:"countryCode"`
	Watermark       *Watermark `json:"watermark,omitempty"`
}

// MiniUserInfo 是小程序用户信息。
type MiniUserInfo struct {
	OpenID    string     `json:"openId"`
	NickName  string     `json:"nickName"`
	Gender    int        `json:"gender"`
	City      string     `json:"city"`
	Province  string     `json:"province"`
	Country   string     `json:"country"`
	AvatarURL string     `json:"avatarUrl"`
	UnionID   string     `json:"unionId"`
	Watermark *Watermark `json:"watermark,omitempty"`
}

// Code2Session 使用小程序登录 code 换取 openid 与 session_key。
func (c *Client) Code2Session(ctx context.Context, code string) (*Session, error) {
	if err := c.requireKind(KindMini); err != nil {
		return nil, err
	}
	var response Session
	path := fmt.Sprintf("/sns/jscode2session?appid=%s&secret=%s&js_code=%s&grant_type=authorization_code",
		urlQueryEscape(c.app.AppID), urlQueryEscape(c.app.AppSecret), urlQueryEscape(code))
	if err := c.getJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	if response.OpenID == "" || response.SessionKey == "" {
		return nil, fmt.Errorf("weixin: code2session missing openid or session_key")
	}
	return &response, nil
}

// PhoneNumber 通过 getPhoneNumber 返回的 code 获取手机号。
func (c *Client) PhoneNumber(ctx context.Context, code string) (*PhoneInfo, error) {
	if err := c.requireKind(KindMini); err != nil {
		return nil, err
	}
	var phone *PhoneInfo
	err := c.withAccessToken(ctx, func(token string) error {
		var response struct {
			PhoneInfo *PhoneInfo `json:"phone_info"`
		}
		path := "/wxa/business/getuserphonenumber?access_token=" + url.QueryEscape(token)
		if err := c.postJSON(ctx, path, map[string]string{"code": code}, &response); err != nil {
			return err
		}
		phone = response.PhoneInfo
		return nil
	})
	if err != nil {
		return nil, err
	}
	if phone == nil || phone.PurePhoneNumber == "" {
		return nil, fmt.Errorf("weixin: empty phone number")
	}
	return phone, nil
}

// DecryptPhone 解密小程序手机号加密数据。
func (c *Client) DecryptPhone(sessionKey, encryptedData, iv string) (*PhoneInfo, error) {
	data, err := DecryptData(sessionKey, encryptedData, iv)
	if err != nil {
		return nil, err
	}
	var phone PhoneInfo
	if err := json.Unmarshal(data, &phone); err != nil {
		return nil, fmt.Errorf("weixin: decode phone data: %w", err)
	}
	if phone.PurePhoneNumber == "" {
		return nil, fmt.Errorf("weixin: decrypted phone data is empty")
	}
	return &phone, nil
}

// DecryptUserInfo 解密小程序用户信息加密数据。
func (c *Client) DecryptUserInfo(sessionKey, encryptedData, iv string) (*MiniUserInfo, error) {
	data, err := DecryptData(sessionKey, encryptedData, iv)
	if err != nil {
		return nil, err
	}
	var user MiniUserInfo
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("weixin: decode user data: %w", err)
	}
	return &user, nil
}
