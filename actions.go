package main

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"sync"
)

// actionTrace собирает причины блокировок внутри цепочки действий, чтобы
// пользователь увидел «заблокировано из-за протечки», а не «ошибка MQTT».
type actionTrace struct {
	mu      sync.Mutex
	blocked []string
	failed  bool
}

func (t *actionTrace) addBlocked(reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.blocked = append(t.blocked, reason)
	t.mu.Unlock()
}

func (t *actionTrace) blockedReasons() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.blocked...)
}

func (t *actionTrace) merge(child *actionTrace) {
	for _, reason := range child.blockedReasons() {
		t.addBlocked(reason)
	}
	if child.hasFailure() {
		t.addFailure()
	}
}

func (t *actionTrace) addFailure() {
	if t != nil {
		t.mu.Lock()
		t.failed = true
		t.mu.Unlock()
	}
}
func (t *actionTrace) hasFailure() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failed
}

// chainFailureText формирует итог цепочки: отдельно перечисляет заблокированные
// действия (это защитная функция, а не сбой связи) и общие ошибки отправки.
func chainFailureText(name string, child *actionTrace) string {
	reasons := child.blockedReasons()
	if len(reasons) == 0 {
		return fmt.Sprintf("%s: ошибка отправки MQTT-команды; проверьте журнал", html.EscapeString(name))
	}
	text := fmt.Sprintf("%s: выполнено не полностью.\nЗаблокировано: %s", html.EscapeString(name), html.EscapeString(strings.Join(reasons, "; ")))
	if child.hasFailure() {
		text += "\nТакже ошибка отправки MQTT-команды; проверьте журнал"
	}
	return text
}

// RunDeviceAction находит устройство и действие по ID и запускает это действие.
func (a *App) RunDeviceAction(deviceID, actionName string, chatID int64) string {
	return a.runDeviceActionInternal(deviceID, actionName, chatID, "", false)
}

// RunDeviceActionFromIntegration запускает действие устройства по команде внешней интеграции
// (например, Яндекс Алисы). В этом случае пропускаем публикации в топики данной интеграции,
// чтобы не создавать петлю: интеграция -> action -> publish -> интеграция -> ...
func (a *App) RunDeviceActionFromIntegration(deviceID, actionName, integrationID string, force bool) string {
	return a.runDeviceActionInternal(deviceID, actionName, 0, integrationID, force)
}

// runDeviceActionInternal — внутренняя реализация запуска действия устройства.
func (a *App) runDeviceActionInternal(deviceID, actionName string, chatID int64, skipIntegrationID string, force bool) string {
	text, _ := a.executeDeviceAction(deviceID, actionName, chatID, skipIntegrationID, force)
	return text
}

func (a *App) executeDeviceAction(deviceID, actionName string, chatID int64, skipIntegrationID string, force bool) (string, bool) {
	return a.dispatchCompound(deviceID+"."+actionName, true, func() (string, bool) {
		return a.executeDeviceActionTraced(deviceID, actionName, chatID, skipIntegrationID, force, nil)
	})
}

func (a *App) executeDeviceActionTraced(deviceID, actionName string, chatID int64, skipIntegrationID string, force bool, tr *actionTrace) (string, bool) {
	d, ok := a.devices[deviceID]
	if !ok {
		logWarn("unknown device action: %s.%s", deviceID, actionName)
		return "Устройство не найдено", false
	}
	act, ok := d.Actions[actionName]
	if !ok {
		logWarn("unknown device action: %s.%s", deviceID, actionName)
		return "Действие не найдено", false
	}
	return a.runAction(d, act, chatID, skipIntegrationID, force, tr)
}

// RunGroupAction выполняет действие группы или применяет одноимённое действие ко всем участникам группы.
func (a *App) RunGroupAction(groupID, actionName string, chatID int64) string {
	text, _ := a.dispatchCompound(groupID+"."+actionName, true, func() (string, bool) {
		return a.executeGroupActionTraced(groupID, actionName, chatID, nil)
	})
	return text
}

