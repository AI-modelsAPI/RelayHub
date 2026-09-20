package seekai

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
			SiteKey:        "seekai",
			SiteName:       "SeekAI",
			CheckinType:    "newapi",
			NeedsRelogin:   false,
			DisableCheckin: true, // Cloudflare Turnstile, disabled in pure HTTP
		},
	}
}

func NewWithSecrets(client *http.Client, sec adapter.SecretResolver) *Adapter {
	a := New(client)
	a.Secrets = sec
	return a
}
