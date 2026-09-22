package checkin

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"

	"relayhub/internal/adapter"
	"relayhub/internal/browser"
	"relayhub/internal/domain"
	"relayhub/internal/repository"
)

var ErrNeedManual = errors.New("turnstile or human verification required: manual checkin required")

type Config struct {
	Interval     time.Duration
	RandomJitter time.Duration
	MaxRetries   int
	BaseBackoff  time.Duration
}

type Scheduler struct {
	cfg        Config
	adapters   *adapter.Registry
	repo       repository.ResourceRepository
	states     map[string]JobState
	channelMu  map[string]*sync.Mutex
	detector   func() browser.Info
	executor   browser.Executor
	modelSync  func(ctx context.Context, channelID, pattern string) error
	syncNextAt map[string]time.Time
	mu         sync.RWMutex
	stopCh     chan struct{}
	stopped    chan struct{}
	running    bool
}

func NewScheduler(cfg Config, reg *adapter.Registry, repo repository.ResourceRepository) *Scheduler {
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	if cfg.RandomJitter <= 0 {
		cfg.RandomJitter = 10 * time.Minute
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 5 * time.Second
	}
	return &Scheduler{
		cfg:        cfg,
		adapters:   reg,
		repo:       repo,
		states:     make(map[string]JobState),
		channelMu:  make(map[string]*sync.Mutex),
		detector:   browser.Detect,
		syncNextAt: make(map[string]time.Time),
		stopCh:     make(chan struct{}),
		stopped:    make(chan struct{}),
	}
}

// SetModelSync installs the callback used to auto-sync a channel's upstream
// models when auto_sync is enabled. Wiring provides the concrete implementation
// (it lives in the API layer to reuse the fetch+filter+bind logic).
func (s *Scheduler) SetModelSync(fn func(ctx context.Context, channelID, pattern string) error) {
	s.mu.Lock()
	s.modelSync = fn
	s.mu.Unlock()
}

func (s *Scheduler) SetBrowserDetector(fn func() browser.Info) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detector = fn
}

func (s *Scheduler) SetBrowserExecutor(exec browser.Executor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executor = exec
}

func (s *Scheduler) getChannelMu(channelID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	mu, ok := s.channelMu[channelID]
	if !ok {
		mu = &sync.Mutex{}
		s.channelMu[channelID] = mu
	}
	return mu
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}
	s.stopCh = make(chan struct{})
	s.stopped = make(chan struct{})
	s.running = true
	stopCh := s.stopCh
	stopped := s.stopped
	s.mu.Unlock()

	go s.runLoop(ctx, stopCh, stopped)
	return nil
}

func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	close(s.stopCh)
	stopped := s.stopped
	s.mu.Unlock()

	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scheduler) runLoop(ctx context.Context, stopCh <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		case <-ticker.C:
			s.checkAndTrigger(ctx)
		}
	}
}

// maybeAutoSync triggers a model sync for a channel at most once per configured
// interval (reusing the check-in Interval as the sync cadence). It is a no-op
// when no modelSync callback is installed.
func (s *Scheduler) maybeAutoSync(ctx context.Context, ch domain.Channel, now time.Time) {
	s.mu.Lock()
	fn := s.modelSync
	if fn == nil {
		s.mu.Unlock()
		return
	}
	next, ok := s.syncNextAt[ch.ID]
	if ok && now.Before(next) {
		s.mu.Unlock()
		return
	}
	s.syncNextAt[ch.ID] = now.Add(s.cfg.Interval)
	s.mu.Unlock()

	go func(id, pattern string) {
		_ = fn(ctx, id, pattern)
	}(ch.ID, ch.AutoSyncPattern)
}

