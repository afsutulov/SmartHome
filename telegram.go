package main

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"html"
	"strconv"
	"strings"
	"time"
)

// ProcessTelegram читает входящие Telegram-сообщения, проверяет доступ и передаёт команды обработчику.
func (a *App) ProcessTelegram() {
	updates, err := a.bot.GetUpdatesChan(tgbotapi.UpdateConfig{Timeout: a.cfg.Telegram.PollTimeoutSec})
	if err != nil {
		logError("telegram polling error: %v", a.telegramError(err))
		return
	}
	for update := range updates {
		if update.Message == nil || update.Message.From == nil || update.Message.Chat == nil {
			continue
		}
		if !a.IsUserAllowed(int64(update.Message.From.ID)) {
			continue
		}
		a.HandleTelegramMessage(update.Message)
	}
}

// IsUserAllowed проверяет, разрешён ли пользователю доступ к Telegram-боту.
func (a *App) IsUserAllowed(id int64) bool { _, ok := a.users[id]; return ok }

// normalizeTelegramText приводит текст Telegram-кнопки к единому виду для поиска в menuIndex.
// Убираем NBSP и обычные пробелы по краям строк, нормализуем переносы строк.
// Telegram возвращает оригинальный текст кнопки — пробелы для центрирования убираем при поиске.
func normalizeTelegramText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\u00a0", " ")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// makeButtonText формирует текст кнопки меню с переносом строки между частями.
// Центрирование строк внутри кнопки — задача Telegram-клиента.
func makeButtonText(lines []string) string {
	return strings.Join(lines, " - ")
}

// HandleTelegramMessage обрабатывает команду Telegram, выполняет пункт меню или возвращает неизвестную команду.
func (a *App) HandleTelegramMessage(message *tgbotapi.Message) {
	logInfo("telegram request: chat_id=%d user_id=%d text=%q", message.Chat.ID, message.From.ID, message.Text)
	if message.Text == a.cfg.Menu.StartCommand || message.Text == "/start" {
		msg := tgbotapi.NewMessage(message.Chat.ID, a.cfg.Telegram.WelcomeMessage)
		msg.ReplyMarkup = a.BuildKeyboard()
		if _, err := a.bot.Send(msg); err != nil {
			logError("telegram reply error: %v", a.telegramError(err))
		}
		return
	}
	btnText := normalizeTelegramText(message.Text)
	item, ok := a.menuIndex[btnText]
	var text string
	if ok {
		logDebug("telegram menu matched: chat=%d text=%q", message.Chat.ID, btnText)
		// Настройки видеоконтроля принадлежат пользователю (From.ID), а не чату:
		// в групповом чате Chat.ID отличается от ID пользователя из allowed_users.
		text = a.ExecuteMenuItem(item, int64(message.From.ID))
	} else {
		logWarn("telegram menu item not found: chat=%d text=%q", message.Chat.ID, btnText)
		text = html.EscapeString(a.cfg.Labels.Unknown)
	}
	msg := tgbotapi.NewMessage(message.Chat.ID, text)
	msg.ParseMode = "HTML"
	if _, err := a.bot.Send(msg); err != nil {
		logError("telegram reply error: %v", a.telegramError(err))
	}
}

