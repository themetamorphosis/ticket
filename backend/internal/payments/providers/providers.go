// Package providers builds the configured payment gateway.
package providers

import (
	"kacheem/backend/internal/config"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/bkash"
	"kacheem/backend/internal/payments/fakegw"
)

// New returns the gateway named by cfg. publicOrigin is where the fake
// gateway's hosted page is served from (development only).
func New(cfg config.Payment, db dbx.DB, publicOrigin string) payments.Gateway {
	if cfg.Provider == "bkash" {
		return bkash.New(bkash.Config{
			BaseURL: cfg.BkashBaseURL, Username: cfg.BkashUsername, Password: cfg.BkashPassword,
			AppKey: cfg.BkashAppKey, AppSecret: cfg.BkashAppSecret,
		}, db, bkash.HTTPClient())
	}
	return &fakegw.Gateway{DB: db, BaseURL: publicOrigin}
}
