package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"relayhub/internal/domain"
)

type staticSecrets map[string][]byte

func (s staticSecrets) Get(_ context.Context, ref string) ([]byte, error) {
	v, ok := s[ref]
	if !ok {
		return nil, fmt.Errorf("missing %s", ref)
	}
	return v, nil
}
func (s staticSecrets) Put(_ context.Context, ref string, v []byte) error { s[ref] = v; return nil }

// VerifyCheckin is the evidence the scheduler trusts after a browser
// check-in; it must say yes only when the site has today's record.
func TestVerifyCheckinUsesServerRecords(t *testing.T) {
	type site struct {
		calendar string // body for /api/user/checkin
		bonus    string // body for /api/log/self
		self     string // body for /api/user/self
		status   int
	}
	today := BeijingTodayStr()
	cases := []struct {
		name      string
		site      site
		wantIn    bool
		wantErr   bool
		wantRew   string
		needLogin bool
	}{
		{
			name:    "calendar has today",
			site:    site{calendar: fmt.Sprintf(`{"success":true,"data":{"stats":{"checked_in_today":true,"records":[{"checkin_date":%q,"quota_awarded":250000}]}}}`, today), bonus: `{"success":true,"data":[]}`, self: `{"success":true,"data":{"quota":1}}`, status: 200},
			wantIn:  true,
			wantRew: "$0.50",
		},
		{
			name:   "no record anywhere",
			site:   site{calendar: `{"success":true,"data":{"stats":{"records":[]}}}`, bonus: `{"success":true,"data":[]}`, self: `{"success":true,"data":{"quota":1,"checked_in":false}}`, status: 200},
			wantIn: false,
		},
		{
			name:   "user flag counts only for non-relogin sites",
			site:   site{calendar: `{"success":true,"data":{"stats":{"records":[]}}}`, bonus: `{"success":true,"data":[]}`, self: `{"success":true,"data":{"quota":1,"checked_in":true}}`, status: 200},
			wantIn: true,
		},
		{
			name:      "user flag ignored for relogin sites",
			site:      site{calendar: `{"success":true,"data":{"stats":{"records":[]}}}`, bonus: `{"success":true,"data":[]}`, self: `{"success":true,"data":{"quota":1,"checked_in":true}}`, status: 200},
			needLogin: true,
			wantIn:    false,
		},
		{
			name:    "site down is an error, not a no",
			site:    site{status: 502},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.site.status != 200 {
					w.WriteHeader(tc.site.status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/status":
					_, _ = w.Write([]byte(`{"data":{"quota_per_unit":500000}}`))
				case "/api/user/checkin":
					_, _ = w.Write([]byte(tc.site.calendar))
				case "/api/log/self":
					_, _ = w.Write([]byte(tc.site.bonus))
				case "/api/user/self":
					_, _ = w.Write([]byte(tc.site.self))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			b := &BaseNewAPIAdapter{Secrets: staticSecrets{"cred": []byte("token-123")}, NeedsRelogin: tc.needLogin}
			ch := domain.Channel{ID: "c", BaseURL: srv.URL, CredentialRef: "cred"}
			v, err := b.VerifyCheckin(context.Background(), ch)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if v.CheckedIn != tc.wantIn {
				t.Fatalf("CheckedIn=%v want %v (%s)", v.CheckedIn, tc.wantIn, v.Message)
			}
			if tc.wantRew != "" && v.Reward != tc.wantRew {
				t.Fatalf("reward=%q want %q", v.Reward, tc.wantRew)
			}
		})
	}
}
