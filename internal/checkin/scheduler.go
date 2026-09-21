package checkin

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"relayhub/internal/adapter"
	"relayhub/internal/browser"
	"relayhub/internal/domain"
	"relayhub/internal/notify"
	"relayhub/internal/repository"
)

var ErrNeedManual = errors.New("turnstile or human verification required: manual checkin required")

type Config struct {
	Interval     time.Duration
	RandomJitter time.Duration
	MaxRetries   int
	BaseBackoff  time.Duration
	// BalanceInterval is the cadence of the periodic balance poll that feeds
	// quota-first routing. Default 1h. Adapters that report
	// ErrUnsupportedOperation are parked for 24h instead of retried hourly.
	BalanceInterval time.Duration
}

// balanceTimeout bounds one Balance() call so a hung site cannot stall the
// scheduler loop or a check-in follow-up.
const balanceTimeout = 30 * time.Second

// unsupportedBalanceRetry is how long a channel whose adapter has no balance
// endpoint is parked before it is probed again (adapters can be swapped).
const unsupportedBalanceRetry = 24 * time.Hour

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
	// quotaObserver receives every persisted balance snapshot; production
	// wiring forwards it to the health registry so routing sees quota.
	quotaObserver func(domain.Channel, domain.QuotaSnapshot)
	balanceNextAt map[string]time.Time
	// proxyResolver yields the egress proxy for a channel so the browser
	// exits through the same path as the HTTP adapters and the gateway.
	proxyResolver func(domain.Channel) string
	// events receives operator-facing notifications (nil = disabled).
	events  func(notify.Event)
	bg      sync.WaitGroup
	mu      sync.RWMutex
	stopCh  chan struct{}
	stopped chan struct{}
	running bool
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
	if cfg.BalanceInterval <= 0 {
		cfg.BalanceInterval = time.Hour
	}
	return &Scheduler{
		cfg:           cfg,
		adapters:      reg,
		repo:          repo,
		states:        make(map[string]JobState),
		channelMu:     make(map[string]*sync.Mutex),
		detector:      browser.Detect,
		syncNextAt:    make(map[string]time.Time),
		balanceNextAt: make(map[string]time.Time),
		stopCh:        make(chan struct{}),
		stopped:       make(chan struct{}),
	}
}

// SetEventSink installs the notification sink for check-in failures, manual
// fallbacks and balance refresh failures.
func (s *Scheduler) SetEventSink(fn func(notify.Event)) {
	s.mu.Lock()
	s.events = fn
	s.mu.Unlock()
}

func (s *Scheduler) emit(ev notify.Event) {
	s.mu.RLock()
	fn := s.events
	s.mu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}

// SetProxyResolver installs the per-channel egress resolver used for browser
// check-ins (see internal/egress.Selector.ProxyFor).
func (s *Scheduler) SetProxyResolver(fn func(domain.Channel) string) {
	s.mu.Lock()
	s.proxyResolver = fn
	s.mu.Unlock()
}

// SetQuotaObserver installs the callback invoked after every successful
// balance refresh (from a check-in follow-up or the periodic poll).
func (s *Scheduler) SetQuotaObserver(fn func(domain.Channel, domain.QuotaSnapshot)) {
	s.mu.Lock()
	s.quotaObserver = fn
	s.mu.Unlock()
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
	s.pollBalancesFor(ctx, channels, now)
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
		return s.failNeedManual(ctx, ch, "manual checkin required by channel mode")
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
			proxyResolver := s.proxyResolver
			s.mu.RUnlock()
			var bInfo browser.Info
			if detector != nil {
				bInfo = detector()
			}

			// If a browser executor is configured and browser is available, attempt CDP execution
			if executor != nil && bInfo.Available {
				targetURL := ch.BaseURL
				proxyURL := ""
				if proxyResolver != nil {
					proxyURL = proxyResolver(ch)
				}
				execRes, execErr := executor.ExecuteCheckin(ctx, browser.CheckinRequest{
					URL:        targetURL,
					ProviderID: provider.ID,
					ChannelID:  ch.ID,
					Timeout:    30 * time.Second,
					ProxyURL:   proxyURL,
					Selectors:  browser.SelectorsFromCapabilities(provider.Capabilities),
				})
				if execErr == nil && execRes.Success {
					// The DOM said "success". That is a hint, not evidence:
					// confirm against the site's own record when the adapter
					// can, and fail closed when it cannot.
					verified, vErr := s.verifyBrowserCheckin(ctx, adp, ch, execRes)
					if vErr != nil {
						manualReason := fmt.Sprintf("browser reported success but server-side verification failed (%v): confirm manually", vErr)
						return s.failNeedManual(ctx, ch, manualReason)
					}
					result = verified
					if verified.Success {
						lastErr = nil
						break
					}
					lastErr = fmt.Errorf("browser reported success but the site has no record of today's check-in")
					break
				}
				// If CDP execution encountered turnstile interactive challenge or timeout,
				// fail closed and downgrade to manual.
				manualReason := "turnstile challenge interactive or failed: manual checkin required"
				if execErr != nil {
					manualReason = fmt.Sprintf("turnstile verification failed (%v): manual checkin required", execErr)
				}
				return s.failNeedManual(ctx, ch, manualReason)
			}

			// Fail closed until a browser executor can provide server evidence.
			manualReason := "turnstile verification required: browser executor is not configured, manual checkin required"
			if !bInfo.Available {
				manualReason = "turnstile verification required: no browser runtime available, manual checkin required"
			}
			return s.failNeedManual(ctx, ch, manualReason)
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
		if strings.Contains(result.Message, "unverified") {
			rec.ErrorMessage = result.Message
		}
	} else {
		rec.Status = "failed"
		if lastErr != nil {
			rec.ErrorMessage = lastErr.Error()
		} else {
			rec.ErrorMessage = result.Message
			lastErr = fmt.Errorf("check-in failed: %s", result.Message)
		}
	}
	if err := s.repo.CreateCheckinRecord(ctx, rec); err != nil {
		return s.recordFailure(channelID, err)
	}

	if lastErr != nil {
		s.emit(notify.Event{
			Kind:      notify.KindCheckinFailed,
			Severity:  notify.SeverityError,
			Title:     fmt.Sprintf("签到失败：%s", channelLabel(ch)),
			Body:      rec.ErrorMessage,
			ChannelID: channelID,
			At:        now,
		})
		return lastErr
	}
	// A successful (or already-done) check-in is the moment the balance most
	// likely changed: refresh it so routing sees the new quota immediately.
	// Balance problems are surfaced on the job state, never turned into a
	// check-in failure.
	_, _ = s.refreshBalance(ctx, ch, adp, "checkin", time.Now().UTC())
	return nil
}

