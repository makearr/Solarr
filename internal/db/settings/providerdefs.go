package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// ProviderDefinitionState is what a person set about a provider definition file; the file is the default.
type ProviderDefinitionState struct {
	Kind, Key string
	// Enabled is nil while the person has not chosen.
	Enabled       *bool
	ApprovedHash  string
	ApprovedHosts []string
	// HasSettings says values are stored; they stay sealed until ProviderDefinitionSettings.
	HasSettings bool
	UpdatedAt   int64
}

func convertDefinitionState(row settingsdb.ProviderDefinitionState) (ProviderDefinitionState, error) {
	state := ProviderDefinitionState{Kind: row.Kind, Key: row.Key, ApprovedHash: row.ApprovedHash, HasSettings: len(row.Settings) > 0, UpdatedAt: row.UpdatedAt}
	if row.Enabled.Valid {
		on := row.Enabled.Int64 == 1
		state.Enabled = &on
	}
	if err := json.Unmarshal([]byte(row.ApprovedHosts), &state.ApprovedHosts); err != nil {
		return ProviderDefinitionState{}, fmt.Errorf("settings: provider definition %s/%s hosts: %w", row.Kind, row.Key, err)
	}
	return state, nil
}

// ProviderDefinitionStates lists every definition a person has set something on.
func (c *Cache) ProviderDefinitionStates(ctx context.Context) ([]ProviderDefinitionState, error) {
	rows, err := c.queries.ListProviderDefinitionStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list provider definition states: %w", err)
	}
	states := make([]ProviderDefinitionState, 0, len(rows))
	for _, row := range rows {
		state, err := convertDefinitionState(row)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

// SetProviderDefinitionEnabled stores a person's on or off; nil returns to the file's own default.
func (c *Cache) SetProviderDefinitionEnabled(ctx context.Context, kind, key string, enabled *bool, at int64) error {
	value := sql.NullInt64{}
	if enabled != nil {
		value = sql.NullInt64{Int64: boolToInt(*enabled), Valid: true}
	}
	return c.writeDefinition(ctx, "enable", func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetProviderDefinitionEnabled(ctx, settingsdb.SetProviderDefinitionEnabledParams{Kind: kind, Key: key, Enabled: value, UpdatedAt: at})
	})
}

// ApproveProviderDefinition records an administrator's approval of a custom file: its hash and the host list approved with it.
func (c *Cache) ApproveProviderDefinition(ctx context.Context, kind, key, hash string, hosts []string, at int64) error {
	if hosts == nil {
		hosts = []string{}
	}
	encoded, err := json.Marshal(hosts)
	if err != nil {
		return fmt.Errorf("settings: encode approved hosts: %w", err)
	}
	return c.writeDefinition(ctx, "approve", func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).ApproveProviderDefinition(ctx, settingsdb.ApproveProviderDefinitionParams{
			Kind: kind, Key: key, ApprovedHash: hash, ApprovedHosts: string(encoded), UpdatedAt: at,
		})
	})
}

// SetProviderDefinitionSettings seals the values a definition's `settings:` declared; an empty map clears them.
func (c *Cache) SetProviderDefinitionSettings(ctx context.Context, kind, key string, values map[string]string, at int64) error {
	if c.cipher == nil {
		return ErrSecretsUnavailable
	}
	var sealed []byte
	if len(values) > 0 {
		encoded, err := json.Marshal(values)
		if err != nil {
			return fmt.Errorf("settings: encode definition settings: %w", err)
		}
		if sealed, err = c.cipher.Seal(secrets.Secret(encoded)); err != nil {
			return fmt.Errorf("settings: seal definition settings: %w", err)
		}
	}
	return c.writeDefinition(ctx, "settings", func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).SetProviderDefinitionSettings(ctx, settingsdb.SetProviderDefinitionSettingsParams{Kind: kind, Key: key, Settings: sealed, UpdatedAt: at})
	})
}

// ProviderDefinitionSettings opens the sealed values for the one outbound call that sends them; nil when none are stored.
func (c *Cache) ProviderDefinitionSettings(ctx context.Context, kind, key string) (map[string]string, error) {
	if c.cipher == nil {
		return nil, ErrSecretsUnavailable
	}
	row, err := c.queries.GetProviderDefinitionState(ctx, settingsdb.GetProviderDefinitionStateParams{Kind: kind, Key: key})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(row.Settings) == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("settings: get provider definition %s/%s: %w", kind, key, err)
	}
	opened, err := c.cipher.Open(row.Settings)
	if err != nil {
		return nil, fmt.Errorf("settings: open provider definition %s/%s settings: %w", kind, key, err)
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(opened.Reveal()), &values); err != nil {
		return nil, fmt.Errorf("settings: decode provider definition %s/%s settings: %w", kind, key, err)
	}
	return values, nil
}

// ClearProviderDefinitionState forgets everything set about a definition (its file was removed).
func (c *Cache) ClearProviderDefinitionState(ctx context.Context, kind, key string) error {
	return c.writeDefinition(ctx, "clear", func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteProviderDefinitionState(ctx, settingsdb.DeleteProviderDefinitionStateParams{Kind: kind, Key: key})
	})
}

func (c *Cache) writeDefinition(ctx context.Context, what string, write func(context.Context, *sql.Tx) error) error {
	if err := c.store.Write(ctx, store.PrioInteractive, write); err != nil {
		return fmt.Errorf("settings: provider definition %s: %w", what, err)
	}
	return nil
}