// BuildKeyboard строит Telegram-клавиатуру из раздела menu конфигурационного файла.
func (a *App) BuildKeyboard() tgbotapi.ReplyKeyboardMarkup {
	rows := [][]tgbotapi.KeyboardButton{}
	for _, cfgRow := range a.cfg.Menu.Rows {
		row := []tgbotapi.KeyboardButton{}
		for _, item := range cfgRow {
			if !a.ShouldShowMenuItem(item) {
				continue
			}
			row = append(row, tgbotapi.NewKeyboardButton(a.RenderMenuText(item)))
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	keyboard := tgbotapi.NewReplyKeyboard(rows...)
	keyboard.ResizeKeyboard = true
	return keyboard
}

// ShouldShowMenuItem проверяет, нужно ли показывать пункт меню с учётом настроенных устройств.
func (a *App) ShouldShowMenuItem(item MenuItem) bool {
	switch item.Builtin {
	case "alarms":
		return a.hasAlarmDevices()
	case "batteries":
		return a.hasBatteryDevices()
	default:
		return true
	}
}

// hasAlarmDevices проверяет, есть ли в конфигурации датчики протечки/тревоги.
func (a *App) hasAlarmDevices() bool {
	for _, d := range a.cfg.Devices {
		if d.Type == "alarm" {
			return true
		}
	}
	return false
}

// hasBatteryDevices проверяет, есть ли устройства с батарейкой.
func (a *App) hasBatteryDevices() bool {
	for _, d := range a.cfg.Devices {
		if _, ok := d.Initial["battery"]; ok {
			return true
		}
		if _, ok := d.Fields["battery"]; ok {
			return true
		}
	}
	return false
}

// MenuButtonParts возвращает строки кнопки: для устройств две строки, для служебных кнопок одну.
func (a *App) MenuButtonParts(item MenuItem) []string {
	if item.Text != "" {
		return []string{item.Text}
	}
	label := item.Action
	if item.Action == "on" {
		label = a.cfg.Labels.On
	}
	if item.Action == "off" {
		label = a.cfg.Labels.Off
	}
	name := item.DeviceID
	if d, ok := a.devices[item.DeviceID]; ok {
		name = d.Name
	}
	if g, ok := a.groups[item.GroupID]; ok {
		name = g.Name
	}
	return []string{name, label}
}

// RenderMenuText формирует текст кнопки меню.
// Telegram на мобильных клиентах центрирует строки внутри кнопки самостоятельно.
// Ключ в menuIndex строится через normalizeTelegramText — совпадает с текстом от Telegram.
func (a *App) RenderMenuText(item MenuItem) string {
	return makeButtonText(a.MenuButtonParts(item))
}

// ExecuteMenuItem выполняет действие, связанное с пунктом Telegram-меню.
// userID — Telegram ID пользователя, нажавшего кнопку (используется для пользовательских настроек).
func (a *App) ExecuteMenuItem(item MenuItem, userID int64) string {
	if item.Builtin != "" {
		return a.RenderBuiltin(item.Builtin, userID)
	}
	if item.DeviceID != "" {
		return a.RunDeviceAction(item.DeviceID, item.Action, userID)
	}
	if item.GroupID != "" {
		return a.RunGroupAction(item.GroupID, item.Action, userID)
	}
	return a.cfg.Labels.Unknown
}

// RenderBuiltin выполняет встроенную команду меню: статус, тревоги или батарейки.
func (a *App) RenderBuiltin(name string, userID int64) string {
	switch name {
	case "status":
		return a.StatusText(userID)
	case "alarms":
		return a.AlarmsText()
	case "batteries":
		return a.BatteriesText()
	default:
		return a.cfg.Labels.Unknown
	}
}

// StatusText формирует HTML-текст со статусом устройств и видеомониторинга.
// Все значения, пришедшие из MQTT/state, экранируются: иначе символы <, > или &
// в payload ломают HTML-разметку, и Telegram отклоняет сообщение целиком.
func (a *App) StatusText(userID int64) string {
	var b strings.Builder
	b.WriteString("<pre>\n")

	if home := a.findDeviceWithState("home_mode"); home != nil {
		st := a.getDeviceState(home.ID)
		b.WriteString(fmt.Sprintf("%-18s%s\n", html.EscapeString(home.Name), a.onOffValue(st["state"])))
	}
	b.WriteString(fmt.Sprintf("%-18s%s\n\n", "Видеоконтроль", a.boolLabel(a.userVideoEnabled(userID))))

	for _, d := range a.cfg.Devices {
		if d.Type != "switch" {
			continue
		}
		st := a.getDeviceState(d.ID)
		if _, ok := st["state"]; !ok {
			continue
		}
		b.WriteString(fmt.Sprintf("%-18s%-18s%s\n", html.EscapeString(d.Name), formatUnix(st["last_seen"], a.cfg.Labels.DateTime), a.onOffValue(st["state"])))
	}

	if weather := a.findWeatherDevice(); weather != nil {
		st := a.getDeviceState(weather.ID)
		b.WriteString(fmt.Sprintf("\nНа %s:\n", formatUnix(st["last_seen"], "02.01.2006 15:04")))
		b.WriteString(fmt.Sprintf("Температура       %v°С\n", formatNumber(st["temperature"])))
		b.WriteString(fmt.Sprintf("Влажность         %v%%\n", formatNumber(st["humidity"])))
		b.WriteString(fmt.Sprintf("Давление          %vмм.рт.ст.", formatNumber(st["pressure"])))
	}

	b.WriteString("\n</pre>")
	return b.String()
}

// onOffValue выводит булево значение состояния как метку ВКЛ/ВЫКЛ, остальное — экранированным текстом.
func (a *App) onOffValue(v any) string {
	if x, ok := v.(bool); ok {
		return a.boolLabel(x)
	}
	return htmlValue(v)
}

// htmlValue безопасно превращает произвольное значение состояния в текст для HTML-режима Telegram.
func htmlValue(v any) string {
	if v == nil {
		return "-"
	}
	return html.EscapeString(fmt.Sprint(v))
}

// findDeviceWithState ищет устройство по ID и проверяет, что у него есть поле state.
func (a *App) findDeviceWithState(id string) *DeviceConfig {
	for i := range a.cfg.Devices {
		if a.cfg.Devices[i].ID == id {
			st := a.getDeviceState(id)
			if _, ok := st["state"]; ok {
				return &a.cfg.Devices[i]
			}
		}
	}
	return nil
}

// findWeatherDevice ищет первый сенсор, у которого есть температура, влажность и давление.
func (a *App) findWeatherDevice() *DeviceConfig {
	for i := range a.cfg.Devices {
		st := a.getDeviceState(a.cfg.Devices[i].ID)
		if _, ok := st["temperature"]; !ok {
			continue
		}
		if _, ok := st["humidity"]; !ok {
			continue
		}
		if _, ok := st["pressure"]; !ok {
			continue
		}
		return &a.cfg.Devices[i]
	}
	return nil
}

// formatNumber форматирует число без лишних нулей, сохраняя до двух знаков после запятой.
func formatNumber(v any) string {
	f := toFloat(v)
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// AlarmsText формирует HTML-текст со статусом датчиков тревоги/затопления.
func (a *App) AlarmsText() string {
	var b strings.Builder
	count := 0
	b.WriteString("<pre>\n")
	for _, d := range a.cfg.Devices {
		if d.Type == "alarm" {
			st := a.getDeviceState(d.ID)
			leak, known := toBool(st["water_leak"])
			label := a.cfg.Labels.No
			if leak {
				label = a.cfg.Labels.Yes
			}
			if !known {
				label = "НЕИЗВЕСТНО"
			}
			b.WriteString(fmt.Sprintf("%-32s%s\n", html.EscapeString(d.Name), html.EscapeString(label)))
			count++
		}
	}
	if count == 0 {
		b.WriteString("Датчики затопления не настроены\n")
	}
	b.WriteString("</pre>")
	logInfo("telegram builtin rendered: alarms count=%d", count)
	return b.String()
}

// BatteriesText формирует HTML-текст с уровнем батарей устройств.
func (a *App) BatteriesText() string {
	var b strings.Builder
	count := 0
	b.WriteString("<pre>\n")
	for _, d := range a.cfg.Devices {
		st := a.getDeviceState(d.ID)
		if battery, ok := st["battery"]; ok && battery != nil {
			b.WriteString(fmt.Sprintf("%-32s%-16s%s%%\n", html.EscapeString(d.Name), formatUnix(st["last_seen"], a.cfg.Labels.DateTime), htmlValue(battery)))
			count++
		}
	}
	if count == 0 {
		b.WriteString("Устройства с батарейками не настроены\n")
	}
	b.WriteString("</pre>")
	logInfo("telegram builtin rendered: batteries count=%d", count)
	return b.String()
}

// formatUnix форматирует Unix timestamp по указанному шаблону даты.
func formatUnix(v any, layout string) string {
	ts := int64(toFloat(v))
	if ts == 0 {
		return "-"
	}
	return time.Unix(ts, 0).Format(layout)
}

// Notify отправляет уведомление Telegram-пользователям по правилу события.
func (a *App) Notify(n *NotifyConfig, deviceID string, payload map[string]any, raw string, actionsOK bool) {
	a.notifyContext(n, map[string]any{"Device": a.devices[deviceID], "BaseTopic": a.cfg.MQTT.BaseTopics, "Payload": payload, "RawPayload": raw, "ActionsOK": actionsOK})
}

func (a *App) notifyContext(n *NotifyConfig, context map[string]any) {
	text := render(n.Text, context)
	for _, user := range a.cfg.Telegram.AllowedUsers {
		if n.Users == "video_enabled" && !a.userVideoEnabled(user.ID) {
			continue
		}
		select {
		case a.notifications <- tgbotapi.NewMessage(user.ID, text):
		default:
			logError("telegram notification queue full: user=%d", user.ID)
		}
	}
}

// userVideoEnabled возвращает, включён ли видеомониторинг у пользователя.
func (a *App) userVideoEnabled(id int64) bool {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.state.Users[fmt.Sprint(id)].VideoMonitoringEnabled
}

// setUserVideo включает или выключает видеомониторинг для конкретного Telegram-пользователя.
func (a *App) setUserVideo(userID int64, enabled bool) {
	a.stateMu.Lock()
	a.state.Users[fmt.Sprint(userID)] = UserState{VideoMonitoringEnabled: enabled}
	a.stateMu.Unlock()
	a.SaveState()
}

// Switch all currently allowed recipients together, then persist once.
// Historical state entries for removed users are not changed.
func (a *App) setAllUsersVideo(enabled bool) {
	a.stateMu.Lock()
	for _, user := range a.cfg.Telegram.AllowedUsers {
		state := a.state.Users[fmt.Sprint(user.ID)]
		state.VideoMonitoringEnabled = enabled
		a.state.Users[fmt.Sprint(user.ID)] = state
	}
	a.stateMu.Unlock()
	a.SaveState()
}

// boolLabel переводит bool-значение в пользовательскую метку ВКЛ/ВЫКЛ из конфигурации (HTML-безопасно).
func (a *App) boolLabel(v bool) string {
	if v {
		return html.EscapeString(a.cfg.Labels.On)
	}
	return html.EscapeString(a.cfg.Labels.Off)
}

// Telegram не задерживает закрытие кранов и обработку следующей протечки.
func (a *App) RunNotifications() {
	for message := range a.notifications {
		if _, err := a.bot.Send(message); err != nil {
			logError("telegram notification send failed: user=%d error=%v", message.ChatID, a.telegramError(err))
		}
	}
}

// notifyAllUsers ставит служебное сообщение в очередь для всех разрешённых пользователей.
func (a *App) notifyAllUsers(text string) {
	for _, user := range a.cfg.Telegram.AllowedUsers {
		select {
		case a.notifications <- tgbotapi.NewMessage(user.ID, text):
		default:
			logError("telegram notification queue full: user=%d", user.ID)
		}
	}
}