// RunBalanceNow refreshes one channel's balance on demand.
func (s *Scheduler) RunBalanceNow(ctx context.Context, channelID string) (domain.QuotaSnapshot, error) {
	if s.repo == nil {
		return domain.QuotaSnapshot{}, fmt.Errorf("repository not configured")
	}
	ch, err := s.repo.GetChannel(ctx, channelID)
	if err != nil {
		return domain.QuotaSnapshot{}, err
	}
	provider, err := s.repo.GetProvider(ctx, ch.ProviderID)
	if err != nil {
		return domain.QuotaSnapshot{}, err
	}
	adp, err := s.adapters.Resolve(provider)
	if err != nil {
		return domain.QuotaSnapshot{}, err
	}
	return s.refreshBalance(ctx, ch, adp, "manual", time.Now().UTC())
}

// pollBalances runs the periodic balance refresh for every enabled channel
// whose adapter supports it. Exposed for tests; the loop calls
// pollBalancesFor with the channel list it already fetched.
func (s *Scheduler) pollBalances(ctx context.Context, now time.Time) {
	if s.repo == nil {
		return
	}
	channels, err := s.repo.ListChannels(ctx, "")
	if err != nil {
		return
	}
	s.pollBalancesFor(ctx, channels, now)
}

func (s *Scheduler) pollBalancesFor(ctx context.Context, channels []domain.Channel, now time.Time) {
	if s.adapters == nil {
		return
	}
	for _, ch := range channels {
		if !ch.Enabled || ch.ProviderID == "" {
			continue
		}
		s.mu.Lock()
		next, ok := s.balanceNextAt[ch.ID]
		if ok && now.Before(next) {
			s.mu.Unlock()
			continue
		}
		s.balanceNextAt[ch.ID] = now.Add(s.cfg.BalanceInterval)
		s.mu.Unlock()

		provider, err := s.repo.GetProvider(ctx, ch.ProviderID)
		if err != nil || !provider.Enabled {
			continue
		}
		adp, err := s.adapters.Resolve(provider)
		if err != nil {
			continue
		}
		s.bg.Add(1)
		go func(ch domain.Channel, adp adapter.ProviderAdapter) {
			defer s.bg.Done()
			if _, err := s.refreshBalance(ctx, ch, adp, "balance", now); errors.Is(err, adapter.ErrUnsupportedOperation) {
				s.mu.Lock()
				s.balanceNextAt[ch.ID] = now.Add(unsupportedBalanceRetry)
				s.mu.Unlock()
			}
		}(ch, adp)
	}
}

// waitBackground blocks until in-flight balance refreshes finish (tests).
func (s *Scheduler) waitBackground() { s.bg.Wait() }