func (s *Scheduler) checkAndTrigger(ctx context.Context) {
	if s.repo == nil {
		return
	}
	channels, err := s.repo.ListChannels(ctx, "")
	if err != nil {
		return
	}

	now := time.Now().UTC()
	for _, ch := range channels {
		// Auto-sync models runs independently of check-in enablement: an enabled
		// channel with auto_sync refreshes its model bindings on the sync cadence.
		if ch.Enabled && ch.AutoSync {
			s.maybeAutoSync(ctx, ch, now)
		}
		if !ch.Enabled || !ch.CheckinEnabled {
			continue
		}
		s.mu.Lock()
		st, ok := s.states[ch.ID]
		if !ok {
			// First-time schedule: allocate random initial delay (0 to RandomJitter)
			var jitter time.Duration
			if s.cfg.RandomJitter > 0 {
				jitter = time.Duration(rand.Int63n(int64(s.cfg.RandomJitter)))
			}
			st = JobState{
				ChannelID: ch.ID,
				Status:    StatusIdle,
				NextRunAt: now.Add(jitter),
			}
			s.states[ch.ID] = st
			s.mu.Unlock()
			continue
		}
		shouldTrigger := now.After(st.NextRunAt)
		s.mu.Unlock()

		if shouldTrigger {
			go func(c domain.Channel) {
				_ = s.RunNow(ctx, c.ID)
			}(ch)
		}
	}
}

