package partneredition

import (
	"errors"
	"testing"
)

func TestCurrentRejectsInvalidBuildMetadata(t *testing.T) {
	oldEdition, oldGateway := builtEdition, builtGatewayURL
	t.Cleanup(func() { builtEdition, builtGatewayURL = oldEdition, oldGateway })
	builtEdition, builtGatewayURL = "partner", "https://23.138.12.112:2443"
	got, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsPartner() || got.GatewayURL != "https://23.138.12.112:2443" {
		t.Fatalf("got=%+v", got)
	}

	builtEdition = "unexpected"
	if _, err := Current(); !errors.Is(err, ErrInvalidEdition) {
		t.Fatalf("err=%v", err)
	}
}

func TestCurrentDefaultsToOwner(t *testing.T) {
	oldEdition, oldGateway := builtEdition, builtGatewayURL
	t.Cleanup(func() { builtEdition, builtGatewayURL = oldEdition, oldGateway })
	builtEdition, builtGatewayURL = "owner", ""

	got, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != Owner || got.GatewayURL != "" || got.IsPartner() {
		t.Fatalf("got=%+v", got)
	}
}

func TestCurrentRejectsInvalidPartnerGateway(t *testing.T) {
	oldEdition, oldGateway := builtEdition, builtGatewayURL
	t.Cleanup(func() { builtEdition, builtGatewayURL = oldEdition, oldGateway })
	builtEdition = "partner"

	tests := []struct {
		name       string
		gatewayURL string
	}{
		{name: "http scheme", gatewayURL: "http://23.138.12.112:2443"},
		{name: "wrong host", gatewayURL: "https://23.138.12.113:2443"},
		{name: "extra path", gatewayURL: "https://23.138.12.112:2443/api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builtGatewayURL = tt.gatewayURL
			if _, err := Current(); !errors.Is(err, ErrInvalidGateway) {
				t.Fatalf("gatewayURL=%q err=%v", tt.gatewayURL, err)
			}
		})
	}
}

func TestCurrentOwnerIgnoresLeftoverGatewayURL(t *testing.T) {
	oldEdition, oldGateway := builtEdition, builtGatewayURL
	t.Cleanup(func() { builtEdition, builtGatewayURL = oldEdition, oldGateway })
	builtEdition, builtGatewayURL = "owner", "https://23.138.12.112:2443"

	got, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if got != (Config{Name: Owner}) {
		t.Fatalf("got=%+v", got)
	}
}
