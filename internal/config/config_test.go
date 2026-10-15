package config

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCapabilityVectors(t *testing.T) {
	tests := []struct {
		secret string
		want   string
	}{
		{"000102030405060708090a0b0c0d0e0f", "MHLEY5PmW1GWqJkSrlmJpvJUiLhBH_QKy6yKg8a0JPk"},
		{"dd000102030405060708090a0b0c0d0e0f", "IpJrt3e7sKtzPyoXy6w-Zj6GGEvsvclN66JzQEfPYLA"},
	}
	for _, test := range tests {
		secret, err := DecodeSecret(test.secret)
		if err != nil {
			t.Fatal(err)
		}
		got := CapabilityString(DeriveCapability("proxy.example.com", "", secret))
		if got != test.want {
			t.Fatalf("got %q, want %q", got, test.want)
		}
	}
}

func TestHostnameValidation(t *testing.T) {
	for _, valid := range []string{
		"site.example",
		"xn--bcher-kva.example",
		"a.b.example",
		strings.Repeat("a", 63) + ".example",
	} {
		if err := ValidateHostname(valid); err != nil {
			t.Errorf("%q: %v", valid, err)
		}
	}
	for _, invalid := range []string{
		"localhost",
		"127.0.0.1",
		"[::1]",
		"HTTPS://site.example",
		"Site.example",
		"bücher.example",
		"site.example.",
		"site.example:443",
		"site/example",
		"site\\example",
		"site..example",
		"-site.example",
		"site-.example",
		"site_example.com",
		strings.Repeat("a", 64) + ".example",
		strings.Repeat("a.", 127) + "aa",
	} {
		if err := ValidateHostname(invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
}

func TestPublicSourceValidation(t *testing.T) {
	value := Defaults()
	value.PublicHostname = "proxy.example.com"
	value.ProfilesFile = "profiles.json"
	if err := value.validate(); err == nil {
		t.Fatal("configuration without a public source was accepted")
	}
	for _, upstream := range []string{
		"http://127.0.0.1:3000",
		"http://[::1]:3000",
	} {
		value.PublicUpstream = upstream
		if err := value.validate(); err != nil {
			t.Fatalf("valid public upstream %q was rejected: %v", upstream, err)
		}
	}
	for _, upstream := range []string{
		"https://127.0.0.1:3000",
		"http://localhost:3000",
		"http://192.0.2.1:3000",
		"http://127.0.0.1",
		"http://127.0.0.1:3000/path",
		"http://user@127.0.0.1:3000",
	} {
		value.PublicUpstream = upstream
		if err := value.validate(); err == nil {
			t.Fatalf("invalid public upstream %q was accepted", upstream)
		}
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("site"), 0600); err != nil {
		t.Fatal(err)
	}
	value.PublicDir = directory
	value.PublicUpstream = "http://127.0.0.1:3000"
	if err := value.validate(); err == nil {
		t.Fatal("configuration with both public sources was accepted")
	}
}

func TestPlainSecretMayBeginWithEE(t *testing.T) {
	secret, err := DecodeSecret("ee0102030405060708090a0b0c0d0e0f")
	if err != nil || len(secret) != 16 || secret[0] != 0xee {
		t.Fatalf("valid plain secret was rejected: %v", err)
	}
}

func TestLoadAppliesDefaultsAndRelativePaths(t *testing.T) {
	directory := t.TempDir()
	public := filepath.Join(directory, "public")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("site"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := `{"profiles":[{"name":"default","secret":"000102030405060708090a0b0c0d0e0f","backend":"127.0.0.1:2398","carrier_mode":"https-lanes"}]}`
	if err := os.WriteFile(filepath.Join(directory, "profiles.json"), []byte(profiles), 0600); err != nil {
		t.Fatal(err)
	}
	server := `{"public_hostname":"proxy.example.com","public_dir":"public","profiles_file":"profiles.json"}`
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte(server), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TokenKeyFile != filepath.Join(directory, "token.key") || loaded.StaticRoutes != "legacy" || loaded.PublicDir != public || len(loaded.Profiles) != 1 || loaded.Limits.MaxBodyBytes != 2*1024*1024 || loaded.Profiles[0].CarrierMode != CarrierHTTPSLanes {
		t.Fatalf("unexpected loaded configuration: %#v", loaded)
	}
	profileLimits := loaded.Profiles[0].Limits
	if profileLimits.MaxSessions != loaded.Limits.MaxSessionsGlobal ||
		profileLimits.MaxStreams != loaded.Limits.MaxStreamsGlobal ||
		profileLimits.MaxBackendDialsInFlight != loaded.Limits.MaxBackendDialsInFlight ||
		profileLimits.NewSessionsPerMinute != loaded.Limits.NewSessionsPerMinute ||
		profileLimits.NewStreamsPerMinute != loaded.Limits.NewStreamsPerMinute {
		t.Fatalf("profile limits did not inherit global values: %#v", profileLimits)
	}
}

func TestProfileCarrierModeDefaultsAndValidation(t *testing.T) {
	directory := t.TempDir()
	public := filepath.Join(directory, "public")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("site"), 0600); err != nil {
		t.Fatal(err)
	}
	profilesPath := filepath.Join(directory, "profiles.json")
	writeProfiles := func(carrier string) {
		value := `{"profiles":[{"name":"default","secret":"000102030405060708090a0b0c0d0e0f","backend":"127.0.0.1:2398"` + carrier + `}]}`
		if err := os.WriteFile(profilesPath, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := `{"public_hostname":"proxy.example.com","public_dir":"public","profiles_file":"profiles.json"}`
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, []byte(server), 0600); err != nil {
		t.Fatal(err)
	}
	writeProfiles("")
	loaded, err := Load(configPath)
	if err != nil || loaded.Profiles[0].CarrierMode != CarrierHTTPS {
		t.Fatalf("omitted carrier mode did not default to HTTPS: %#v %v", loaded.Profiles, err)
	}
	for _, carrier := range []CarrierMode{
		CarrierHTTPS,
		CarrierHTTPSLanes,
		CarrierWebSocket,
		CarrierWebSocketLanes,
	} {
		writeProfiles(`,"carrier_mode":"` + string(carrier) + `"`)
		loaded, err := Load(configPath)
		if err != nil || loaded.Profiles[0].CarrierMode != carrier {
			t.Fatalf("carrier mode %q was not loaded: %#v %v", carrier, loaded.Profiles, err)
		}
	}
	writeProfiles(`,"carrier_mode":"unknown"`)
	if _, err := Load(configPath); err == nil {
		t.Fatal("invalid carrier mode was accepted")
	}
}

func TestProfileStreamDefaultsRespectProfileCeiling(t *testing.T) {
	global := Defaults().Limits
	resolved := (ProfileLimits{MaxStreams: 32}).WithDefaults(global)
	if resolved.MaxStreamsPerSession != 32 ||
		resolved.MaxBackendDialsInFlight != 32 ||
		resolved.MaxSessions != global.MaxSessionsGlobal ||
		resolved.NewStreamsPerMinute != global.NewStreamsPerMinute {
		t.Fatalf("unexpected resolved profile limits: %#v", resolved)
	}
}

func TestProfileLimitsCannotExceedGlobalCeilings(t *testing.T) {
	global := Defaults().Limits
	if err := validateProfileLimits(ProfileLimits{
		MaxStreams: global.MaxStreamsGlobal + 1,
	}, global); err == nil {
		t.Fatal("profile max_streams exceeded the global ceiling")
	}
	if err := validateProfileLimits(ProfileLimits{
		MaxStreams:           16,
		MaxStreamsPerSession: 17,
	}, global); err == nil {
		t.Fatal("per-session stream limit exceeded the profile stream ceiling")
	}
}

func TestLoadAcceptsSystemdCredentialReadPermissions(t *testing.T) {
	directory := t.TempDir()
	public := filepath.Join(directory, "public")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("site"), 0600); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(directory, "credentials")
	if err := os.Mkdir(credentials, 0700); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(credentials, "profiles.json")
	content := `{"profiles":[{"name":"default","secret":"000102030405060708090a0b0c0d0e0f","backend":"127.0.0.1:2398"}]}`
	if err := os.WriteFile(profiles, []byte(content), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(profiles, 0444); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", credentials)
	server := `{"public_hostname":"proxy.example.com","public_dir":"public","profiles_file":"credentials/profiles.json"}`
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte(server), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	if _, err := Load(path); err == nil {
		t.Fatal("group/other-readable profiles file outside a credential directory was accepted")
	}
}

func TestCarrierBatchMustFitDesktopLoopbackCap(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("index"), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(directory, "profiles.json")
	if err := os.WriteFile(profiles, []byte(`{"profiles":[{"name":"a","secret":"000102030405060708090a0b0c0d0e0f","backend":"127.0.0.1:443"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	write := func(batch, body int) string {
		path := filepath.Join(directory, "config.json")
		content := `{"public_hostname":"proxy.example.com","public_dir":"` + directory + `","profiles_file":"` + profiles + `","limits":{"carrier_batch_bytes":` + strconv.Itoa(batch) + `,"max_body_bytes":` + strconv.Itoa(body) + `}}`
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if _, err := Load(write(3*1024*1024, 4*1024*1024)); err == nil {
		t.Fatal("accepted carrier_batch_bytes above 2 MiB")
	}
	if _, err := Load(write(2*1024*1024, 4*1024*1024)); err != nil {
		t.Fatalf("rejected a 2 MiB carrier batch: %v", err)
	}
}

func TestDefaultReconnectGraceIsShort(t *testing.T) {
	if Defaults().Timeouts.ReconnectGrace.Value() != 2*time.Minute {
		t.Fatal("reconnect_grace default is not 2m")
	}
}

func TestExampleConfigsValidate(t *testing.T) {
	directory := t.TempDir()

	profilesBytes, err := os.ReadFile(filepath.Join("..", "..", "profiles.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	profilesPath := filepath.Join(directory, "profiles.json")
	if err := os.WriteFile(profilesPath, profilesBytes, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(profilesPath, 0o400); err != nil {
		t.Fatal(err)
	}

	tokenKeyPath := filepath.Join(directory, "token.key")
	if err := os.WriteFile(tokenKeyPath, bytes.Repeat([]byte{0x42}, 32), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tokenKeyPath, 0o400); err != nil {
		t.Fatal(err)
	}

	configBytes, err := os.ReadFile(filepath.Join("..", "..", "config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(configBytes, &raw); err != nil {
		t.Fatal(err)
	}
	raw["profiles_file"] = profilesPath
	raw["token_key_file"] = tokenKeyPath
	modifiedConfig, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, modifiedConfig, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load example config and profiles: %v", err)
	}
	if len(loaded.Profiles) < 1 || loaded.Profiles[0].CarrierMode != CarrierHTTPSLanes {
		t.Fatalf("expected default profile in profiles.example.json to use https-lanes, got: %#v", loaded.Profiles)
	}
}

func TestCallsTCPDefaultsValidationAndSSRFGuard(t *testing.T) {
	directory := t.TempDir()
	public := filepath.Join(directory, "public")
	if err := os.Mkdir(public, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("site"), 0o600); err != nil {
		t.Fatal(err)
	}
	profilesPath := filepath.Join(directory, "profiles.json")
	writeProfiles := func(extra string) {
		_ = os.Remove(profilesPath)
		value := `{"profiles":[{"name":"default","secret":"000102030405060708090a0b0c0d0e0f","backend":"127.0.0.1:2398"` + extra + `}]}`
		if err := os.WriteFile(profilesPath, []byte(value), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(directory, "config.json")
	server := `{"public_hostname":"proxy.example.com","public_dir":"public","profiles_file":"profiles.json"}`
	if err := os.WriteFile(configPath, []byte(server), 0o600); err != nil {
		t.Fatal(err)
	}

	// Default Telegram AS CIDRs and ports [443, 5222] when calls_tcp: true on https-lanes.
	writeProfiles(`,"carrier_mode":"https-lanes","calls_tcp":true`)
	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("valid calls_tcp profile failed to load: %v", err)
	}
	profile := loaded.Profiles[0]
	if !profile.CallsTCP || len(profile.CallAllowlist) != len(DefaultTelegramCallPrefixes()) || len(profile.CallPorts) != 2 {
		t.Fatalf("unexpected default call profile: %#v", profile)
	}
	for _, allowed := range []string{
		"149.154.167.99:443",
		"91.108.16.42:5222",
		"[2001:67c:4e8:f002::a]:443",
		"[::ffff:149.154.167.99]:443",
	} {
		if !profile.AllowsCallTarget(netip.MustParseAddrPort(allowed)) {
			t.Fatalf("expected target %s to be allowed", allowed)
		}
	}
	for _, forbidden := range []string{
		"127.0.0.1:443",
		"127.0.0.1:2398",
		"[::1]:443",
		"[::7f00:1]:443",
		"[::ffff:0:127.0.0.1]:443",
		"[::ffff:127.0.0.1]:443",
		"[::ffff:10.0.0.1]:443",
		"10.0.0.1:443",
		"192.168.1.1:443",
		"169.254.169.254:443",
		"100.64.0.1:443",
		"149.154.167.99:80",
		"8.8.8.8:443",
	} {
		if profile.AllowsCallTarget(netip.MustParseAddrPort(forbidden)) {
			t.Fatalf("expected target %s to be rejected", forbidden)
		}
	}
	if !IsForbiddenCallAddr(netip.MustParseAddr("2001:67c:4e8:f002::a").WithZone("lo0")) {
		t.Fatal("expected zoned IPv6 address to be rejected by IsForbiddenCallAddr")
	}

	// Reject calls_tcp on non-lane carriers (https and websocket).
	for _, badCarrier := range []string{`"https"`, `"websocket"`} {
		writeProfiles(`,"carrier_mode":` + badCarrier + `,"calls_tcp":true`)
		if _, err := Load(configPath); err == nil {
			t.Fatalf("accepted calls_tcp on carrier %s", badCarrier)
		}
	}

	// Reject forbidden, non-canonical, or wildcard CIDRs and port 0.
	for _, badExtra := range []string{
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_allowlist":["0.0.0.0/0"]`,
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_allowlist":["::/0"]`,
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_allowlist":["127.0.0.0/8"]`,
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_allowlist":["126.0.0.0/7"]`,
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_allowlist":["10.0.0.0/8"]`,
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_allowlist":["100.64.0.0/10"]`,
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_allowlist":["::ffff:149.154.160.0/116"]`,
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_allowlist":["149.154.167.99/20"]`,
		`,"carrier_mode":"https-lanes","calls_tcp":true,"call_ports":[0]`,
		`,"carrier_mode":"https-lanes","calls_tcp":false,"call_ports":[443]`,
	} {
		writeProfiles(badExtra)
		if _, err := Load(configPath); err == nil {
			t.Fatalf("accepted invalid call config %s", badExtra)
		}
	}
}


