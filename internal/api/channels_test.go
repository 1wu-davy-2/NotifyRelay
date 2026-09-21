package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"notifyrelay/internal/channel"
)

func getChannels(t *testing.T, h http.Handler, token string) (*httptest.ResponseRecorder, channelsResponse) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp channelsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
		}
	}
	return rec, resp
}

func findChannel(t *testing.T, resp channelsResponse, typeName string) channelInfo {
	t.Helper()
	for _, c := range resp.Channels {
		if c.Type == typeName {
			return c
		}
	}
	t.Fatalf("channel %q missing from the response", typeName)
	return channelInfo{}
}

func TestChannels_ListsRegisteredTypesWithEnoughToConfigureThem(t *testing.T) {
	h, _ := newTestHandler(t, channelCfg("good", nil))

	rec, resp := getChannels(t, h, testToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(resp.Channels) == 0 {
		t.Fatal("no channels reported")
	}

	info := findChannel(t, resp, "apitest")

	// Enough to write a configuration without reading the source.
	if len(info.Parameters) == 0 {
		t.Fatal("channel has no parameters; a caller cannot construct a configuration")
	}

	byName := map[string]channel.ParamSpec{}
	for _, p := range info.Parameters {
		byName[p.Name] = p
		if p.Type == "" {
			t.Errorf("parameter %q declares no type", p.Name)
		}
	}

	fail, ok := byName["fail"]
	if !ok {
		t.Fatal("the enum parameter is missing")
	}
	if fail.Type != channel.ParamEnum {
		t.Errorf("fail type = %q, want enum", fail.Type)
	}
	if len(fail.Values) == 0 {
		t.Error("an enum parameter must list its allowed values")
	}

	if _, ok := byName["delay"]; !ok {
		t.Error("the duration parameter is missing")
	}
}

func TestChannels_ReportsCapability(t *testing.T) {
	h, _ := newTestHandler(t, channelCfg("good", nil))

	_, resp := getChannels(t, h, testToken)
	info := findChannel(t, resp, "apitest")

	if len(info.Capability.SupportedFormats) == 0 {
		t.Error("capability must declare which formats the channel renders")
	}
	if info.Capability.OverflowMode == "" {
		t.Error("capability must declare an overflow mode")
	}
}

func TestChannels_ListsConfiguredInstances(t *testing.T) {
	h, _ := newTestHandler(t,
		channelCfg("primary", nil),
		channelCfg("secondary", nil),
	)

	_, resp := getChannels(t, h, testToken)
	info := findChannel(t, resp, "apitest")

	if len(info.Configured) != 2 {
		t.Fatalf("configured = %v, want two instances", info.Configured)
	}
	if info.Configured[0] != "primary" || info.Configured[1] != "secondary" {
		t.Errorf("configured = %v, want [primary secondary]", info.Configured)
	}
}

// A declared default for a secret parameter would be a secret in the
// configuration file, and this endpoint is not the place to publish it.
func TestChannels_NeverPublishesASecretDefault(t *testing.T) {
	registerFakeChannel()

	// The built-in channels are registered by the aggregator package, which
	// this test package does not import; assert on the rule directly instead.
	spec := channel.ParamSpec{
		Name: "password", Type: channel.ParamString, Private: true, Default: "hunter2",
	}
	if got := publicSpec(spec); got.Default != nil {
		t.Errorf("a private parameter's default leaked: %v", got.Default)
	}

	open := channel.ParamSpec{Name: "host", Type: channel.ParamString, Default: "localhost"}
	if got := publicSpec(open); got.Default != "localhost" {
		t.Errorf("a non-private default was dropped: %v", got.Default)
	}
}

func TestChannels_RequiresAKey(t *testing.T) {
	h, _ := newTestHandler(t, channelCfg("good", nil))

	if rec, _ := getChannels(t, h, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d without a key, want 401", rec.Code)
	}
	if rec, _ := getChannels(t, h, "wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d with a wrong key, want 401", rec.Code)
	}
}