// refreshBalance fetches the balance, persists the structured snapshot on the
// channel, mirrors it onto the job state and notifies the quota observer.
func (s *Scheduler) refreshBalance(ctx context.Context, ch domain.Channel, adp adapter.ProviderAdapter, source string, now time.Time) (domain.QuotaSnapshot, error) {
	if adp == nil {
		return domain.QuotaSnapshot{}, adapter.ErrUnsupportedOperation
	}
	bctx, cancel := context.WithTimeout(ctx, balanceTimeout)
	defer cancel()
	res, err := adp.Balance(bctx, ch)
	if err != nil {
		if !errors.Is(err, adapter.ErrUnsupportedOperation) {
			prev := s.GetState(ch.ID)
			s.updateState(ch.ID, func(st *JobState) {
				st.ChannelID = ch.ID
				st.LastBalanceError = err.Error()
			})
			// Notify once per outage: only when the previous refresh worked.
			if prev.LastBalanceError == "" && !prev.LastBalanceAt.IsZero() {
				s.emit(notify.Event{
					Kind:      notify.KindBalanceFailed,
					Severity:  notify.SeverityWarning,
					Title:     fmt.Sprintf("余额刷新失败：%s", channelLabel(ch)),
					Body:      err.Error(),
					ChannelID: ch.ID,
					At:        now,
				})
			}
		}
		return domain.QuotaSnapshot{}, err
	}
	snap := domain.QuotaSnapshot{
		AvailableUSD: res.AvailableUSD,
		UsedUSD:      res.UsedUSD,
		TodayUsedUSD: res.TodayUsedUSD,
		Remaining:    res.Remaining,
		Total:        res.Total,
		UpdatedAt:    now,
		Source:       source,
		Username:     res.Username,
	}
	if !res.ResetAt.IsZero() {
		reset := res.ResetAt
		snap.ResetAt = &reset
	}
	// Persist against the freshest copy of the channel so a concurrent edit
	// of unrelated fields is not undone.
	if s.repo != nil {
		if fresh, gErr := s.repo.GetChannel(ctx, ch.ID); gErr == nil {
			fresh.QuotaState = snap.Encode()
			if uErr := s.repo.UpdateChannel(ctx, fresh); uErr != nil {
				s.updateState(ch.ID, func(st *JobState) {
					st.ChannelID = ch.ID
					st.LastBalanceError = "persist balance: " + uErr.Error()
				})
			}
			ch = fresh
		}
	}
	s.updateState(ch.ID, func(st *JobState) {
		st.ChannelID = ch.ID
		st.QuotaUSD = snap.AvailableUSD
		st.LastBalanceAt = now
		st.LastBalanceError = ""
	})
	s.mu.RLock()
	obs := s.quotaObserver
	s.mu.RUnlock()
	if obs != nil {
		obs(ch, snap)
	}
	return snap, nil
}

// verifyBrowserCheckin turns a browser-side success into a CheckInResult
// backed by server evidence. Adapters without a verifier keep the browser
// result but it is explicitly labelled unverified.
func (s *Scheduler) verifyBrowserCheckin(ctx context.Context, adp adapter.ProviderAdapter, ch domain.Channel, execRes browser.CheckinResult) (adapter.CheckInResult, error) {
	verifier, ok := adp.(adapter.CheckinVerifier)
	if !ok {
		return adapter.CheckInResult{
			Success: true,
			Reward:  execRes.Reward,
			Message: "checkin succeeded via cdp automation (unverified: adapter has no server-side verifier)",
		}, nil
	}
	vctx, cancel := context.WithTimeout(ctx, balanceTimeout)
	defer cancel()
	v, err := verifier.VerifyCheckin(vctx, ch)
	if err != nil {
		return adapter.CheckInResult{}, err
	}
	if !v.CheckedIn {
		return adapter.CheckInResult{Success: false, Message: v.Message}, nil
	}
	msg := "checkin confirmed by server record"
	if v.Message != "" {
		msg = "checkin confirmed: " + v.Message
	}
	return adapter.CheckInResult{
		Success:     true,
		Reward:      v.Reward,
		RewardUSD:   v.RewardUSD,
		RewardKnown: v.RewardKnown,
		Message:     msg,
	}, nil
}

// failNeedManual records a need_manual outcome with the given reason and
// notifies the operator: a check-in that needs a human is only useful if the
// human hears about it.
func (s *Scheduler) failNeedManual(ctx context.Context, ch domain.Channel, reason string) error {
	channelID := ch.ID
	now := time.Now().UTC()
	jitter := time.Duration(rand.Int63n(int64(s.cfg.RandomJitter)))
	nextRun := now.Add(s.cfg.Interval).Add(jitter)
	s.emit(notify.Event{
		Kind:      notify.KindNeedManual,
		Severity:  notify.SeverityWarning,
		Title:     fmt.Sprintf("需要手动签到：%s", channelLabel(ch)),
		Body:      reason,
		ChannelID: channelID,
		At:        now,
	})
	s.updateState(channelID, func(st *JobState) {
		st.ChannelID = channelID
		st.LastRunAt = now
		st.NextRunAt = nextRun
		st.Status = StatusNeedManual
		st.LastError = reason
	})
	rec := domain.CheckinRecord{
		ID:           uuid.NewString(),
		ChannelID:    channelID,
		StartedAt:    now,
		FinishedAt:   &now,
		Status:       "need_manual",
		ErrorMessage: reason,
	}
	if err := s.repo.CreateCheckinRecord(ctx, rec); err != nil {
		return s.recordFailure(channelID, err)
	}
	return ErrNeedManual
}

func channelLabel(ch domain.Channel) string {
	if strings.TrimSpace(ch.Name) != "" {
		return ch.Name
	}
	return ch.ID
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