func (a *App) executeGroupActionTraced(groupID, actionName string, chatID int64, tr *actionTrace) (string, bool) {
	g, ok := a.groups[groupID]
	if !ok {
		return "Группа не найдена", false
	}
	if act, ok := g.Actions[actionName]; ok {
		return a.runGroupAction(g, act, chatID, tr)
	}
	child := &actionTrace{}
	delivered := true
	for _, id := range g.Members {
		if _, ok := a.executeDeviceActionTraced(id, actionName, chatID, "", false, child); !ok {
			delivered = false
		}
	}
	tr.merge(child)
	if !delivered {
		return chainFailureText(g.Name, child), false
	}
	return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(g.Name), html.EscapeString(actionName)), delivered
}

// runAction выполняет конкретное действие устройства: публикации MQTT, изменение состояния и цепочки действий.
// skipIntegrationID: если непустой, пропускаем публикации в топики данной command integration
// (защита от петли при командах от внешних интеграций типа Яндекс Алисы).
//
// Для одиночного действия сначала обновляем state, потом публикуем в MQTT.
// Состояние действия с цепочкой фиксируется только после успешной цепочки.
// Это критично для защиты от петли: когда мы публикуем в /yandex/DeviceID,
// брокер немедленно эхо-отправляет нам наше же сообщение (мы подписаны на этот топик).
// Если state уже обновлён ДО публикации — state guard (deviceStateEquals) заблокирует
// повторное выполнение действия. Если обновить state ПОСЛЕ — guard не сработает,
// потому что к моменту эхо state ещё хранит старое значение.
func (a *App) runAction(d DeviceConfig, act ActionConfig, chatID int64, skipIntegrationID string, force bool, tr *actionTrace) (string, bool) {
	r := a.runtime[d.ID]
	r.mu.Lock()
	text, executeNext, delivered, version := a.runActionLocked(d, act, chatID, skipIntegrationID, force, tr)
	r.mu.Unlock()
	child := &actionTrace{}
	if executeNext {
		if !delivered {
			child.addFailure()
		}
		for _, next := range act.RunActions {
			if !a.runNamedActionTraced(next, chatID, child) {
				delivered = false
			}
		}
	}
	tr.merge(child)
	if executeNext && delivered && act.SetState != nil && len(act.RunActions) > 0 {
		// Состояние цепочки фиксируется только после всех дочерних действий.
		// Перезапуск посередине цепочки не оставит преждевременное «уже выполнено».
		a.stateMu.Lock()
		if a.stateVersions[d.ID] == version {
			a.state.Devices[d.ID]["state"] = *act.SetState
			a.stateVersions[d.ID]++
		}
		a.stateMu.Unlock()
		a.SaveState()
	}
	if executeNext && !delivered {
		text = chainFailureText(d.Name, child)
	}
	// Notify only an executed action, after the whole chain has finished.
	// Enqueueing Telegram messages does not wait for Telegram or hold MQTT up.
	if executeNext && act.Notify != nil {
		a.Notify(act.Notify, d.ID, nil, "", delivered)
	}
	return text, delivered
}

// Не отменять новое действие или подтверждение, пришедшее во время цепочки.
func (a *App) rollbackActionState(id string, previous map[string]any, version uint64) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.stateVersions[id] != version {
		return
	}
	if value, ok := previous["state"]; ok {
		a.state.Devices[id]["state"] = value
	} else {
		delete(a.state.Devices[id], "state")
	}
	a.stateVersions[id]++
}

