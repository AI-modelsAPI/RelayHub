package justdowork

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
			SiteKey:        "justdowork",
			SiteName:       "JustDoWork",
			CheckinType:    "newapi",
			NeedsRelogin:   true,
			DisableCheckin: true, // Turnstile + needs relogin, disabled in pure HTTP
		},
	}
}

func NewWithSecrets(client *http.Client, sec adapter.SecretResolver) *Adapter {
	a := New(client)
	a.Secrets = sec
	return a
}
