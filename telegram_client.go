package main

import (
	"context"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"golang.org/x/net/proxy"
	"net"
	"net/http"
	"strings"
	"time"
)

// ConnectTelegram подключает Telegram-бота с учётом настроек HTTP-клиента и прокси.
func (a *App) ConnectTelegram() (*tgbotapi.BotAPI, error) {
	client, err := a.HTTPClient()
	if err != nil {
		return nil, err
	}
	for {
		bot, err := tgbotapi.NewBotAPIWithClient(a.cfg.Telegram.Token, client)
		if err == nil {
			return bot, nil
		}
		logWarn("telegram connect error, retry in 5s: %v", err)
		time.Sleep(5 * time.Second)
	}
}

// HTTPClient создаёт HTTP-клиент для Telegram Bot API: напрямую или через настроенный прокси.
func (a *App) HTTPClient() (*http.Client, error) {
	pc := a.cfg.Telegram.Proxy
	if !pc.Enabled || strings.EqualFold(pc.Type, "none") {
		return &http.Client{Timeout: time.Duration(a.cfg.Telegram.HTTPTimeoutSec) * time.Second}, nil
	}
	switch strings.ToLower(pc.Type) {
	case "socks5":
		var auth *proxy.Auth
		if pc.Username != "" {
			auth = &proxy.Auth{User: pc.Username, Password: pc.Password}
		}
		dialer, err := proxy.SOCKS5("tcp", pc.Address, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) { return dialer.Dial(network, addr) }}, Timeout: time.Duration(a.cfg.Telegram.HTTPTimeoutSec) * time.Second}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy type: %s", pc.Type)
	}
}