// Вызывается под mutex устройства; программного таймера выключения нет.
func (a *App) runActionLocked(d DeviceConfig, act ActionConfig, chatID int64, skipIntegrationID string, force bool, tr *actionTrace) (string, bool, bool, uint64) {
	for _, guard := range act.BlockWhen {
		if reason := a.guardBlockReason(d.Name, guard); reason != "" {
			logWarn("device action blocked by state guard: device=%s guard=%s.%s reason=%s", d.ID, guard.DeviceID, guard.Field, reason)
			tr.addBlocked(reason)
			return "Действие заблокировано: " + html.EscapeString(reason), false, false, 0
		}
	}
	if act.SetUserVideo != nil && (chatID != 0 || act.VideoUserID != 0) {
		videoUserID := chatID
		if act.VideoUserID != 0 {
			videoUserID = act.VideoUserID
		}
		a.setUserVideo(videoUserID, *act.SetUserVideo)
		return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(d.Name), a.boolLabel(*act.SetUserVideo)), false, true, 0
	}
	if !force && !act.Force && act.SetState != nil && a.deviceStateEquals(d.ID, "state", *act.SetState) {
		return fmt.Sprintf("%s уже <b>%s</b>", html.EscapeString(d.Name), a.boolLabel(*act.SetState)), false, true, 0
	}
	if act.SetAllUsersVideo != nil {
		a.setAllUsersVideo(*act.SetAllUsersVideo)
	}
	logInfo("device action: device=%s name=%q action_title=%q", d.ID, d.Name, act.Title)
	previous := a.getDeviceState(d.ID)
	a.stateMu.RLock()
	version := a.stateVersions[d.ID]
	a.stateMu.RUnlock()
	if act.SetState != nil && len(act.RunActions) == 0 {
		version = a.SetDeviceValue(d.ID, "state", *act.SetState)
	}
	delivered := true
	for _, p := range act.Publishes {
		topic := a.RenderWithDevice(p.Topic, d, nil)
		if skipIntegrationID != "" && a.isIntegrationTopic(skipIntegrationID, topic, d) {
			continue
		}
		if !a.publishConfigured(p, map[string]any{"Device": d, "BaseTopic": a.cfg.MQTT.BaseTopics, "Payload": map[string]any{}}) {
			delivered = false
		}
	}
	if !delivered && act.SetState != nil && len(act.RunActions) == 0 {
		a.rollbackActionState(d.ID, previous, version)
	}

	a.SaveState()
	if !delivered {
		return fmt.Sprintf("%s: ошибка отправки MQTT-команды; проверьте журнал", html.EscapeString(d.Name)), true, false, version
	}
	if act.Title != "" {
		return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(d.Name), html.EscapeString(act.Title)), true, true, version
	}
	if act.SetState != nil {
		return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(d.Name), a.boolLabel(*act.SetState)), true, true, version
	}
	return fmt.Sprintf("%s: выполнено", html.EscapeString(d.Name)), true, true, version
}

// runGroupAction выполняет действие группы: вложенные действия, MQTT-публикации и сохранение состояния.
func (a *App) runGroupAction(g GroupConfig, act ActionConfig, chatID int64, tr *actionTrace) (string, bool) {
	for _, guard := range act.BlockWhen {
		if reason := a.guardBlockReason(g.Name, guard); reason != "" {
			logWarn("group action blocked by state guard: group=%s guard=%s.%s reason=%s", g.ID, guard.DeviceID, guard.Field, reason)
			tr.addBlocked(reason)
			return "Действие заблокировано: " + html.EscapeString(reason), false
		}
	}
	child := &actionTrace{}
	if act.SetAllUsersVideo != nil {
		a.setAllUsersVideo(*act.SetAllUsersVideo)
	}
	delivered := true
	for _, next := range act.RunActions {
		if !a.runNamedActionTraced(next, chatID, child) {
			delivered = false
		}
	}
	ctx := map[string]any{"Group": g, "BaseTopic": a.cfg.MQTT.BaseTopics}
	for _, p := range act.Publishes {
		if !a.publishConfigured(p, ctx) {
			delivered = false
			child.addFailure()
		}
	}
	tr.merge(child)
	a.SaveState()
	if act.Notify != nil {
		a.notifyContext(act.Notify, map[string]any{"Group": g, "BaseTopic": a.cfg.MQTT.BaseTopics, "ActionsOK": delivered})
	}
	if !delivered {
		return chainFailureText(g.Name, child), false
	}
	return fmt.Sprintf("%s: <b>%s</b>", html.EscapeString(g.Name), html.EscapeString(act.Title)), true
}

// Возвращает успешность отправки всех команд, продолжая цепочку даже после ошибки одной команды.
func (a *App) RunNamedAction(ref string, chatID int64) bool {
	_, ok := a.dispatchCompound(ref, true, func() (string, bool) {
		return "", a.runNamedActionTraced(ref, chatID, nil)
	})
	return ok
}

