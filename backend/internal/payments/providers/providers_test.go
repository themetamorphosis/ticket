package providers

import (
	"testing"

	"kacheem/backend/internal/config"
	"kacheem/backend/internal/payments/bkash"
	"kacheem/backend/internal/payments/fakegw"
)

func TestNew(t *testing.T) {
	if g, ok := New(config.Payment{Provider: "bkash", BkashBaseURL: "https://b"}, nil, "").(*bkash.Gateway); !ok || g.Provider() != "bkash" {
		t.Fatal("bkash")
	}
	if g, ok := New(config.Payment{Provider: "fake"}, nil, "http://localhost:8088").(*fakegw.Gateway); !ok || g.BaseURL != "http://localhost:8088" {
		t.Fatal("fake")
	}
}
