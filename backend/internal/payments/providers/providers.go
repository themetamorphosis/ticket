// Package providers builds the configured payment gateway.
package providers

import (
	"kacheem/backend/internal/config"
	"kacheem/backend/internal/dbx"
	"kacheem/backend/internal/payments"
	"kacheem/backend/internal/payments/bkash"
	"kacheem/backend/internal/payments/fakegw"
	"kacheem/backend/internal/payments/paystation"
)

// New returns the gateway named by cfg. publicOrigin is where the fake
// gateway's hosted page is served from (development only).
func New(cfg config.Payment, db dbx.DB, publicOrigin string) payments.Gateway {
	switch cfg.Provider {
	case "bkash":
		return bkash.New(bkash.Config{
			BaseURL: cfg.BkashBaseURL, Username: cfg.BkashUsername, Password: cfg.BkashPassword,
			AppKey: cfg.BkashAppKey, AppSecret: cfg.BkashAppSecret,
		}, db, bkash.HTTPClient())
	case "paystation":
		return paystation.New(paystation.Config{
			BaseURL: cfg.PayStationBaseURL, MerchantID: cfg.PayStationMerchantID, Password: cfg.PayStationPassword,
		}, paystation.HTTPClient())
	}
	return &fakegw.Gateway{DB: db, BaseURL: publicOrigin}
}