func (s *Scheduler) RunNow(ctx context.Context, channelID string) error {
	mu := s.getChannelMu(channelID)
	if !mu.TryLock() {
		return fmt.Errorf("job already in progress for channel: %s", channelID)
	}
	defer mu.Unlock()

	if s.repo == nil {
		return fmt.Errorf("repository not configured")
	}

	ch, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return err
	}
	provider, err := s.repo.GetProvider(ctx, ch.ProviderID)
	if err != nil {
		return err
	}

	if !provider.Enabled || !ch.Enabled || !ch.CheckinEnabled {
		return fmt.Errorf("check-in disabled for channel or provider")
	}
	adp, err := s.adapters.Resolve(provider)
	if err != nil {
		return err
	}

	s.updateState(channelID, func(st *JobState) {
		st.Status = StatusRunning
		st.ExecutionStatus = ""
		st.LastRunAt = time.Now().UTC()
	})

	// Check manual checkin_mode first
	if ch.CheckinMode == "manual" {
		now := time.Now().UTC()
		jitter := time.Duration(rand.Int63n(int64(s.cfg.RandomJitter)))
		nextRun := now.Add(s.cfg.Interval).Add(jitter)
		s.updateState(channelID, func(st *JobState) {
			st.ChannelID = channelID
			st.LastRunAt = now
			st.NextRunAt = nextRun
			st.Status = StatusNeedManual
			st.LastError = "manual checkin required by channel mode"
		})
		rec := domain.CheckinRecord{
			ID:           uuid.NewString(),
			ChannelID:    channelID,
			StartedAt:    now,
			FinishedAt:   &now,
			Status:       "need_manual",
			ErrorMessage: "manual checkin required by channel mode",
		}
		if err := s.repo.CreateCheckinRecord(ctx, rec); err != nil {
			return s.recordFailure(channelID, err)
		}
		return ErrNeedManual
	}

	var result adapter.CheckInResult
	var lastErr error

	for attempt := 0; attempt <= s.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := s.cfg.BaseBackoff * time.Duration(1<<attempt)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		result, lastErr = adp.CheckIn(ctx, ch)
		if errors.Is(lastErr, adapter.ErrNeedWebview) {
			s.mu.RLock()
			detector := s.detector
			executor := s.executor
			s.mu.RUnlock()
			var bInfo browser.Info
			if detector != nil {
				bInfo = detector()
			}

			// If a browser executor is configured and browser is available, attempt CDP execution
			if executor != nil && bInfo.Available {
				targetURL := ch.BaseURL
				execRes, execErr := executor.ExecuteCheckin(ctx, browser.CheckinRequest{
					URL:        targetURL,
					ProviderID: provider.ID,
					ChannelID:  ch.ID,
					Timeout:    30 * time.Second,
					// Browser check-ins honour the same channel proxy as the
					// HTTP adapter paths (AUDIT RH-10).
					ProxyURL: ch.ProxyURL,
				})
				if execErr == nil && execRes.Success {
					result = adapter.CheckInResult{
						Success: true,
						Reward:  execRes.Reward,
						Message: execRes.Message,
					}
					lastErr = nil
					break
				}
				// If CDP execution encountered turnstile interactive challenge or timeout,
				// fail closed and downgrade to manual.
				manualReason := "turnstile challenge interactive or failed: manual checkin required"
				if execErr != nil {
					manualReason = fmt.Sprintf("turnstile verification failed (%v): manual checkin required", execErr)
				}
				now := time.Now().UTC()
				jitter := time.Duration(rand.Int63n(int64(s.cfg.RandomJitter)))
				nextRun := now.Add(s.cfg.Interval).Add(jitter)
				s.updateState(channelID, func(st *JobState) {
					st.ChannelID = channelID
					st.LastRunAt = now
					st.NextRunAt = nextRun
					st.Status = StatusNeedManual
					st.LastError = manualReason
				})
				rec := domain.CheckinRecord{
					ID:           uuid.NewString(),
					ChannelID:    channelID,
					StartedAt:    now,
					FinishedAt:   &now,
					Status:       "need_manual",
					ErrorMessage: manualReason,
				}
				if err := s.repo.CreateCheckinRecord(ctx, rec); err != nil {
					return s.recordFailure(channelID, err)
				}
				return ErrNeedManual
			}

			manualReason := "turnstile verification required: browser executor is not configured, manual checkin required"
			if !bInfo.Available {
				manualReason = "turnstile verification required: no browser runtime available, manual checkin required"
			}
			{
				// Fail closed until a browser executor can provide server evidence.
				now := time.Now().UTC()
				jitter := time.Duration(rand.Int63n(int64(s.cfg.RandomJitter)))
				nextRun := now.Add(s.cfg.Interval).Add(jitter)
				s.updateState(channelID, func(st *JobState) {
					st.ChannelID = channelID
					st.LastRunAt = now
					st.NextRunAt = nextRun
					st.Status = StatusNeedManual
					st.LastError = manualReason
				})
				rec := domain.CheckinRecord{
					ID:           uuid.NewString(),
					ChannelID:    channelID,
					StartedAt:    now,
					FinishedAt:   &now,
					Status:       "need_manual",
					ErrorMessage: manualReason,
				}
				if err := s.repo.CreateCheckinRecord(ctx, rec); err != nil {
					return s.recordFailure(channelID, err)
				}
				return ErrNeedManual
			}
		}
		if lastErr == nil && result.Success {
			break
		}
	}

	now := time.Now().UTC()
	jitter := time.Duration(rand.Int63n(int64(s.cfg.RandomJitter)))
	nextRun := now.Add(s.cfg.Interval).Add(jitter)

	s.updateState(channelID, func(st *JobState) {
		st.ChannelID = channelID
		st.LastRunAt = now
		st.NextRunAt = nextRun
		if lastErr == nil && result.Success {
			st.Status = StatusSuccess
			st.LastReward = result.Reward
			st.LastError = ""
			st.FailureCount = 0
		} else {
			st.Status = StatusFailed
			if lastErr != nil {
				st.LastError = lastErr.Error()
			} else {
				st.LastError = result.Message
			}
			st.FailureCount++
		}
	})

	// Record in repository if available
	rec := domain.CheckinRecord{
		ID:         uuid.NewString(),
		ChannelID:  channelID,
		StartedAt:  now,
		FinishedAt: &now,
	}
	if lastErr == nil && result.Success {
		rec.Status = "success"
		rec.Reward = result.Reward
	} else {
		rec.Status = "failed"
		if lastErr != nil {
			rec.ErrorMessage = lastErr.Error()
		} else {
			rec.ErrorMessage = result.Message
		}
	}
	if err := s.repo.CreateCheckinRecord(ctx, rec); err != nil {
		return s.recordFailure(channelID, err)
	}

	if lastErr != nil {
		return lastErr
	}
	return nil
}

func (s *Scheduler) recordFailure(channelID string, cause error) error {
	err := fmt.Errorf("failed to save checkin record: %w", cause)
	s.updateState(channelID, func(st *JobState) {
		st.ExecutionStatus = st.Status
		st.Status = StatusPersistenceFailed
		if st.LastError != "" {
			st.LastError += "; "
		}
		st.LastError += "check-in outcome could not be persisted; verify outcome before retrying"
	})
	return err
}

func (s *Scheduler) GetState(channelID string) JobState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[channelID]
}

func (s *Scheduler) updateState(channelID string, fn func(*JobState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[channelID]
	fn(&st)
	s.states[channelID] = st
}
