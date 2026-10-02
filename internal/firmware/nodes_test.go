package firmware

import (
	"bytes"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/n0remac/Fabric/internal/auth"
	"github.com/n0remac/Fabric/internal/nodes"
)

func TestNodeCapabilitiesProtectFirmware(t *testing.T) {
	r, err := nodes.Open(filepath.Join(t.TempDir(), "nodes.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	reader, readerDigest, err := nodes.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	publisher, publisherDigest, err := nodes.RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add(nodes.Node{ID: "x3-pocket", Type: "xteink-x3", Name: "Reader", Enabled: true, Permissions: []string{"firmware.read"}, Attributes: map[string]string{"firmware_channel": "dev"}}, readerDigest); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(nodes.Node{ID: "pi-home", Type: "raspberry-pi", Name: "Publisher", Enabled: true, Permissions: []string{"firmware.read", "firmware.publish"}}, publisherDigest); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mux := http.NewServeMux()
	(&Handler{Store: s, Auth: &auth.Middleware{Registry: r}}).Mount(mux)
	image := imageFixture("x4", 5, true)
	if response := request(t, mux, "GET", apiPrefix+"/x3/manifest", "", nil, ""); response.Code != 401 {
		t.Fatal(response.Code)
	}
	if response := request(t, mux, "GET", apiPrefix+"/x3/manifest", reader, nil, ""); response.Code != 200 {
		t.Fatal(response.Code)
	}
	body, contentType := uploadBody(t, image, false)
	if response := request(t, mux, "POST", apiPrefix+"/x3/builds", reader, body, contentType); response.Code != 403 {
		t.Fatal(response.Code)
	}
	body, contentType = uploadBody(t, image, false)
	if response := request(t, mux, "POST", apiPrefix+"/x3/builds", publisher, body, contentType); response.Code != 201 {
		t.Fatalf("publisher upload: %d %s", response.Code, response.Body)
	}
	build, err := s.Latest("dev")
	if err != nil {
		t.Fatal(err)
	}
	if response := request(t, mux, "GET", apiPrefix+"/x3/latest?channel=stable", reader, nil, ""); response.Code != 200 {
		t.Fatal("reader channel attribute did not select dev")
	}
	if response := request(t, mux, "GET", build.DownloadPath, reader, nil, ""); response.Code != 200 || !bytes.Equal(response.Body.Bytes(), image) {
		t.Fatal("authenticated download mismatch")
	}
}
