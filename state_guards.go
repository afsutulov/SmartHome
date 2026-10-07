package main

import "fmt"

// Defaults are not evidence that a protective sensor is dry. After loading
// state, each configured guard must be backed by a saved value of the right
// type or a subsequent explicit report. Protected by stateMu.
func (a *App) initGuardTypes() {
	a.guardTypes = map[string]map[string]any{}
	add := func(act ActionConfig) {
		for _, g := range act.BlockWhen {
			if a.guardTypes[g.DeviceID] == nil {
				a.guardTypes[g.DeviceID] = map[string]any{}
			}
			a.guardTypes[g.DeviceID][g.Field] = g.Value
		}
	}
	for _, d := range a.cfg.Devices {
		for _, act := range d.Actions {
			add(act)
		}
	}
	for _, g := range a.cfg.Groups {
		for _, act := range g.Actions {
			add(act)
		}
	}
}

func (a *App) invalidateGuardKnowledge() {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.unknownGuards = map[string]map[string]any{}
	for id, fields := range a.guardTypes {
		a.unknownGuards[id] = cloneMap(fields)
		for field := range fields {
			delete(a.state.Devices[id], field)
		}
	}
}

func sameGuardType(value, want any) bool {
	switch want.(type) {
	case bool:
		_, ok := value.(bool)
		return ok
	case string:
		_, ok := value.(string)
		return ok
	case float64:
		_, ok := value.(float64)
		return ok
	}
	return false
}

// Caller holds stateMu. An invalid value must not clear an unknown guard.
func (a *App) acceptGuardValue(id, field string, value any) {
	if want, guarded := a.guardTypes[id][field]; guarded {
		if sameGuardType(value, want) {
			delete(a.unknownGuards[id], field)
		} else {
			logWarn("protective state has invalid type; guard remains blocked: device=%s field=%s got=%T expected=%T", id, field, value, want)
			if a.unknownGuards == nil {
				a.unknownGuards = map[string]map[string]any{}
			}
			if a.unknownGuards[id] == nil {
				a.unknownGuards[id] = map[string]any{}
			}
			a.unknownGuards[id][field] = want
		}
	}
}

func (a *App) guardBlockReason(target string, guard StateCondition) string {
	a.stateMu.RLock()
	_, unknown := a.unknownGuards[guard.DeviceID][guard.Field]
	got := a.state.Devices[guard.DeviceID][guard.Field]
	a.stateMu.RUnlock()
	if unknown {
		name := guard.DeviceID
		if d, ok := a.devices[name]; ok {
			name = d.Name
		}
		return fmt.Sprintf("%s (состояние датчика неизвестно: %s, %s)", target, name, guard.Field)
	}
	if valuesEqual(got, guard.Value) {
		return a.guardReason(target, guard)
	}
	return ""
}
