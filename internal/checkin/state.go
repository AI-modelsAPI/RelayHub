package checkin

import "time"

type JobStatus string

const (
	StatusIdle              JobStatus = "idle"
	StatusRunning           JobStatus = "running"
	StatusSuccess           JobStatus = "success"
	StatusFailed            JobStatus = "failed"
	StatusCooldown          JobStatus = "cooldown"
	StatusNeedManual        JobStatus = "need_manual"
	StatusPersistenceFailed JobStatus = "persistence_failed"
)

type JobState struct {
	ChannelID       string    `json:"channel_id"`
	Status          JobStatus `json:"status"`
	ExecutionStatus JobStatus `json:"execution_status,omitempty"`
	LastRunAt       time.Time `json:"last_run_at"`
	NextRunAt       time.Time `json:"next_run_at"`
	LastReward      string    `json:"last_reward"`
	LastError       string    `json:"last_error"`
	FailureCount    int       `json:"failure_count"`
	// Balance observation attached to the job so the UI can show "signed in,
	// $1.50 left" without a second round-trip. QuotaUSD is 0 when unknown;
	// check LastBalanceAt to distinguish "unknown" from "empty".
	QuotaUSD         float64   `json:"quota_usd"`
	LastBalanceAt    time.Time `json:"last_balance_at,omitempty"`
	LastBalanceError string    `json:"last_balance_error,omitempty"`
}
