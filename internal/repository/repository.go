package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"relayhub/internal/domain"
	"strings"
	"time"
)

var (
	ErrNotFound           = sql.ErrNoRows
	ErrProviderReferenced = errors.New("provider has referenced channels")
)

type ProviderRepository interface {
	CreateProvider(context.Context, domain.Provider) error
	GetProvider(context.Context, string) (domain.Provider, error)
	ListProviders(context.Context) ([]domain.Provider, error)
	UpdateProvider(context.Context, domain.Provider) error
	DeleteProvider(context.Context, string) error
}

type ChannelRepository interface {
	CreateChannel(context.Context, domain.Channel) error
	GetChannel(context.Context, string) (domain.Channel, error)
	ListChannels(context.Context, string) ([]domain.Channel, error)
	UpdateChannel(context.Context, domain.Channel) error
	DeleteChannel(context.Context, string) error
	CreateChannelKey(context.Context, domain.ChannelKey) error
	GetChannelKey(context.Context, string) (domain.ChannelKey, error)
	ListChannelKeys(context.Context, string) ([]domain.ChannelKey, error)
	UpdateChannelKey(context.Context, domain.ChannelKey) error
	DeleteChannelKey(context.Context, string) error
}

type ModelRepository interface {
	CreateModel(context.Context, domain.Model) error
	GetModel(context.Context, string) (domain.Model, error)
	ListModels(context.Context) ([]domain.Model, error)
	UpdateModel(context.Context, domain.Model) error
	DeleteModel(context.Context, string) error
}

type ProviderModelRepository interface {
	CreateProviderModel(context.Context, domain.ProviderModel) error
	GetProviderModel(context.Context, string) (domain.ProviderModel, error)
	ListProviderModels(context.Context, string) ([]domain.ProviderModel, error)
	UpdateProviderModel(context.Context, domain.ProviderModel) error
	DeleteProviderModel(context.Context, string) error
	DeleteProviderModelsByChannel(context.Context, string) error
}

// ModelSyncRepository stores the before-image of each model sync so a sync can
// be rolled back in one step (AUDIT 2026-09-24 §5 B3).
type ModelSyncRepository interface {
	CreateModelSyncSnapshot(context.Context, domain.ModelSyncSnapshot) error
	LastModelSyncSnapshot(context.Context, string) (domain.ModelSyncSnapshot, error)
	MarkModelSyncSnapshotUndone(context.Context, string) error
}

type ModelGroupRepository interface {
	CreateModelGroup(context.Context, domain.ModelGroup) error
	GetModelGroup(context.Context, string) (domain.ModelGroup, error)
	ListModelGroups(context.Context) ([]domain.ModelGroup, error)
	UpdateModelGroup(context.Context, domain.ModelGroup) error
	DeleteModelGroup(context.Context, string) error
	AddModelGroupMember(context.Context, domain.ModelGroupMember) error
	ListModelGroupMembers(context.Context, string) ([]domain.ModelGroupMember, error)
	RemoveModelGroupMember(context.Context, string, string) error
}

type RouteRepository interface {
	CreateRoute(context.Context, domain.Route) error
	GetRoute(context.Context, string) (domain.Route, error)
	ListRoutes(context.Context) ([]domain.Route, error)
	UpdateRoute(context.Context, domain.Route) error
	DeleteRoute(context.Context, string) error
}

type StatusRepository interface {
	CreateCheckinRecord(context.Context, domain.CheckinRecord) error
	GetCheckinRecord(context.Context, string) (domain.CheckinRecord, error)
	ListCheckinRecords(context.Context, string) ([]domain.CheckinRecord, error)
	ListCheckinRecordsMulti(context.Context, []string, int) ([]domain.CheckinRecord, error)
	CreateHealthRecord(context.Context, domain.HealthRecord) error
	GetHealthRecord(context.Context, string) (domain.HealthRecord, error)
	ListHealthRecords(context.Context, string) ([]domain.HealthRecord, error)
	CreateRequestRecord(context.Context, domain.RequestRecord) error
	GetRequestRecord(context.Context, string) (domain.RequestRecord, error)
	ListRequestRecords(context.Context) ([]domain.RequestRecord, error)
	ListRequestRecordsLimit(context.Context, int) ([]domain.RequestRecord, error)
	ListRequestRecordsByChannel(context.Context, string, int) ([]domain.RequestRecord, error)
	RequestUsageTotals(context.Context) (int64, int64, int64, error)
	PruneRequestRecords(context.Context, time.Time) (int64, error)
	CreateBillingReport(context.Context, domain.BillingReport) error
	ListBillingReports(context.Context, string, int) ([]domain.BillingReport, error)
	CreateCLISyncRecord(context.Context, domain.CLISyncRecord) error
	GetCLISyncRecord(context.Context, string) (domain.CLISyncRecord, error)
	ListCLISyncRecords(context.Context) ([]domain.CLISyncRecord, error)
	CreateConfigBackup(context.Context, domain.ConfigBackup) error
	GetConfigBackup(context.Context, string) (domain.ConfigBackup, error)
	ListConfigBackups(context.Context) ([]domain.ConfigBackup, error)
}

type ResourceRepository interface {
	ProviderRepository
	ChannelRepository
	ModelRepository
	ProviderModelRepository
	ModelSyncRepository
	ModelGroupRepository
	RouteRepository
	StatusRepository
}

