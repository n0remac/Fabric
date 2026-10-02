package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/n0remac/Fabric/internal/firmware"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: fabricctl token-issue|token-revoke|upload|promote [options]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	if command == "token-issue" || command == "token-revoke" {
		config := flags.String("config", "/etc/fabric/firmware-access.json", "Credential configuration")
		id := flags.String("id", "", "Credential identity")
		role := flags.String("role", "reader", "reader or publisher")
		output := flags.String("output", "", "New private token file (must not exist)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		if err := firmware.EditCredentials(*config, *id, *role, *output, command == "token-revoke"); err != nil {
			return err
		}
		fmt.Println("Credential configuration updated; restart fabricd to apply.")
		return nil
	}
	if command != "upload" && command != "promote" {
		return errors.New("unknown fabricctl command")
	}
	server := flags.String("server", "https://raspberrypi.tail628049.ts.net", "Fabric base URL")
	tokenFile := flags.String("token-file", "", "Publisher token file")
	device := flags.String("device", "x3", "Device type")
	file := flags.String("file", "", "Raw OTA firmware.bin")
	version := flags.String("version", "", "Display version")
	source := flags.String("source", ".", "Git checkout used to build the firmware")
	buildID := flags.String("build-id", "", "Existing build to promote")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *device != "x3" {
		return errors.New("only device x3 is supported; no positional arguments are accepted")
	}
	base, err := url.Parse(*server)
	if err != nil || base == nil || base.User != nil || base.Host == "" || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return errors.New("server must be an HTTPS origin without credentials")
	}
	loopback := base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1" || base.Hostname() == "::1"
	if base.Scheme != "https" && !(base.Scheme == "http" && loopback) {
		return errors.New("HTTPS is required except on loopback")
	}
	tokenBytes, err := os.ReadFile(*tokenFile)
	if err != nil {
		return errors.New("could not read publisher token file")
	}
	token := strings.TrimSpace(string(tokenBytes))
	if len(token) != 64 {
		return errors.New("invalid publisher token file")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return errors.New("invalid publisher token file")
	}
	endpoint := strings.TrimRight(base.String(), "/") + "/api/firmware/v1/x3"
	var request *http.Request
	if command == "promote" {
		if len(*buildID) != 32 {
			return errors.New("--build-id is required")
		}
		body, _ := json.Marshal(map[string]string{"build_id": *buildID})
		request, err = http.NewRequest(http.MethodPut, endpoint+"/stable", bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
	} else {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := firmware.ValidateImage(f); err != nil {
			return fmt.Errorf("image validation: %w", err)
		}
		h := sha256.New()
		size, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		commit, err := exec.Command("git", "-C", *source, "rev-parse", "HEAD").Output()
		if err != nil {
			return errors.New("could not determine build Git commit")
		}
		status, err := exec.Command("git", "-C", *source, "status", "--porcelain", "--untracked-files=normal").Output()
		if err != nil {
			return errors.New("could not determine Git worktree state")
		}
		metadata := firmware.Metadata{Version: *version, Size: size, SHA256: hex.EncodeToString(h.Sum(nil)), GitCommit: strings.TrimSpace(string(commit)), Dirty: len(status) != 0}
		if err := metadata.Validate(); err != nil {
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		reader, writer := io.Pipe()
		defer reader.Close()
		form := multipart.NewWriter(writer)
		go func() {
			writeErr := func() error {
				part, err := form.CreateFormField("metadata")
				if err != nil {
					return err
				}
				if err := json.NewEncoder(part).Encode(metadata); err != nil {
					return err
				}
				part, err = form.CreateFormFile("firmware", "firmware.bin")
				if err != nil {
					return err
				}
				if _, err := io.Copy(part, f); err != nil {
					return err
				}
				return form.Close()
			}()
			_ = writer.CloseWithError(writeErr)
		}()
		request, err = http.NewRequest(http.MethodPost, endpoint+"/builds", reader)
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", form.FormDataContentType())
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("firmware API redirects are not allowed")
	}}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Fabric returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	_, err = os.Stdout.Write(body)
	return err
}
