package firmware

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func imageFixture(tag string, chip uint16, appended bool) []byte {
	data := make([]byte, 64<<10)
	binary.LittleEndian.PutUint32(data, 0xabcd5432)
	// Cross a validator buffer boundary to exercise the streaming board scanner.
	copy(data[(32<<10)-9:], "CROSSPOINT-BOARD-V1:"+tag+";")
	body := make([]byte, 32)
	body[0], body[1] = 0xe9, 1
	if appended {
		body[23] = 1
	}
	binary.LittleEndian.PutUint16(body[12:], chip)
	binary.LittleEndian.PutUint32(body[28:], uint32(len(data)))
	body = append(body, data...)
	checksum := byte(0xef)
	for _, b := range data {
		checksum ^= b
	}
	padEnd := (len(body) + 16) &^ 15
	body = append(body, make([]byte, padEnd-len(body))...)
	body[len(body)-1] = checksum
	if appended {
		sum := sha256.Sum256(body)
		body = append(body, sum[:]...)
	}
	return body
}

func imageFile(t *testing.T, body []byte) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "image-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(body); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func metadataFor(body []byte) Metadata {
	sum := sha256.Sum256(body)
	return Metadata{Version: "1.6.5-dev", Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), GitCommit: strings.Repeat("a", 40), Dirty: true}
}

func TestImageValidation(t *testing.T) {
	valid := imageFixture("x4", 5, true)
	mutate := func(fn func([]byte)) []byte { body := bytes.Clone(valid); fn(body); return body }
	cases := map[string]struct {
		image []byte
		valid bool
	}{
		"app with digest":    {valid, true},
		"app without digest": {imageFixture("x4", 5, false), true},
		"wrong chip":         {imageFixture("x4", 9, true), false},
		"wrong board":        {imageFixture("x4pro", 5, true), false},
		"no board tag":       {mutate(func(b []byte) { clear(b[32+(32<<10)-9 : 32+(32<<10)+40]) }), false},
		"conflicting tag":    {mutate(func(b []byte) { copy(b[1000:], "CROSSPOINT-BOARD-V1:sticky;") }), false},
		"bootloader":         {mutate(func(b []byte) { clear(b[32:36]) }), false},
		"truncated":          {valid[:len(valid)-1], false},
		"extra bytes":        {append(bytes.Clone(valid), 0), false},
		"bad checksum":       {mutate(func(b []byte) { b[len(b)-33] ^= 1 }), false},
		"bad digest":         {mutate(func(b []byte) { b[len(b)-1] ^= 1 }), false},
		"invalid header":     {mutate(func(b []byte) { b[0] = 0 }), false},
		"zero segments":      {mutate(func(b []byte) { b[1] = 0 }), false},
		"segment overflow":   {mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[28:], 0xffffffff) }), false},
		"too small":          {valid[:4096], false},
		"too large":          {make([]byte, MaxImageBytes+1), false},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateImage(imageFile(t, test.image))
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
		})
	}
}

