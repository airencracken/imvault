package main

import (
	"reflect"
	"testing"
)

func TestWithDataDirEnvironmentReplacesOnlyTheConfiguredKey(t *testing.T) {
	got := withDataDirEnvironment([]string{"PATH=/bin", "IMVAULT_DATA_DIR=/wrong", "HOME=/root"}, "IMVAULT_DATA_DIR", "/var/lib/imvault")
	want := []string{"PATH=/bin", "HOME=/root", "IMVAULT_DATA_DIR=/var/lib/imvault"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
}

func TestProvisioningHelpDoesNotReexecuteAsServiceUser(t *testing.T) {
	for _, args := range [][]string{{"create-admin", "--help"}, {"create-admin", "-h"}} {
		handled, status, err := reexecProvisioningAsService(args)
		if handled || status != 0 || err != nil {
			t.Fatalf("reexec for %v = handled %t status %d err %v", args, handled, status, err)
		}
	}
}