func (a *App) runNamedActionTraced(ref string, chatID int64, tr *actionTrace) bool {
	parts := strings.SplitN(ref, ".", 2)
	if len(parts) != 2 {
		return false
	}
	if _, ok := a.devices[parts[0]]; ok {
		_, delivered := a.executeDeviceActionTraced(parts[0], parts[1], chatID, "", false, tr)
		return delivered
	}
	if _, ok := a.groups[parts[0]]; ok {
		_, delivered := a.executeGroupActionTraced(parts[0], parts[1], chatID, tr)
		return delivered
	}
	return false
}

// guardReason описывает сработавшее условие block_when человеческим текстом (без HTML).
func (a *App) guardReason(target string, guard StateCondition) string {
	sensor := guard.DeviceID
	if d, ok := a.devices[guard.DeviceID]; ok && d.Name != "" {
		sensor = d.Name
	}
	if guard.Field == "water_leak" {
		return fmt.Sprintf("%s (протечка: %s)", target, sensor)
	}
	return fmt.Sprintf("%s (%s: %s = %v)", target, sensor, guard.Field, guard.Value)
}

// isIntegrationTopic проверяет, принадлежит ли данный topic к топикам command integration.
// Используется для предотвращения петли при обработке команд от внешних интеграций.
func (a *App) isIntegrationTopic(integrationID, topic string, d DeviceConfig) bool {
	for _, ci := range a.cfg.CommandIntegrations {
		if ci.ID != integrationID || !ci.Enabled {
			continue
		}
		for _, topicTpl := range commandIntegrationTopics(ci) {
			rendered := a.RenderWithDevice(topicTpl, d, nil)
			if sameMQTTTopic(topic, rendered) {
				return true
			}
		}
	}
	return false
}

// SetDeviceValue обновляет поле состояния. last_seen меняется только при MQTT-отчёте.
func (a *App) SetDeviceValue(id, field string, value any) uint64 {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if _, ok := a.state.Devices[id]; !ok {
		a.state.Devices[id] = map[string]any{}
	}
	old := a.state.Devices[id][field]
	a.state.Devices[id][field] = value
	a.acceptGuardValue(id, field, value)
	a.stateVersions[id]++

	if !valuesEqual(old, value) {
		if isLoggableStateField(field) {
			logInfo("state changed: device=%s field=%s old=%v new=%v", id, field, old, value)
		} else {
			logDebug("state changed: device=%s field=%s old=%v new=%v", id, field, old, value)
		}
	}
	return a.stateVersions[id]
}

// isLoggableStateField определяет, какие поля состояния писать в info-лог.
func isLoggableStateField(field string) bool {
	switch field {
	case "state", "water_leak":
		return true
	default:
		return false
	}
}

// getDeviceState возвращает копию текущего состояния устройства.
func (a *App) getDeviceState(id string) map[string]any {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	st := cloneMap(a.state.Devices[id])
	for field := range a.unknownGuards[id] {
		delete(st, field)
	}
	return st
}

// deviceStateEquals проверяет, совпадает ли текущее поле состояния с ожидаемым значением.
func (a *App) deviceStateEquals(id, field string, want any) bool {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	got, ok := a.state.Devices[id][field]
	if !ok {
		return false
	}
	return valuesEqual(got, want)
}

// valuesEqual сравнивает значения из JSON/Go с учётом bool, чисел и строк.
func valuesEqual(a any, b any) bool {
	if ab, ok := toBool(a); ok {
		if bb, ok := toBool(b); ok {
			return ab == bb
		}
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// toBool безопасно приводит bool/string к bool для сравнения состояний.
func toBool(v any) (bool, bool) {
	switch x := v.(type) {
	case float64:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	case int:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	case int64:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	case json.Number:
		if x == "0" || x == "1" {
			return x == "1", true
		}
	case bool:
		return x, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "on", "1", "вкл":
			return true, true
		case "false", "off", "0", "выкл":
			return false, true
		}
	}
	return false, false
}
