package main

import (
	"sort"
	"strings"
	"sync"
)

// Lock only owners of compound actions, in one stable order. Acquire once at
// the public entry point; recursive actions reuse the locks. Physical leaf
// devices retain their short per-device locks, so a home-mode chain does not
// reserve the valves while it waits on unrelated devices.
func (a *App) lockActionChains(ref string) func() {
	return a.lockOwners(a.chainOwners(ref))
}

// chainOwners возвращает отсортированный список владельцев составных действий,
// которые затрагивает ref (включая вложенные цепочки).
func (a *App) chainOwners(ref string) []string {
	owners, visited := map[string]bool{}, map[string]bool{}
	var visit func(string)
	visit = func(ref string) {
		if visited[ref] {
			return
		}
		visited[ref] = true
		parts := strings.SplitN(ref, ".", 2)
		if len(parts) != 2 {
			return
		}
		id, name := parts[0], parts[1]
		if a.chainLocks[id] != nil {
			owners[id] = true
		}
		if d, ok := a.devices[id]; ok {
			for _, child := range d.Actions[name].RunActions {
				visit(child)
			}
		} else if g, ok := a.groups[id]; ok {
			if act, exists := g.Actions[name]; exists {
				for _, child := range act.RunActions {
					visit(child)
				}
			} else {
				for _, member := range g.Members {
					visit(member + "." + name)
				}
			}
		}
	}
	visit(ref)
	ids := make([]string, 0, len(owners))
	for id := range owners {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (a *App) lockOwners(ids []string) func() {
	for _, id := range ids {
		a.chainLocks[id].Lock()
	}
	return func() {
		for i := len(ids) - 1; i >= 0; i-- {
			a.chainLocks[ids[i]].Unlock()
		}
	}
}

// One bounded FIFO admits compound commands from all entry points. MQTT
// submits without waiting; interactive callers wait for their own result.
// Emergency rules bypass this queue and retain synchronous result handling.
type actionResult struct {
	text string
	ok   bool
}

type deferredAction struct {
	ref    string
	owners []string
	run    func() (string, bool)
	result chan actionResult
}

func (a *App) dispatchCompound(ref string, wait bool, run func() (string, bool)) (string, bool) {
	owners := a.chainOwners(ref)
	if len(owners) == 0 {
		return run()
	}
	job := deferredAction{ref: ref, owners: owners, run: run}
	if wait {
		job.result = make(chan actionResult, 1)
	}
	a.deferMu.Lock()
	select {
	case a.deferredActions <- job:
		for _, id := range owners {
			a.deferredPending[id]++
		}
		a.deferMu.Unlock()
		a.deferWorkerOnce.Do(func() { go a.runDeferredActions() })
		if !wait {
			return "", true
		}
		result := <-job.result
		return result.text, result.ok
	default:
		a.deferMu.Unlock()
		logError("compound command queue full; command rejected: action=%s capacity=%d", ref, cap(a.deferredActions))
		return "Очередь команд заполнена; команда не принята. Повторите позже", false
	}
}

// Deferrable compound events always leave the MQTT receive worker, even when
// their owner is currently idle. Waiting on PUBACK is also a blocking operation.
func (a *App) runEventAction(ref string, deferrable bool) bool {
	run := func() (string, bool) { return "", a.runNamedActionTraced(ref, 0, nil) }
	if deferrable {
		_, ok := a.dispatchCompound(ref, false, run)
		return ok
	}
	unlock := a.lockActionChains(ref)
	defer unlock()
	_, ok := run()
	return ok
}

func (a *App) runDeferredActions() {
	for job := range a.deferredActions {
		unlock := a.lockOwners(job.owners)
		text, ok := job.run()
		unlock()
		a.deferMu.Lock()
		for _, id := range job.owners {
			a.deferredPending[id]--
		}
		a.deferMu.Unlock()
		if job.result != nil {
			job.result <- actionResult{text: text, ok: ok}
		}
		if !ok {
			logError("queued compound action failed: action=%s", job.ref)
		}
	}
}

func (a *App) initChainLocks() {
	a.deferredPending = map[string]int{}
	a.deferredActions = make(chan deferredAction, 64)
	a.chainLocks = map[string]*sync.Mutex{}
	for _, d := range a.cfg.Devices {
		for _, act := range d.Actions {
			if len(act.RunActions) > 0 {
				a.chainLocks[d.ID] = &sync.Mutex{}
				break
			}
		}
	}
	for _, g := range a.cfg.Groups {
		a.chainLocks[g.ID] = &sync.Mutex{}
	}
}