func TestStorePersistenceAndFailures(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir); err == nil {
		t.Fatal("second registry writer accepted")
	}
	body := imageFixture("x4", 5, true)
	b, err := s.Publish(metadataFor(body), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Latest("stable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stable fell back to dev: %v", err)
	}
	if _, err := s.Promote(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Promote("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	bad := metadataFor(body)
	bad.SHA256 = strings.Repeat("0", 64)
	if _, err := s.Publish(bad, bytes.NewReader(body)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	bad = metadataFor(body)
	bad.Size = MaxImageBytes + 1
	if _, err := s.Publish(bad, bytes.NewReader(body)); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if _, _, err := s.Open("../../secret"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if len(s.Manifest().Builds) != 1 {
		t.Fatal("rejected publication changed manifest")
	}
	// Mutating a returned manifest must not mutate registry state.
	m := s.Manifest()
	*m.Channels.Dev = "broken"
	m.Builds[0].Version = "broken"
	if got, _ := s.Latest("dev"); got.ID != b.ID || got.Version != b.Version {
		t.Fatal("manifest aliases registry state")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x3", ".staging", "interrupted"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x3", "builds", strings.Repeat("f", 32)+".bin"), []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(filepath.Join(dir, "x3", ".staging", "interrupted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging file retained")
	}
	if got, _ := s.Latest("stable"); got.ID != b.ID {
		t.Fatal("stable did not survive restart")
	}
	_, f, err := s.Open(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	download, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(download, body) {
		t.Fatal("download differs")
	}
	if stat, _ := f.Stat(); stat.Mode().Perm()&0222 != 0 {
		t.Fatal("build is writable")
	}
}

func TestManifestFailureDoesNotAdvancePointers(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := imageFixture("x4", 5, true)
	first, err := s.Publish(metadataFor(body), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.manifestPath(), s.manifestPath()+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.manifestPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(metadataFor(body), bytes.NewReader(body)); err == nil {
		t.Fatal("expected atomic manifest replacement failure")
	}
	if got, _ := s.Latest("dev"); got.ID != first.ID || len(s.Manifest().Builds) != 1 {
		t.Fatal("failed manifest write advanced pointer")
	}
}

func TestCorruptRegistryRejected(t *testing.T) {
	for _, corruption := range []string{"json", "missing", "hash", "symlink"} {
		t.Run(corruption, func(t *testing.T) {
			dir := t.TempDir()
			s, err := NewStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			body := imageFixture("x4", 5, true)
			b, err := s.Publish(metadataFor(body), bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			switch corruption {
			case "json":
				err = os.WriteFile(s.manifestPath(), []byte("{"), 0640)
			case "missing":
				err = os.Remove(s.buildPath(b.ID))
			case "hash":
				err = os.Chmod(s.buildPath(b.ID), 0640)
				if err == nil {
					err = os.WriteFile(s.buildPath(b.ID), bytes.Repeat([]byte{1}, len(body)), 0440)
				}
			case "symlink":
				err = os.Remove(s.buildPath(b.ID))
				if err == nil {
					err = os.Symlink("/etc/passwd", s.buildPath(b.ID))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if store, err := NewStore(dir); err == nil {
				store.Close()
				t.Fatal("corrupt registry accepted")
			}
		})
	}
}

func TestConcurrentPublication(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := imageFixture("x4", 5, true)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Publish(metadataFor(body), bytes.NewReader(body)); err != nil {
				t.Error(err)
			}
			_ = s.Manifest()
		}()
	}
	wg.Wait()
	if len(s.Manifest().Builds) != 8 {
		t.Fatal("concurrent publications lost builds")
	}
}

func testAccess() AccessConfig {
	credentials := []Credential{}
	for _, role := range []string{"reader", "publisher"} {
		sum := sha256.Sum256([]byte(role))
		credentials = append(credentials, Credential{ID: role, Role: role, SHA256: hex.EncodeToString(sum[:]), Devices: []string{"x3"}})
	}
	return AccessConfig{SchemaVersion: 1, Credentials: credentials}
}

func request(t *testing.T, handler http.Handler, method, path, token string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, body)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func uploadBody(t *testing.T, body []byte, extra bool) (io.Reader, string) {
	t.Helper()
	buffer := new(bytes.Buffer)
	writer := multipart.NewWriter(buffer)
	part, err := writer.CreateFormField("metadata")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(part).Encode(metadataFor(body)); err != nil {
		t.Fatal(err)
	}
	part, err = writer.CreateFormFile("firmware", "../../untrusted.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatal(err)
	}
	if extra {
		if err := writer.WriteField("unexpected", "value"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer, writer.FormDataContentType()
}

func TestFirmwareAPI(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mux := http.NewServeMux()
	(&Handler{Store: s, Access: testAccess()}).Mount(mux)
	for _, path := range []string{"/x3/manifest", "/x3/latest", "/x3/builds/missing/download", "/unknown/manifest"} {
		w := request(t, mux, "GET", apiPrefix+path, "", nil, "")
		if w.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
	}
	for _, test := range []struct {
		method, path, token string
		status              int
	}{
		{"GET", "/x3/manifest", "invalid", 401}, {"GET", "/other/manifest", "reader", 403},
		{"POST", "/x3/builds", "reader", 403}, {"PUT", "/x3/stable", "reader", 403},
		{"GET", "/x3/latest", "reader", 404}, {"GET", "/x3/latest?channel=bad", "reader", 422},
	} {
		w := request(t, mux, test.method, apiPrefix+test.path, test.token, nil, "")
		if w.Code != test.status {
			t.Fatalf("%s: %d: %s", test.path, w.Code, w.Body)
		}
	}
	body := imageFixture("x4", 5, true)
	for _, extra := range []bool{true, false} {
		reader, contentType := uploadBody(t, body, extra)
		w := request(t, mux, "POST", apiPrefix+"/x3/builds", "publisher", reader, contentType)
		if extra {
			if w.Code != 422 || len(s.Manifest().Builds) != 0 {
				t.Fatalf("extra part committed: %d", w.Code)
			}
			continue
		}
		if w.Code != 201 {
			t.Fatalf("upload: %d %s", w.Code, w.Body)
		}
	}
	b, err := s.Latest("dev")
	if err != nil {
		t.Fatal(err)
	}
	if w := request(t, mux, "GET", apiPrefix+"/x3/latest", "reader", nil, ""); w.Code != 404 {
		t.Fatal("stable fell back to dev")
	}
	w := request(t, mux, "PUT", apiPrefix+"/x3/stable", "publisher", strings.NewReader(`{"build_id":"`+b.ID+`"}`), "application/json")
	if w.Code != 200 {
		t.Fatalf("promote: %d %s", w.Code, w.Body)
	}
	w = request(t, mux, "GET", b.DownloadPath, "reader", nil, "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatal("authenticated download mismatch")
	}
	w = request(t, mux, "HEAD", b.DownloadPath, "", nil, "")
	if w.Code != 401 {
		t.Fatal("anonymous HEAD bypassed authentication")
	}
	w = request(t, mux, "GET", apiPrefix+"/x3/builds/not-a-build/download", "reader", nil, "")
	if w.Code != 404 {
		t.Fatal("unlisted download accepted")
	}
	// Revocation takes effect on a freshly mounted handler after configuration reload.
	access := testAccess()
	access.Credentials = access.Credentials[1:]
	revokedMux := http.NewServeMux()
	(&Handler{Store: s, Access: access}).Mount(revokedMux)
	if w := request(t, revokedMux, "GET", b.DownloadPath, "reader", nil, ""); w.Code != 401 {
		t.Fatal("revoked token accepted")
	}
}

func TestCredentialAdministration(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "access.json")
	tokenPath := filepath.Join(dir, "reader.token")
	if err := EditCredentials(configPath, "x3-personal", "reader", tokenPath, false); err != nil {
		t.Fatal(err)
	}
	c, err := LoadAccess(configPath)
	if err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Authenticate(strings.TrimSpace(string(token))); !ok {
		t.Fatal("new token rejected")
	}
	config, _ := os.ReadFile(configPath)
	if bytes.Contains(config, bytes.TrimSpace(token)) {
		t.Fatal("raw token persisted in server config")
	}
	if err := EditCredentials(configPath, "x3-personal", "reader", filepath.Join(dir, "other"), false); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	if err := EditCredentials(configPath, "publisher", "publisher", filepath.Join(dir, "publisher.token"), false); err != nil {
		t.Fatal(err)
	}
	if err := EditCredentials(configPath, "x3-personal", "", "", true); err != nil {
		t.Fatal(err)
	}
	c, err = LoadAccess(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Authenticate(strings.TrimSpace(string(token))); ok {
		t.Fatal("revoked token accepted")
	}
	if err := os.Chmod(configPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAccess(configPath); err == nil {
		t.Fatal("world-readable config accepted")
	}
}
