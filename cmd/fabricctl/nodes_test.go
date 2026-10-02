package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n0remac/Fabric/internal/firmware"
	"github.com/n0remac/Fabric/internal/nodes"
)

func TestNodeCLIProvisionAndRevoke(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "nodes.json")
	output := filepath.Join(dir, "x3.token")
	args := []string{"create", "--config", config, "--id", "x3-pocket", "--type", "xteink-x3", "--name", "Pocket Reader", "--output", output, "--permissions", "pages.read,actions.invoke,firmware.read"}
	if err := runNode(args); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token mode %o", info.Mode().Perm())
	}
	value, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	r, err := nodes.Open(config, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Authenticate(strings.TrimSpace(string(value))); !ok {
		t.Fatal("issued token not registered")
	}
	if err := runNode(args); err == nil {
		t.Fatal("existing token file overwritten")
	}
	if err := runNode([]string{"disable", "--config", config, "x3-pocket"}); err != nil {
		t.Fatal(err)
	}
	r.Reload()
	if _, ok := r.Authenticate(strings.TrimSpace(string(value))); ok {
		t.Fatal("disabled token works")
	}
	if err := runNode([]string{"revoke", "--config", config, "x3-pocket"}); err != nil {
		t.Fatal(err)
	}
	r.Reload()
	if _, ok := r.Authenticate(strings.TrimSpace(string(value))); ok {
		t.Fatal("revoked token works")
	}
}

func TestImportExistingFirmwareCredential(t *testing.T) {
	dir := t.TempDir()
	access := filepath.Join(dir, "firmware-access.json")
	tokenFile := filepath.Join(dir, "x3.token")
	config := filepath.Join(dir, "nodes.json")
	if err := firmware.EditCredentials(access, "x3-personal", "reader", tokenFile, false); err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := runNode([]string{"import-firmware", "--config", config, "--firmware-access", access, "--credential-id", "x3-personal", "--id", "x3-pocket", "--type", "xteink-x3", "--name", "Pocket Reader", "--firmware-channel", "dev"}); err != nil {
		t.Fatal(err)
	}
	r, err := nodes.Open(config, false)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := r.Authenticate(strings.TrimSpace(string(token)))
	if !ok || n.ID != "x3-pocket" || !nodes.Has(n, "firmware.read") || !nodes.Has(n, "pages.read") {
		t.Fatalf("imported token did not resolve to node: %+v", n)
	}
	if n.Attributes["firmware_channel"] != "dev" {
		t.Fatal(n.Attributes)
	}
}
