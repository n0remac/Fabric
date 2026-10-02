package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/n0remac/Fabric/internal/firmware"
	"github.com/n0remac/Fabric/internal/nodes"
)

func commaList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	var out []string
	for _, v := range strings.Split(value, ",") {
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

func runNode(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: fabricctl node create|import-firmware|list|show|revoke|disable|enable")
	}
	command := args[0]
	flags := flag.NewFlagSet("node "+command, flag.ContinueOnError)
	config := flags.String("config", "/etc/fabric/nodes.json", "Node registry file")
	id := flags.String("id", "", "Stable node ID")
	nodeType := flags.String("type", "", "Node type")
	name := flags.String("name", "", "Display name")
	output := flags.String("output", "", "Private token output file")
	permissions := flags.String("permissions", "pages.read,actions.invoke", "Comma-separated permissions")
	provides := flags.String("provides", "", "Comma-separated provided capabilities")
	channel := flags.String("firmware-channel", "", "Firmware channel")
	accessPath := flags.String("firmware-access", "/etc/fabric/firmware-access.json", "Existing firmware access file")
	credentialID := flags.String("credential-id", "", "Existing firmware credential ID")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	positionals := flags.Args()
	if *id == "" && len(positionals) == 1 {
		*id = positionals[0]
		positionals = nil
	}
	if len(positionals) != 0 {
		return errors.New("unexpected positional arguments")
	}
	create := command == "create" || command == "import-firmware"
	r, err := nodes.Open(*config, create)
	if err != nil {
		return err
	}
	switch command {
	case "list":
		return json.NewEncoder(os.Stdout).Encode(r.List())
	case "show":
		n, ok := r.Get(*id)
		if !ok {
			return nodes.ErrNotFound
		}
		return json.NewEncoder(os.Stdout).Encode(n)
	case "revoke":
		return r.Revoke(*id)
	case "disable":
		return r.SetEnabled(*id, false)
	case "enable":
		return r.SetEnabled(*id, true)
	case "create", "import-firmware":
		if *id == "" || *nodeType == "" || *name == "" {
			return errors.New("--id, --type and --name are required")
		}
		attrs := map[string]string{}
		if *channel != "" {
			if *channel != "dev" && *channel != "stable" {
				return errors.New("firmware channel must be dev or stable")
			}
			attrs["firmware_channel"] = *channel
		}
		n := nodes.Node{ID: *id, Type: *nodeType, Name: *name, Enabled: true, Permissions: commaList(*permissions), Provides: commaList(*provides), Attributes: attrs}
		if command == "import-firmware" {
			if *credentialID == "" {
				return errors.New("--credential-id is required")
			}
			access, err := firmware.LoadAccess(*accessPath)
			if err != nil {
				return err
			}
			for _, c := range access.Credentials {
				if c.ID == *credentialID {
					if c.Role == "publisher" && *permissions == "pages.read,actions.invoke" {
						n.Permissions = []string{"pages.read", "actions.invoke", "firmware.read", "firmware.publish"}
					} else if c.Role == "reader" && *permissions == "pages.read,actions.invoke" {
						n.Permissions = []string{"pages.read", "actions.invoke", "firmware.read"}
					}
					if err := r.Add(n, c.SHA256); err != nil {
						return err
					}
					fmt.Printf("Imported node %s; changes apply on the next request.\n", n.ID)
					return nil
				}
			}
			return errors.New("firmware credential ID not found")
		}
		if *output == "" {
			return errors.New("--output is required")
		}
		token, digest, err := nodes.RandomToken()
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if _, err = f.WriteString(token + "\n"); err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(*output)
			return err
		}
		if err := r.Add(n, digest); err != nil {
			os.Remove(*output)
			return err
		}
		fmt.Printf("Created node %s; token written to %s. Changes apply on the next request.\n", n.ID, *output)
		return nil
	default:
		return errors.New("unknown node command")
	}
}
