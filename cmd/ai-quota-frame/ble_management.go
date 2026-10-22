package main

import (
	"encoding/json"
	"errors"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
)

type managementState struct {
	LoadedApp int    `json:"loaded_app"`
	Loaded    int    `json:"loaded"`
	Version   uint32 `json:"version"`
	Ready     bool   `json:"ready"`
	Apps      []struct {
		ID       string    `json:"id"`
		Bank     int       `json:"bank"`
		Active   int       `json:"active"`
		Previous int       `json:"previous"`
		Pending  int       `json:"pending"`
		Versions []*uint32 `json:"versions"`
	} `json:"apps"`
}

// Preserve the expected state BEFORE sending a mutation. A database change can
// invalidate Windows GATT objects before the response arrives. A fresh signed
// status can establish the outcome without ever resending the mutation.
type managementPlan struct {
	app        string
	remove     bool
	bank, slot int
	version    uint32
}

func planManagement(op byte, app string, raw []byte) (managementPlan, error) {
	var s managementState
	if err := json.Unmarshal(raw, &s); err != nil {
		return managementPlan{}, errors.New("invalid management status")
	}
	for _, a := range s.Apps {
		if a.ID != app {
			continue
		}
		p := managementPlan{app: app, bank: a.Bank, slot: a.Active, remove: op == ble.OpRemove}
		if p.remove {
			return p, nil
		}
		if op == ble.OpActivate && a.Pending >= 0 {
			p.slot = a.Pending
		}
		if op == ble.OpRollback {
			p.slot = a.Previous
		}
		if p.slot < 0 || p.slot >= len(a.Versions) || a.Versions[p.slot] == nil {
			return managementPlan{}, errors.New("requested application slot is unavailable")
		}
		p.version = *a.Versions[p.slot]
		return p, nil
	}
	return managementPlan{}, errors.New("application is not installed")
}
func (p managementPlan) reached(raw []byte) bool {
	var s managementState
	if json.Unmarshal(raw, &s) != nil || s.Apps == nil {
		return false
	}
	for _, a := range s.Apps {
		if a.ID != p.app {
			continue
		}
		if p.remove {
			return false
		}
		return a.Bank == p.bank && p.slot >= 0 && p.slot < len(a.Versions) && a.Versions[p.slot] != nil &&
			*a.Versions[p.slot] == p.version && s.LoadedApp == p.bank && s.Loaded == p.slot && s.Version == p.version && s.Ready
	}
	return p.remove
}