type Store struct {
	DB   *sql.DB
	exec executor
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Tx struct{ Store }

func New(db *sql.DB) *Store { return &Store{DB: db, exec: db} }

func (s *Store) repositoryExecutor() executor {
	if s.exec != nil {
		return s.exec
	}
	return s.DB
}

func stamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func scanTimePair(created, updated string, dstCreated, dstUpdated *time.Time) error {
	var err error
	if *dstCreated, err = parseTime(created); err != nil {
		return err
	}
	if *dstUpdated, err = parseTime(updated); err != nil {
		return err
	}
	return nil
}

func scanProvider(row interface{ Scan(...any) error }) (domain.Provider, error) {
	var p domain.Provider
	var enabled, checkin int
	var created, updated string
	err := row.Scan(&p.ID, &p.Name, &p.AdapterType, &p.Protocol, &p.BaseURLTemplate, &p.Capabilities, &checkin, &p.AdapterVersion, &enabled, &created, &updated)
	if err != nil {
		return p, err
	}
	p.CheckinEnabled, p.Enabled = checkin != 0, enabled != 0
	return p, scanTimePair(created, updated, &p.CreatedAt, &p.UpdatedAt)
}

func (s *Store) CreateProvider(ctx context.Context, p domain.Provider) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	created := p.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO providers(id,name,adapter_type,protocol,base_url_template,capabilities,checkin_enabled,adapter_version,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.Name, p.AdapterType, p.Protocol, p.BaseURLTemplate, p.Capabilities, boolInt(p.CheckinEnabled), p.AdapterVersion, boolInt(p.Enabled), stamp(created), stamp(created))
	return err
}

func (s *Store) GetProvider(ctx context.Context, id string) (domain.Provider, error) {
	return scanProvider(s.repositoryExecutor().QueryRowContext(ctx, `SELECT id,name,adapter_type,protocol,base_url_template,capabilities,checkin_enabled,adapter_version,enabled,created_at,updated_at FROM providers WHERE id=?`, id))
}

