package firmware

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"golang.org/x/sys/unix"
)

var tokenIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type Credential struct {
	ID      string   `json:"id"`
	SHA256  string   `json:"sha256"`
	Role    string   `json:"role"`
	Devices []string `json:"devices"`
}
type AccessConfig struct {
	SchemaVersion int          `json:"schema_version"`
	Credentials   []Credential `json:"credentials"`
}

func (c AccessConfig) Validate() error {
	if c.SchemaVersion != 1 || len(c.Credentials) == 0 {
		return errors.New("firmware credentials require schema_version 1 and at least one credential")
	}
	ids, digests := map[string]bool{}, map[string]bool{}
	for _, token := range c.Credentials {
		if !tokenIDPattern.MatchString(token.ID) || ids[token.ID] || !shaPattern.MatchString(token.SHA256) || digests[token.SHA256] || (token.Role != "reader" && token.Role != "publisher") || len(token.Devices) != 1 || token.Devices[0] != "x3" {
			return fmt.Errorf("invalid firmware credential %q", token.ID)
		}
		ids[token.ID], digests[token.SHA256] = true, true
	}
	return nil
}

func LoadAccess(path string) (AccessConfig, error) {
	f, err := regularFile(path)
	if err != nil {
		return AccessConfig{}, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return AccessConfig{}, err
	}
	if stat.Mode().Perm()&0007 != 0 || stat.Mode().Perm()&0022 != 0 {
		return AccessConfig{}, errors.New("firmware credential file must not be world accessible or group writable")
	}
	if stat.Size() > 1<<20 {
		return AccessConfig{}, errors.New("firmware credential file exceeds size limit")
	}
	var c AccessConfig
	if err := decodeJSON(io.LimitReader(f, (1<<20)+1), &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

func (c AccessConfig) Authenticate(token string) (Credential, bool) {
	digest := sha256.Sum256([]byte(token))
	var result Credential
	found := false
	for _, credential := range c.Credentials {
		decoded, _ := hex.DecodeString(credential.SHA256)
		if subtle.ConstantTimeCompare(digest[:], decoded) == 1 {
			result, found = credential, true
		}
	}
	return result, found
}

func protectedOutput(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// EditCredentials is an offline administration operation; restart fabricd after editing.
func EditCredentials(path, id, role, output string, revoke bool) error {
	if !tokenIDPattern.MatchString(id) {
		return errors.New("credential ID must contain 1-64 letters, digits, underscores or hyphens")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	c, err := LoadAccess(path)
	if errors.Is(err, os.ErrNotExist) {
		c = AccessConfig{SchemaVersion: 1, Credentials: []Credential{}}
	} else if err != nil {
		return err
	}
	index := -1
	for i, token := range c.Credentials {
		if token.ID == id {
			index = i
		}
	}
	if revoke {
		if index < 0 {
			return errors.New("credential ID not found")
		}
		c.Credentials = append(c.Credentials[:index], c.Credentials[index+1:]...)
	} else {
		if index >= 0 {
			return errors.New("credential ID already exists; issue a new ID before revoking the old one")
		}
		if output == "" || filepath.Clean(output) == filepath.Clean(path) {
			return errors.New("a separate output file is required")
		}
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return err
		}
		token := hex.EncodeToString(secret[:])
		digest := sha256.Sum256([]byte(token))
		c.Credentials = append(c.Credentials, Credential{ID: id, Role: role, SHA256: hex.EncodeToString(digest[:]), Devices: []string{"x3"}})
		if err := c.Validate(); err != nil {
			return err
		}
		if err := protectedOutput(output, []byte(token+"\n")); err != nil {
			return err
		}
	}
	if err := c.Validate(); err != nil {
		return err
	}
	committed, err := AtomicJSON(path, c, 0640)
	if !revoke && !committed {
		_ = os.Remove(output)
	}
	return err
}
