package goroku

import (
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

func (c *CustomTelegramClient) SendCodeRequest(phone string) error {
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	sentCode, err := c.client.Auth().SendCode(c.ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return err
	}
	if sc, ok := sentCode.(*tg.AuthSentCode); ok {
		c.phoneCodeHash = sc.PhoneCodeHash
	}
	return nil
}

func (c *CustomTelegramClient) SignIn(phone, code, password string) error {
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	L().Debug("SignIn", zap.Bool("has_phone", phone != ""), zap.Bool("has_code", code != ""), zap.Bool("has_hash", c.phoneCodeHash != ""), zap.Bool("has_password", password != ""))
	var err error
	if password != "" {
		// 2FA password flow
		_, err = c.client.Auth().Password(c.ctx, password)
	} else {
		// Phone code flow
		_, err = c.client.Auth().SignIn(c.ctx, phone, code, c.phoneCodeHash)
	}
	if err == nil {
		if me, selfErr := c.client.Self(c.ctx); selfErr == nil {
			c.SetIdentity(me.ID, me.Username, me)
		}
	}
	return err
}

func (c *CustomTelegramClient) QRLogin() (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("client not connected")
	}
	token, err := c.client.QR().Export(c.ctx)
	if err != nil {
		return "", err
	}
	return token.URL(), nil
}

func (c *CustomTelegramClient) QRLoginStatus() (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("client not connected")
	}
	select {
	case <-c.qrLoginSignal:
		// Fast path: Telegram sent updateLoginToken.
	default:
		// gotd may not deliver updateLoginToken in every temporary web-login setup.
		// Import is still safe to poll: while pending it returns auth.loginToken.
	}

	auth, err := c.client.QR().Import(c.ctx)
	if err != nil {
		if strings.Contains(err.Error(), "AuthLoginToken") || strings.Contains(err.Error(), "auth.loginToken") {
			return "PENDING", nil
		}
		return "", err
	}
	if auth != nil && auth.User != nil {
		if user, ok := auth.User.(*tg.User); ok {
			c.SetIdentity(user.ID, user.Username, user)
		}
	}
	return "SUCCESS", nil
}
