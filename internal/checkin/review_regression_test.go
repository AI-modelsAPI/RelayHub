package checkin

import (
	"context"
	"errors"
	"relayhub/internal/adapter"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
	"testing"
)

type reviewRepo struct {
	repository.ResourceRepository
	provider domain.Provider
	channel  domain.Channel
	saveErr  error
}

func (r *reviewRepo) GetProvider(context.Context, string) (domain.Provider, error) {
	return r.provider, nil
}
func (r *reviewRepo) GetChannel(context.Context, string) (domain.Channel, error) {
	return r.channel, nil
}
func (r *reviewRepo) CreateCheckinRecord(context.Context, domain.CheckinRecord) error {
	return r.saveErr
}

type countedAdapter struct {
	mockSuccessfulAdapter
	calls int
}

func (a *countedAdapter) CheckIn(context.Context, domain.Channel) (adapter.CheckInResult, error) {
	a.calls++
	return adapter.CheckInResult{Success: true}, nil
}

func TestReviewDisabledProviderDoesNotExecute(t *testing.T) {
	repo := &reviewRepo{provider: domain.Provider{ID: "p", AdapterType: "test", Enabled: false}, channel: domain.Channel{ID: "c", ProviderID: "p", Enabled: true, CheckinEnabled: true}}
	a := &countedAdapter{}
	reg := adapter.NewRegistry()
	_ = reg.Register("test", a)
	s := NewScheduler(Config{}, reg, repo)
	if err := s.RunNow(context.Background(), "c"); err == nil {
		t.Error("disabled provider was not rejected")
	}
	if a.calls != 0 {
		t.Fatalf("disabled provider adapter called %d times", a.calls)
	}
}
func TestReviewPersistenceFailurePropagates(t *testing.T) {
	for _, mode := range []string{"auto", "manual"} {
		t.Run(mode, func(t *testing.T) {
			sentinel := errors.New("simulated storage failure")
			repo := &reviewRepo{provider: domain.Provider{ID: "p", AdapterType: "test", Enabled: true}, channel: domain.Channel{ID: "c", ProviderID: "p", Enabled: true, CheckinEnabled: true, CheckinMode: mode}, saveErr: sentinel}
			reg := adapter.NewRegistry()
			_ = reg.Register("test", &countedAdapter{})
			s := NewScheduler(Config{}, reg, repo)
			err := s.RunNow(context.Background(), "c")
			if !errors.Is(err, sentinel) {
				t.Fatalf("storage failure not propagated: %v", err)
			}
			if s.GetState("c").Status == StatusSuccess {
				t.Fatal("storage failure reported success")
			}
		})
	}
}