func (s *Store) ListProviders(ctx context.Context) ([]domain.Provider, error) {
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT id,name,adapter_type,protocol,base_url_template,capabilities,checkin_enabled,adapter_version,enabled,created_at,updated_at FROM providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProvider(ctx context.Context, p domain.Provider) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := s.repositoryExecutor().ExecContext(ctx, `UPDATE providers SET name=?,adapter_type=?,protocol=?,base_url_template=?,capabilities=?,checkin_enabled=?,adapter_version=?,enabled=?,updated_at=? WHERE id=?`, p.Name, p.AdapterType, p.Protocol, p.BaseURLTemplate, p.Capabilities, boolInt(p.CheckinEnabled), p.AdapterVersion, boolInt(p.Enabled), stamp(time.Now()), p.ID)
	return requireAffected(result, err)
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	var count int
	if err := s.repositoryExecutor().QueryRowContext(ctx, `SELECT COUNT(*) FROM channels WHERE provider_id=?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrProviderReferenced
	}
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
	return requireAffected(result, err)
}

func scanChannel(row interface{ Scan(...any) error }) (domain.Channel, error) {
	var c domain.Channel
	var checkin, routing, enabled, autoSync int
	var created, updated, customHeaders string
	err := row.Scan(&c.ID, &c.ProviderID, &c.Name, &c.BaseURL, &c.CredentialRef, &c.AccountRef, &c.RoutingTags, &c.QuotaState, &c.HealthState, &c.RateLimitState, &c.Priority, &c.Weight, &checkin, &routing, &enabled, &c.Status, &c.ManualModels, &autoSync, &c.AutoSyncPattern, &c.DefaultTestModel, &c.StreamPolicy, &c.ErrorMessage, &c.Remark, &c.ProxyURL, &c.CheckinMode, &customHeaders, &created, &updated)
	if err != nil {
		return c, err
	}
	c.CheckinEnabled, c.RoutingEnabled, c.Enabled, c.AutoSync = checkin != 0, routing != 0, enabled != 0, autoSync != 0
	if customHeaders != "" {
		_ = json.Unmarshal([]byte(customHeaders), &c.CustomHeaders)
	}
	if c.Status == "" {
		if c.Enabled {
			c.Status = "enabled"
		} else {
			c.Status = "disabled"
		}
	}
	if c.CheckinMode == "" {
		c.CheckinMode = "auto"
	}
	return c, scanTimePair(created, updated, &c.CreatedAt, &c.UpdatedAt)
}

const channelColumns = `id,provider_id,name,base_url,credential_ref,account_ref,routing_tags,quota_state,health_state,rate_limit_state,priority,weight,checkin_enabled,routing_enabled,enabled,status,manual_models,auto_sync,auto_sync_pattern,default_test_model,stream_policy,error_message,remark,proxy_url,checkin_mode,custom_headers,created_at,updated_at`

// jsonOrEmpty encodes a header map for the custom_headers column.
func jsonOrEmpty(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Store) CreateChannel(ctx context.Context, c domain.Channel) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.Weight <= 0 {
		c.Weight = 1
	}
	created := c.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if c.Status == "" {
		if c.Enabled {
			c.Status = "enabled"
		} else {
			c.Status = "disabled"
		}
	}
	if c.CheckinMode == "" {
		c.CheckinMode = "auto"
	}
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO channels(`+channelColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ProviderID, c.Name, c.BaseURL, c.CredentialRef, c.AccountRef, c.RoutingTags, c.QuotaState, c.HealthState, c.RateLimitState, c.Priority, c.Weight, boolInt(c.CheckinEnabled), boolInt(c.RoutingEnabled), boolInt(c.Enabled), c.Status, c.ManualModels, boolInt(c.AutoSync), c.AutoSyncPattern, c.DefaultTestModel, c.StreamPolicy, c.ErrorMessage, c.Remark, c.ProxyURL, c.CheckinMode, jsonOrEmpty(c.CustomHeaders), stamp(created), stamp(created))
	return err
}
func (s *Store) GetChannel(ctx context.Context, id string) (domain.Channel, error) {
	return scanChannel(s.repositoryExecutor().QueryRowContext(ctx, `SELECT `+channelColumns+` FROM channels WHERE id=?`, id))
}
func (s *Store) ListChannels(ctx context.Context, providerID string) ([]domain.Channel, error) {
	query, args := `SELECT `+channelColumns+` FROM channels ORDER BY id`, []any{}
	if providerID != "" {
		query, args = `SELECT `+channelColumns+` FROM channels WHERE provider_id=? ORDER BY id`, []any{providerID}
	}
	rows, err := s.repositoryExecutor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) UpdateChannel(ctx context.Context, c domain.Channel) error {
	if c.Weight <= 0 {
		c.Weight = 1
	}
	if c.Status == "" {
		if c.Enabled {
			c.Status = "enabled"
		} else {
			c.Status = "disabled"
		}
	}
	if c.CheckinMode == "" {
		c.CheckinMode = "auto"
	}
	result, err := s.repositoryExecutor().ExecContext(ctx, `UPDATE channels SET provider_id=?,name=?,base_url=?,credential_ref=?,account_ref=?,routing_tags=?,quota_state=?,health_state=?,rate_limit_state=?,priority=?,weight=?,checkin_enabled=?,routing_enabled=?,enabled=?,status=?,manual_models=?,auto_sync=?,auto_sync_pattern=?,default_test_model=?,stream_policy=?,error_message=?,remark=?,proxy_url=?,checkin_mode=?,custom_headers=?,updated_at=? WHERE id=?`,
		c.ProviderID, c.Name, c.BaseURL, c.CredentialRef, c.AccountRef, c.RoutingTags, c.QuotaState, c.HealthState, c.RateLimitState, c.Priority, c.Weight, boolInt(c.CheckinEnabled), boolInt(c.RoutingEnabled), boolInt(c.Enabled), c.Status, c.ManualModels, boolInt(c.AutoSync), c.AutoSyncPattern, c.DefaultTestModel, c.StreamPolicy, c.ErrorMessage, c.Remark, c.ProxyURL, c.CheckinMode, jsonOrEmpty(c.CustomHeaders), stamp(time.Now()), c.ID)
	return requireAffected(result, err)
}
func (s *Store) DeleteChannel(ctx context.Context, id string) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM channels WHERE id=?`, id)
	return requireAffected(result, err)
}

const channelKeyColumns = `id,channel_id,secret_ref,label,disabled,created_at`

func scanChannelKey(row interface{ Scan(...any) error }) (domain.ChannelKey, error) {
	var k domain.ChannelKey
	var disabled int
	var created string
	if err := row.Scan(&k.ID, &k.ChannelID, &k.SecretRef, &k.Label, &disabled, &created); err != nil {
		return k, err
	}
	k.Disabled = disabled != 0
	t, err := parseTime(created)
	if err != nil {
		return k, err
	}
	k.CreatedAt = t
	return k, nil
}

func (s *Store) CreateChannelKey(ctx context.Context, k domain.ChannelKey) error {
	created := k.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO channel_keys(`+channelKeyColumns+`) VALUES(?,?,?,?,?,?)`,
		k.ID, k.ChannelID, k.SecretRef, k.Label, boolInt(k.Disabled), stamp(created))
	return err
}
func (s *Store) GetChannelKey(ctx context.Context, id string) (domain.ChannelKey, error) {
	return scanChannelKey(s.repositoryExecutor().QueryRowContext(ctx, `SELECT `+channelKeyColumns+` FROM channel_keys WHERE id=?`, id))
}
func (s *Store) ListChannelKeys(ctx context.Context, channelID string) ([]domain.ChannelKey, error) {
	query, args := `SELECT `+channelKeyColumns+` FROM channel_keys ORDER BY created_at, id`, []any{}
	if channelID != "" {
		query, args = `SELECT `+channelKeyColumns+` FROM channel_keys WHERE channel_id=? ORDER BY created_at, id`, []any{channelID}
	}
	rows, err := s.repositoryExecutor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ChannelKey
	for rows.Next() {
		k, err := scanChannelKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s *Store) UpdateChannelKey(ctx context.Context, k domain.ChannelKey) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `UPDATE channel_keys SET label=?,disabled=? WHERE id=?`,
		k.Label, boolInt(k.Disabled), k.ID)
	return requireAffected(result, err)
}

// RebindChannelKeySecret points a channel key at a new secret ref. It is
// deliberately not part of ResourceRepository: only the startup origin
// binding migration may move key material (AUDIT 2026-09-24 F5).
func (s *Store) RebindChannelKeySecret(ctx context.Context, id, secretRef string) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `UPDATE channel_keys SET secret_ref=? WHERE id=?`, secretRef, id)
	return requireAffected(result, err)
}

