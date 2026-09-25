package main

import "testing"

func TestWebsocketConfig(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("WEBSOCKET_ALLOWED_ORIGINS", "")
	config, err := websocketConfig(":9090")
	if err != nil {
		t.Fatal(err)
	}
	if !config.AllowMissingOrigin || len(config.AllowedOrigins) != 2 || config.AllowedOrigins[0] != "http://localhost:9090" {
		t.Fatalf("unexpected development config: %+v", config)
	}
	if err := config.ValidateRoomID("simulator-system"); err != nil {
		t.Fatal(err)
	}
	if err := config.ValidateRoomID("bad room"); err == nil {
		t.Fatal("expected invalid room to fail")
	}

	t.Setenv("ENVIRONMENT", "production")
	if _, err := websocketConfig(":9090"); err == nil {
		t.Fatal("production should require allowed origins")
	}
}
