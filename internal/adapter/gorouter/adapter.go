package gorouter

import (
	"net/http"
	"time"

	"relayhub/internal/adapter"
)

type Adapter struct {
	*adapter.BaseNewAPIAdapter
}

func New(client *http.Client) *Adapter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Adapter{
		BaseNewAPIAdapter: &adapter.BaseNewAPIAdapter{
			Client:         client,
			SiteKey:        "gorouter",
			SiteName:       "GoRouter",
			CheckinType:    "login",
			NeedsRelogin:   false,
			DisableCheckin: false,
		},
	}
}

func NewWithSecrets(client *http.Client, sec adapter.SecretResolver) *Adapter {
	a := New(client)
	a.Secrets = sec
	return a
}