func (s *Store) DeleteChannelKey(ctx context.Context, id string) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM channel_keys WHERE id=?`, id)
	return requireAffected(result, err)
}

func encodeAliases(aliases []string) (string, error) {
	if aliases == nil {
		aliases = []string{}
	}
	b, err := json.Marshal(aliases)
	return string(b), err
}
func decodeAliases(raw string) ([]string, error) {
	var a []string
	if raw == "" {
		return a, nil
	}
	return a, json.Unmarshal([]byte(raw), &a)
}
func scanModel(row interface{ Scan(...any) error }) (domain.Model, error) {
	var m domain.Model
	var aliases string
	var reasoning, vision, tools, enabled, archived int
	var created, updated string
	err := row.Scan(&m.ID, &m.DisplayName, &aliases, &m.Family, &m.ProtocolRequirements, &m.Capabilities, &m.ContextWindow, &m.MaxOutputTokens, &reasoning, &vision, &tools, &enabled, &m.Developer, &m.ModelType, &m.InputPrice, &m.OutputPrice, &m.IconURL, &archived, &created, &updated)
	if err != nil {
		return m, err
	}
	m.Aliases, err = decodeAliases(aliases)
	if err != nil {
		return m, err
	}
	m.ReasoningSupport, m.VisionSupport, m.ToolCallSupport, m.Enabled, m.Archived = reasoning != 0, vision != 0, tools != 0, enabled != 0, archived != 0
	err = scanTimePair(created, updated, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

const modelColumns = `id,display_name,aliases_json,family,protocol_requirements,capabilities,context_window,max_output_tokens,reasoning_support,vision_support,tool_call_support,enabled,developer,model_type,input_price,output_price,icon_url,archived,created_at,updated_at`

func (s *Store) CreateModel(ctx context.Context, m domain.Model) error {
	aliases, err := encodeAliases(m.Aliases)
	if err != nil {
		return err
	}
	created := m.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err = s.repositoryExecutor().ExecContext(ctx, `INSERT INTO models(`+modelColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.DisplayName, aliases, m.Family, m.ProtocolRequirements, m.Capabilities, m.ContextWindow, m.MaxOutputTokens, boolInt(m.ReasoningSupport), boolInt(m.VisionSupport), boolInt(m.ToolCallSupport), boolInt(m.Enabled), m.Developer, m.ModelType, m.InputPrice, m.OutputPrice, m.IconURL, boolInt(m.Archived), stamp(created), stamp(created))
	return err
}
func (s *Store) GetModel(ctx context.Context, id string) (domain.Model, error) {
	return scanModel(s.repositoryExecutor().QueryRowContext(ctx, `SELECT `+modelColumns+` FROM models WHERE id=?`, id))
}
func (s *Store) ListModels(ctx context.Context) ([]domain.Model, error) {
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT `+modelColumns+` FROM models ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) UpdateModel(ctx context.Context, m domain.Model) error {
	aliases, err := encodeAliases(m.Aliases)
	if err != nil {
		return err
	}
	result, err := s.repositoryExecutor().ExecContext(ctx, `UPDATE models SET display_name=?,aliases_json=?,family=?,protocol_requirements=?,capabilities=?,context_window=?,max_output_tokens=?,reasoning_support=?,vision_support=?,tool_call_support=?,enabled=?,developer=?,model_type=?,input_price=?,output_price=?,icon_url=?,archived=?,updated_at=? WHERE id=?`, m.DisplayName, aliases, m.Family, m.ProtocolRequirements, m.Capabilities, m.ContextWindow, m.MaxOutputTokens, boolInt(m.ReasoningSupport), boolInt(m.VisionSupport), boolInt(m.ToolCallSupport), boolInt(m.Enabled), m.Developer, m.ModelType, m.InputPrice, m.OutputPrice, m.IconURL, boolInt(m.Archived), stamp(time.Now()), m.ID)
	return requireAffected(result, err)
}
func (s *Store) DeleteModel(ctx context.Context, id string) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM models WHERE id=?`, id)
	return requireAffected(result, err)
}

