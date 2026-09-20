package checkin

import (
	"context"
	"encoding/json"
	"errors"
	"relayhub/internal/adapter"
	"relayhub/internal/domain"
	"strings"
	"testing"
)

func TestPersistenceFailureRetainsOutcome(t *testing.T) {
	for _, mode := range []string{"auto", "manual"} {
		t.Run(mode, func(t *testing.T) {
			repo := &reviewRepo{provider: domain.Provider{ID: "p", AdapterType: "test", Enabled: true}, channel: domain.Channel{ID: "c", ProviderID: "p", Enabled: true, CheckinEnabled: true, CheckinMode: mode}, saveErr: errors.New("disk unavailable")}
			reg := adapter.NewRegistry()
			_ = reg.Register("test", &countedAdapter{})
			s := NewScheduler(Config{}, reg, repo)
			if err := s.RunNow(context.Background(), "c"); err == nil {
				t.Fatal("expected storage error")
			}
			st := s.GetState("c")
			b, _ := json.Marshal(st)
			var fields map[string]any
			_ = json.Unmarshal(b, &fields)
			want := "success"
			if mode == "manual" {
				want = "need_manual"
			}
			if string(st.Status) != "persistence_failed" || fields["execution_status"] != want {
				t.Fatalf("lost execution outcome: %s", b)
			}
			if mode == "manual" && !strings.Contains(st.LastError, "manual checkin required by channel mode") {
				t.Fatalf("lost manual reason: %+v", st)
			}
		})
	}
}
