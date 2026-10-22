package main

import (
	"strings"
	"testing"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/ble"
)

func TestManagementRecoveryRequiresExpectedIdentitySlotAndVersion(t *testing.T) {
	before := []byte(`{"loaded_app":0,"loaded":0,"version":9,"ready":true,"apps":[{"id":"ble-echo","bank":2,"active":0,"previous":-1,"pending":1,"versions":[1,2]}]}`)
	plan, err := planManagement(ble.OpActivate, "ble-echo", before)
	if err != nil {
		t.Fatal(err)
	}
	after := `{"loaded_app":2,"loaded":1,"version":2,"ready":true,"apps":[{"id":"ble-echo","bank":2,"active":1,"previous":0,"pending":-1,"versions":[1,2]}]}`
	if !plan.reached([]byte(after)) {
		t.Fatal("confirmed outcome not recognized")
	}
	for _, bad := range []string{string(before), `{}`, `{`, strings.Replace(after, `"ready":true`, `"ready":false`, 1), strings.Replace(after, `"version":2`, `"version":3`, 1), strings.Replace(after, `"loaded_app":2`, `"loaded_app":0`, 1), strings.Replace(after, `"versions":[1,2]`, `"versions":[1,3]`, 1), strings.Replace(after, `"bank":2`, `"bank":3`, 1)} {
		if plan.reached([]byte(bad)) {
			t.Fatal("unproven management outcome accepted")
		}
	}
}
func TestManagementRecoveryDoesNotConfuseRemovalSwitchAndRollback(t *testing.T) {
	status := []byte(`{"loaded_app":2,"loaded":1,"version":2,"ready":true,"apps":[{"id":"ble-echo","bank":2,"active":1,"previous":0,"pending":-1,"versions":[1,2]}]}`)
	remove, err := planManagement(ble.OpRemove, "ble-echo", status)
	if err != nil || remove.reached(status) || remove.reached([]byte(`{}`)) || !remove.reached([]byte(`{"apps":[]}`)) {
		t.Fatal("removal proof", err)
	}
	rollback, err := planManagement(ble.OpRollback, "ble-echo", status)
	if err != nil || rollback.reached(status) {
		t.Fatal("rollback confused with current slot", err)
	}
	switchPlan, err := planManagement(ble.OpSwitch, "ble-echo", status)
	if err != nil || !switchPlan.reached(status) {
		t.Fatal("running switch target", err)
	}
	if _, err := planManagement(ble.OpSwitch, "missing", status); err == nil {
		t.Fatal("missing application accepted")
	}
}