func scanProviderModel(row interface{ Scan(...any) error }) (domain.ProviderModel, error) {
	var m domain.ProviderModel
	var channel sql.NullString
	var enabled int
	var created, updated string
	err := row.Scan(&m.ID, &m.ProviderID, &channel, &m.ModelID, &m.UpstreamModelName, &m.Protocol, &m.RequestTransform, &m.ResponseTransform, &m.Priority, &m.Weight, &enabled, &created, &updated)
	if err != nil {
		return m, err
	}
	if channel.Valid {
		m.ChannelID = channel.String
	}
	m.Enabled = enabled != 0
	err = scanTimePair(created, updated, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

const providerModelColumns = `id,provider_id,channel_id,model_id,upstream_model_name,protocol,request_transform,response_transform,priority,weight,enabled,created_at,updated_at`

func (s *Store) CreateProviderModel(ctx context.Context, m domain.ProviderModel) error {
	if m.Weight <= 0 {
		m.Weight = 1
	}
	created := m.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	var channel any = m.ChannelID
	if m.ChannelID == "" {
		channel = nil
	}
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO provider_models(`+providerModelColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.ID, m.ProviderID, channel, m.ModelID, m.UpstreamModelName, m.Protocol, m.RequestTransform, m.ResponseTransform, m.Priority, m.Weight, boolInt(m.Enabled), stamp(created), stamp(created))
	return err
}
func (s *Store) GetProviderModel(ctx context.Context, id string) (domain.ProviderModel, error) {
	return scanProviderModel(s.repositoryExecutor().QueryRowContext(ctx, `SELECT `+providerModelColumns+` FROM provider_models WHERE id=?`, id))
}
func (s *Store) ListProviderModels(ctx context.Context, modelID string) ([]domain.ProviderModel, error) {
	query, args := `SELECT `+providerModelColumns+` FROM provider_models ORDER BY id`, []any{}
	if modelID != "" {
		query, args = `SELECT `+providerModelColumns+` FROM provider_models WHERE model_id=? ORDER BY id`, []any{modelID}
	}
	rows, err := s.repositoryExecutor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ProviderModel
	for rows.Next() {
		m, err := scanProviderModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) UpdateProviderModel(ctx context.Context, m domain.ProviderModel) error {
	if m.Weight <= 0 {
		m.Weight = 1
	}
	var channel any = m.ChannelID
	if m.ChannelID == "" {
		channel = nil
	}
	result, err := s.repositoryExecutor().ExecContext(ctx, `UPDATE provider_models SET provider_id=?,channel_id=?,model_id=?,upstream_model_name=?,protocol=?,request_transform=?,response_transform=?,priority=?,weight=?,enabled=?,updated_at=? WHERE id=?`, m.ProviderID, channel, m.ModelID, m.UpstreamModelName, m.Protocol, m.RequestTransform, m.ResponseTransform, m.Priority, m.Weight, boolInt(m.Enabled), stamp(time.Now()), m.ID)
	return requireAffected(result, err)
}
func (s *Store) DeleteProviderModel(ctx context.Context, id string) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM provider_models WHERE id=?`, id)
	return requireAffected(result, err)
}

// --- model sync snapshots (AUDIT 2026-09-24 §5 B3) ---

func (s *Store) CreateModelSyncSnapshot(context.Context, domain.ModelSyncSnapshot) error {
	return nil
}

func (s *Store) LastModelSyncSnapshot(context.Context, string) (domain.ModelSyncSnapshot, error) {
	return domain.ModelSyncSnapshot{}, ErrNotFound
}

func (s *Store) MarkModelSyncSnapshotUndone(context.Context, string) error {
	return nil
}

// DeleteProviderModelsByChannel removes every provider-model binding of one
// channel in a single statement. Used by model sync so the replace step can
// run inside one transaction (AUDIT RH-15).
func (s *Store) DeleteProviderModelsByChannel(ctx context.Context, channelID string) error {
	_, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM provider_models WHERE channel_id=?`, channelID)
	return err
}

func scanModelGroup(row interface{ Scan(...any) error }) (domain.ModelGroup, error) {
	var g domain.ModelGroup
	var enabled int
	var fallback sql.NullString
	var created, updated string
	err := row.Scan(&g.ID, &g.Name, &g.Description, &g.Strategy, &fallback, &enabled, &created, &updated)
	if err != nil {
		return g, err
	}
	if fallback.Valid {
		g.FallbackGroupID = fallback.String
	}
	g.Enabled = enabled != 0
	err = scanTimePair(created, updated, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

const groupColumns = `id,name,description,strategy,fallback_group_id,enabled,created_at,updated_at`

func (s *Store) CreateModelGroup(ctx context.Context, g domain.ModelGroup) error {
	created := g.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	var fallback any = g.FallbackGroupID
	if g.FallbackGroupID == "" {
		fallback = nil
	}
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO model_groups(`+groupColumns+`) VALUES(?,?,?,?,?,?,?,?)`, g.ID, g.Name, g.Description, g.Strategy, fallback, boolInt(g.Enabled), stamp(created), stamp(created))
	return err
}
func (s *Store) GetModelGroup(ctx context.Context, id string) (domain.ModelGroup, error) {
	return scanModelGroup(s.repositoryExecutor().QueryRowContext(ctx, `SELECT `+groupColumns+` FROM model_groups WHERE id=?`, id))
}
func (s *Store) ListModelGroups(ctx context.Context) ([]domain.ModelGroup, error) {
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT `+groupColumns+` FROM model_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ModelGroup
	for rows.Next() {
		g, err := scanModelGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) UpdateModelGroup(ctx context.Context, g domain.ModelGroup) error {
	var fallback any = g.FallbackGroupID
	if g.FallbackGroupID == "" {
		fallback = nil
	}
	result, err := s.repositoryExecutor().ExecContext(ctx, `UPDATE model_groups SET name=?,description=?,strategy=?,fallback_group_id=?,enabled=?,updated_at=? WHERE id=?`, g.Name, g.Description, g.Strategy, fallback, boolInt(g.Enabled), stamp(time.Now()), g.ID)
	return requireAffected(result, err)
}
func (s *Store) DeleteModelGroup(ctx context.Context, id string) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM model_groups WHERE id=?`, id)
	return requireAffected(result, err)
}
func (s *Store) AddModelGroupMember(ctx context.Context, m domain.ModelGroupMember) error {
	if m.Weight <= 0 {
		m.Weight = 1
	}
	if m.Weight <= 0 {
		m.Weight = 1
	}
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO model_group_members(group_id,model_id,priority,weight) VALUES(?,?,?,?)`, m.GroupID, m.ModelID, m.Priority, m.Weight)
	return err
}
func (s *Store) ListModelGroupMembers(ctx context.Context, groupID string) ([]domain.ModelGroupMember, error) {
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT group_id,model_id,priority,weight FROM model_group_members WHERE group_id=? ORDER BY priority,model_id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ModelGroupMember
	for rows.Next() {
		var m domain.ModelGroupMember
		if err := rows.Scan(&m.GroupID, &m.ModelID, &m.Priority, &m.Weight); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) RemoveModelGroupMember(ctx context.Context, groupID, modelID string) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM model_group_members WHERE group_id=? AND model_id=?`, groupID, modelID)
	return requireAffected(result, err)
}

func scanRoute(row interface{ Scan(...any) error }) (domain.Route, error) {
	var r domain.Route
	var group sql.NullString
	var enabled int
	var created, updated string
	err := row.Scan(&r.ID, &r.Name, &r.Protocol, &r.ModelPattern, &group, &r.ChannelFilter, &r.Strategy, &r.RetryPolicy, &r.FallbackPolicy, &enabled, &created, &updated)
	if err != nil {
		return r, err
	}
	if group.Valid {
		r.GroupID = group.String
	}
	r.Enabled = enabled != 0
	err = scanTimePair(created, updated, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

const routeColumns = `id,name,protocol,model_pattern,group_id,channel_filter,strategy,retry_policy,fallback_policy,enabled,created_at,updated_at`

func (s *Store) CreateRoute(ctx context.Context, r domain.Route) error {
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	var group any = r.GroupID
	if r.GroupID == "" {
		group = nil
	}
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO routes(`+routeColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Name, r.Protocol, r.ModelPattern, group, r.ChannelFilter, r.Strategy, r.RetryPolicy, r.FallbackPolicy, boolInt(r.Enabled), stamp(created), stamp(created))
	return err
}
func (s *Store) GetRoute(ctx context.Context, id string) (domain.Route, error) {
	return scanRoute(s.repositoryExecutor().QueryRowContext(ctx, `SELECT `+routeColumns+` FROM routes WHERE id=?`, id))
}
func (s *Store) ListRoutes(ctx context.Context) ([]domain.Route, error) {
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT `+routeColumns+` FROM routes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Route
	for rows.Next() {
		r, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) UpdateRoute(ctx context.Context, r domain.Route) error {
	var group any = r.GroupID
	if r.GroupID == "" {
		group = nil
	}
	result, err := s.repositoryExecutor().ExecContext(ctx, `UPDATE routes SET name=?,protocol=?,model_pattern=?,group_id=?,channel_filter=?,strategy=?,retry_policy=?,fallback_policy=?,enabled=?,updated_at=? WHERE id=?`, r.Name, r.Protocol, r.ModelPattern, group, r.ChannelFilter, r.Strategy, r.RetryPolicy, r.FallbackPolicy, boolInt(r.Enabled), stamp(time.Now()), r.ID)
	return requireAffected(result, err)
}
func (s *Store) DeleteRoute(ctx context.Context, id string) error {
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM routes WHERE id=?`, id)
	return requireAffected(result, err)
}

func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return stamp(*t)
}

func (s *Store) CreateCheckinRecord(ctx context.Context, r domain.CheckinRecord) error {
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO checkin_records(id,channel_id,started_at,finished_at,status,reward,error_code,error_message) VALUES(?,?,?,?,?,?,?,?)`, r.ID, r.ChannelID, stamp(r.StartedAt), nullableTime(r.FinishedAt), r.Status, r.Reward, r.ErrorCode, r.ErrorMessage)
	return err
}
func scanCheckin(row interface{ Scan(...any) error }) (domain.CheckinRecord, error) {
	var r domain.CheckinRecord
	var started string
	var finished sql.NullString
	err := row.Scan(&r.ID, &r.ChannelID, &started, &finished, &r.Status, &r.Reward, &r.ErrorCode, &r.ErrorMessage)
	if err != nil {
		return r, err
	}
	r.StartedAt, err = parseTime(started)
	if err != nil {
		return r, err
	}
	if finished.Valid {
		t, e := parseTime(finished.String)
		if e != nil {
			return r, e
		}
		r.FinishedAt = &t
	}
	return r, nil
}
func (s *Store) GetCheckinRecord(ctx context.Context, id string) (domain.CheckinRecord, error) {
	return scanCheckin(s.repositoryExecutor().QueryRowContext(ctx, `SELECT id,channel_id,started_at,finished_at,status,reward,error_code,error_message FROM checkin_records WHERE id=?`, id))
}
func (s *Store) ListCheckinRecords(ctx context.Context, channelID string) ([]domain.CheckinRecord, error) {
	// Bounded to recent history (AUDIT RH-32).
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT id,channel_id,started_at,finished_at,status,reward,error_code,error_message FROM checkin_records WHERE channel_id=? ORDER BY started_at DESC LIMIT 200`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CheckinRecord
	for rows.Next() {
		r, e := scanCheckin(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListCheckinRecordsMulti returns the newest records across the given channels
// in one query, replacing the per-channel N+1 loop in the management API
// (AUDIT RH-32). An empty channel list means "all channels".
func (s *Store) ListCheckinRecordsMulti(ctx context.Context, channelIDs []string, limit int) ([]domain.CheckinRecord, error) {
	if limit <= 0 {
		limit = 500
	}
	query := `SELECT id,channel_id,started_at,finished_at,status,reward,error_code,error_message FROM checkin_records`
	args := []any{}
	if len(channelIDs) > 0 {
		marks := strings.Repeat("?,", len(channelIDs))
		marks = marks[:len(marks)-1]
		query += ` WHERE channel_id IN (` + marks + `)`
		for _, id := range channelIDs {
			args = append(args, id)
		}
	}
	query += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.repositoryExecutor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CheckinRecord
	for rows.Next() {
		r, e := scanCheckin(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateHealthRecord(ctx context.Context, r domain.HealthRecord) error {
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO health_records(id,channel_id,checked_at,status,latency_ms,error_message) VALUES(?,?,?,?,?,?)`, r.ID, r.ChannelID, stamp(r.CheckedAt), r.Status, r.LatencyMS, r.ErrorMessage)
	return err
}
func scanHealth(row interface{ Scan(...any) error }) (domain.HealthRecord, error) {
	var r domain.HealthRecord
	var checked string
	err := row.Scan(&r.ID, &r.ChannelID, &checked, &r.Status, &r.LatencyMS, &r.ErrorMessage)
	if err != nil {
		return r, err
	}
	r.CheckedAt, err = parseTime(checked)
	return r, err
}
func (s *Store) GetHealthRecord(ctx context.Context, id string) (domain.HealthRecord, error) {
	return scanHealth(s.repositoryExecutor().QueryRowContext(ctx, `SELECT id,channel_id,checked_at,status,latency_ms,error_message FROM health_records WHERE id=?`, id))
}
func (s *Store) ListHealthRecords(ctx context.Context, channelID string) ([]domain.HealthRecord, error) {
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT id,channel_id,checked_at,status,latency_ms,error_message FROM health_records WHERE channel_id=? ORDER BY checked_at DESC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.HealthRecord
	for rows.Next() {
		r, e := scanHealth(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateRequestRecord(ctx context.Context, r domain.RequestRecord) error {
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO request_records(id,request_id,protocol,model_id,provider_id,channel_id,status_code,latency_ms,input_tokens,output_tokens,error_class,created_at,ttft_ms,cache_read_tokens,cache_write_tokens,finish_reason,upstream_model) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.RequestID, r.Protocol, nullString(r.ModelID), nullString(r.ProviderID), nullString(r.ChannelID), r.StatusCode, r.LatencyMS, r.InputTokens, r.OutputTokens, r.ErrorClass, stamp(r.CreatedAt), r.TTFTMS, r.CacheReadTokens, r.CacheWriteTokens, r.FinishReason, r.UpstreamModel)
	return err
}
func nullString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

const requestColumns = `id,request_id,protocol,model_id,provider_id,channel_id,status_code,latency_ms,input_tokens,output_tokens,error_class,created_at,ttft_ms,cache_read_tokens,cache_write_tokens,finish_reason,upstream_model`

func scanRequest(row interface{ Scan(...any) error }) (domain.RequestRecord, error) {
	var r domain.RequestRecord
	var model, provider, channel sql.NullString
	var created string
	// Migration 008 added these five columns as write-only: the SELECT lists and
	// this scanner only knew the old twelve, so everything read back was zero
	// (AUDIT RH-16 / P1-1).
	err := row.Scan(&r.ID, &r.RequestID, &r.Protocol, &model, &provider, &channel, &r.StatusCode, &r.LatencyMS, &r.InputTokens, &r.OutputTokens, &r.ErrorClass, &created, &r.TTFTMS, &r.CacheReadTokens, &r.CacheWriteTokens, &r.FinishReason, &r.UpstreamModel)
	if err != nil {
		return r, err
	}
	if model.Valid {
		r.ModelID = model.String
	}
	if provider.Valid {
		r.ProviderID = provider.String
	}
	if channel.Valid {
		r.ChannelID = channel.String
	}
	r.CreatedAt, err = parseTime(created)
	return r, err
}
func (s *Store) GetRequestRecord(ctx context.Context, id string) (domain.RequestRecord, error) {
	return scanRequest(s.repositoryExecutor().QueryRowContext(ctx, `SELECT `+requestColumns+` FROM request_records WHERE id=?`, id))
}

// requestRecordCap bounds every unbounded history read. Telemetry listing is
// "recent history", and scanning the whole table on each sync/aggregation was
// a documented capacity-governance gap (AUDIT RH-32 / P1-2).
const requestRecordCap = 5000

func (s *Store) ListRequestRecords(ctx context.Context) ([]domain.RequestRecord, error) {
	return s.ListRequestRecordsLimit(ctx, requestRecordCap)
}

// ListRequestRecordsLimit returns the newest request records, capped.
func (s *Store) ListRequestRecordsLimit(ctx context.Context, limit int) ([]domain.RequestRecord, error) {
	if limit <= 0 {
		limit = requestRecordCap
	}
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT `+requestColumns+` FROM request_records ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RequestRecord
	for rows.Next() {
		r, e := scanRequest(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRequestRecordsByChannel returns the newest records for one channel with
// a hard bound, replacing "scan everything then keep 100" (AUDIT RH-32).
func (s *Store) ListRequestRecordsByChannel(ctx context.Context, channelID string, limit int) ([]domain.RequestRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT `+requestColumns+` FROM request_records WHERE channel_id=? ORDER BY created_at DESC LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RequestRecord
	for rows.Next() {
		r, e := scanRequest(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RequestUsageTotals computes aggregates in SQL instead of loading the full
// history into memory (AUDIT RH-32).
func (s *Store) RequestUsageTotals(ctx context.Context) (total int64, inputTokens int64, cacheReadTokens int64, err error) {
	err = s.repositoryExecutor().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_read_tokens),0) FROM request_records`).Scan(&total, &inputTokens, &cacheReadTokens)
	return total, inputTokens, cacheReadTokens, err
}

// PruneRequestRecords deletes records older than the cutoff and reports how
// many were removed. Called periodically by the runtime (AUDIT RH-32 / P1-2).
func (s *Store) PruneRequestRecords(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := s.repositoryExecutor().ExecContext(ctx, `DELETE FROM request_records WHERE created_at < ?`, stamp(olderThan))
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	return affected, err
}

const billingReportColumns = `id,channel_id,window_start,window_end,source,requests,input_tokens,output_tokens,cache_read_tokens,tokens,charged_tokens,charged_quota,charged_usd,declared_usd,declared_known,effective_usd_per_mtok,declared_usd_per_mtok,baseline_usd_per_mtok,drift,token_drift,multiplier_drift,savings_usd,group_ratio,model_ratio,entries,matched_models,truncated,verdict,reason,created_at`

func (s *Store) CreateBillingReport(ctx context.Context, r domain.BillingReport) error {
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO billing_reports(`+billingReportColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.ChannelID, stamp(r.WindowStart), stamp(r.WindowEnd), r.Source, r.Requests,
		r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.Tokens, r.ChargedTokens, r.ChargedQuota,
		r.ChargedUSD, r.DeclaredUSD, boolInt(r.DeclaredKnown), r.EffectiveUSDPerMTok, r.DeclaredUSDPerMTok,
		r.BaselineUSDPerMTok, r.Drift, r.TokenDrift, r.MultiplierDrift, r.SavingsUSD, r.GroupRatio, r.ModelRatio,
		r.Entries, r.MatchedModels, boolInt(r.Truncated), r.Verdict, r.Reason, stamp(r.CreatedAt))
	return err
}

func scanBillingReport(row interface{ Scan(...any) error }) (domain.BillingReport, error) {
	var r domain.BillingReport
	var start, end, created string
	var declaredKnown, truncated int
	err := row.Scan(&r.ID, &r.ChannelID, &start, &end, &r.Source, &r.Requests,
		&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.Tokens, &r.ChargedTokens, &r.ChargedQuota,
		&r.ChargedUSD, &r.DeclaredUSD, &declaredKnown, &r.EffectiveUSDPerMTok, &r.DeclaredUSDPerMTok,
		&r.BaselineUSDPerMTok, &r.Drift, &r.TokenDrift, &r.MultiplierDrift, &r.SavingsUSD, &r.GroupRatio, &r.ModelRatio,
		&r.Entries, &r.MatchedModels, &truncated, &r.Verdict, &r.Reason, &created)
	if err != nil {
		return r, err
	}
	r.DeclaredKnown = declaredKnown != 0
	r.Truncated = truncated != 0
	if r.WindowStart, err = parseTime(start); err != nil {
		return r, err
	}
	if r.WindowEnd, err = parseTime(end); err != nil {
		return r, err
	}
	r.CreatedAt, err = parseTime(created)
	return r, err
}

// ListBillingReports returns the newest reconciliation reports, optionally
// limited to one channel. An empty channelID means "every channel". The limit
// is bounded so history display cannot scan an unbounded table (AUDIT RH-32).
func (s *Store) ListBillingReports(ctx context.Context, channelID string, limit int) ([]domain.BillingReport, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT ` + billingReportColumns + ` FROM billing_reports ORDER BY created_at DESC LIMIT ?`
	args := []any{limit}
	if channelID != "" {
		query = `SELECT ` + billingReportColumns + ` FROM billing_reports WHERE channel_id=? ORDER BY created_at DESC LIMIT ?`
		args = []any{channelID, limit}
	}
	rows, err := s.repositoryExecutor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BillingReport
	for rows.Next() {
		r, e := scanBillingReport(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateCLISyncRecord(ctx context.Context, r domain.CLISyncRecord) error {
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO cli_sync_records(id,cli,config_path,profile,backup_path,status,error,created_at) VALUES(?,?,?,?,?,?,?,?)`, r.ID, r.CLI, r.ConfigPath, r.Profile, r.BackupPath, r.Status, r.Error, stamp(r.CreatedAt))
	return err
}
func scanCLISync(row interface{ Scan(...any) error }) (domain.CLISyncRecord, error) {
	var r domain.CLISyncRecord
	var created string
	err := row.Scan(&r.ID, &r.CLI, &r.ConfigPath, &r.Profile, &r.BackupPath, &r.Status, &r.Error, &created)
	if err != nil {
		return r, err
	}
	r.CreatedAt, err = parseTime(created)
	return r, err
}
func (s *Store) GetCLISyncRecord(ctx context.Context, id string) (domain.CLISyncRecord, error) {
	return scanCLISync(s.repositoryExecutor().QueryRowContext(ctx, `SELECT id,cli,config_path,profile,backup_path,status,error,created_at FROM cli_sync_records WHERE id=?`, id))
}
func (s *Store) ListCLISyncRecords(ctx context.Context) ([]domain.CLISyncRecord, error) {
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT id,cli,config_path,profile,backup_path,status,error,created_at FROM cli_sync_records ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CLISyncRecord
	for rows.Next() {
		r, e := scanCLISync(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateConfigBackup(ctx context.Context, r domain.ConfigBackup) error {
	_, err := s.repositoryExecutor().ExecContext(ctx, `INSERT INTO config_backups(id,path,sha256,created_at) VALUES(?,?,?,?)`, r.ID, r.Path, r.SHA256, stamp(r.CreatedAt))
	return err
}
func scanBackup(row interface{ Scan(...any) error }) (domain.ConfigBackup, error) {
	var r domain.ConfigBackup
	var created string
	err := row.Scan(&r.ID, &r.Path, &r.SHA256, &created)
	if err != nil {
		return r, err
	}
	r.CreatedAt, err = parseTime(created)
	return r, err
}
func (s *Store) GetConfigBackup(ctx context.Context, id string) (domain.ConfigBackup, error) {
	return scanBackup(s.repositoryExecutor().QueryRowContext(ctx, `SELECT id,path,sha256,created_at FROM config_backups WHERE id=?`, id))
}
func (s *Store) ListConfigBackups(ctx context.Context) ([]domain.ConfigBackup, error) {
	rows, err := s.repositoryExecutor().QueryContext(ctx, `SELECT id,path,sha256,created_at FROM config_backups ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ConfigBackup
	for rows.Next() {
		r, e := scanBackup(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// WithTx executes repository operations in a context-bound transaction. The
// transaction repository exposes the same CRUD methods as Store, so callers
// can compose writes without duplicating SQL or escaping the abstraction.
func (s *Store) WithTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	repoTx := &Tx{Store{DB: s.DB, exec: tx}}
	if err = fn(repoTx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("transaction: %w", err)
	}
	return tx.Commit()
}
