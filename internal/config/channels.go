package config

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/secret"
	"notifyrelay/internal/store"
)

// ChannelStore is what this package needs from persistence.
//
// Narrower than store.Store on purpose: a package that can reach the delivery
// queue has no business doing so from the code that manages channel
// configuration, and an interface is the cheapest way to say that.
type ChannelStore interface {
	store.Channels
	store.Meta
}

// ChannelSource is where channel instances come from.
//
// It exists because "where is this channel configured" stopped having one
// answer in M5: the file is where a deployment starts, and the database is
// where it lives afterwards. Having both readers in one place is what keeps the
// precedence rule from being re-derived at every call site — and the rule has
// exactly one shape:
//
//	The database is the truth. The file seeds it, once.
//
// The alternative — file and database both authoritative — means an operator
// deletes a channel in the UI and it comes back at the next restart, which is
// the kind of behaviour that makes people stop trusting the UI.
type ChannelSource struct {
	store  ChannelStore
	cipher *secret.Cipher
	log    *slog.Logger
}

// NewChannelSource builds a source. A nil cipher means this deployment has no
// key configured; instances whose type declares no private parameters can still
// be stored, and any that do are refused rather than written in the clear.
func NewChannelSource(st ChannelStore, cipher *secret.Cipher, log *slog.Logger) *ChannelSource {
	if log == nil {
		log = slog.Default()
	}
	return &ChannelSource{store: st, cipher: cipher, log: log}
}

// Load returns every configured instance, with sealed values opened.
//
// Disabled instances are included: the operator UI has to show them, and a
// channel that has been switched off is a thing somebody deliberately did.
// router.New skips them when it builds.
func (s *ChannelSource) Load(ctx context.Context) ([]ChannelConfig, error) {
	records, err := s.store.ListChannels(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]ChannelConfig, 0, len(records))
	for _, rec := range records {
		cfg, err := s.toConfig(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, nil
}

// Get returns one instance, or nil when there is no such instance.
func (s *ChannelSource) Get(ctx context.Context, name string) (*ChannelConfig, error) {
	rec, err := s.store.GetChannel(ctx, name)
	if err != nil || rec == nil {
		return nil, err
	}
	cfg, err := s.toConfig(rec)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save seals the instance's credentials and stores it.
//
// Sealing is driven by the channel type's own schema. That is the same
// declaration the operator form is generated from and the same one the router
// redacts by, so a parameter cannot be a secret in one place and not another —
// marking it Private protects it everywhere, including here, without the
// channel author knowing this function exists.
func (s *ChannelSource) Save(ctx context.Context, cfg ChannelConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("config: a channel instance needs a name")
	}
	if cfg.Type == "" {
		return fmt.Errorf("config: channel %q needs a type", cfg.Name)
	}
	// Refusing here is what makes seal()'s behaviour safe. With no schema there
	// is nothing to say which parameters are credentials, so an unknown type
	// would be stored with its secrets in the clear and no indication that
	// anything had been skipped.
	if _, ok := channel.Lookup(cfg.Type); !ok {
		return fmt.Errorf("config: channel %q: unknown type %q (registered: %v)",
			cfg.Name, cfg.Type, channel.Registered())
	}

	sealed, err := s.seal(cfg.Type, cfg.Config)
	if err != nil {
		return fmt.Errorf("config: channel %q: %w", cfg.Name, err)
	}

	enabled := cfg.IsEnabled()
	return s.store.PutChannel(ctx, &store.ChannelInstance{
		Name:    cfg.Name,
		Type:    cfg.Type,
		Enabled: enabled,
		Config:  sealed,
		Quota: store.Quota{
			PerSecond: cfg.Quota.PerSecond,
			PerMinute: cfg.Quota.PerMinute,
			PerHour:   cfg.Quota.PerHour,
			PerDay:    cfg.Quota.PerDay,
			PerMonth:  cfg.Quota.PerMonth,
		},
	})
}

// Delete removes an instance.
func (s *ChannelSource) Delete(ctx context.Context, name string) (bool, error) {
	return s.store.DeleteChannel(ctx, name)
}

// ImportOnce copies file-declared channels into the database, once.
//
// "Once" is recorded as a row in the meta table rather than inferred from the
// channel table being empty. The difference shows up the first time somebody
// deletes every channel through the UI: with the empty-table signal they all
// come back at the next restart, and they come back again after every restart,
// and the UI has just been demonstrated to be untrustworthy. A marker is a
// fact; emptiness is an inference.
//
// A deployment that has credentials but no key configured fails here rather
// than importing them in the clear. That is the same choice the !env loader
// makes for a missing variable: refusing to start is louder than starting with
// a credential the operator believes is protected.
func (s *ChannelSource) ImportOnce(ctx context.Context, fromFile []ChannelConfig) (int, error) {
	if len(fromFile) == 0 {
		return 0, nil
	}

	if _, done, err := s.store.GetMeta(ctx, store.MetaChannelsImported); err != nil {
		return 0, err
	} else if done {
		s.log.Info("config: channel instances are database-managed; " +
			"the `channels:` block in the configuration file is ignored")
		return 0, nil
	}

	for _, cfg := range fromFile {
		if err := s.Save(ctx, cfg); err != nil {
			return 0, err
		}
	}

	// Written after the channels, never before. A marker that survived a failed
	// import would leave the deployment with neither the file's channels nor
	// the record that they were supposed to arrive.
	if err := s.store.SetMeta(ctx, store.MetaChannelsImported, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}

	s.log.Warn("config: imported channel instances from the configuration file",
		slog.Int("count", len(fromFile)),
		slog.String("note", "the database is authoritative from now on; "+
			"the `channels:` block in the file is no longer read"),
	)
	return len(fromFile), nil
}

// ---------------------------------------------------------------- conversion

func (s *ChannelSource) toConfig(rec *store.ChannelInstance) (ChannelConfig, error) {
	opened, err := s.open(rec.Config)
	if err != nil {
		return ChannelConfig{}, fmt.Errorf("config: channel %q: %w", rec.Name, err)
	}

	enabled := rec.Enabled
	return ChannelConfig{
		Name:    rec.Name,
		Type:    rec.Type,
		Enabled: &enabled,
		Config:  opened,
		Quota: QuotaConfig{
			PerSecond: rec.Quota.PerSecond,
			PerMinute: rec.Quota.PerMinute,
			PerHour:   rec.Quota.PerHour,
			PerDay:    rec.Quota.PerDay,
			PerMonth:  rec.Quota.PerMonth,
		},
	}, nil
}

// seal encrypts the values of the parameters the channel type declares private.
//
// Only the ones the schema names. A value that happens to look like a
// credential but is not declared private stays readable, which is what keeps a
// database dump useful for the question people actually open it to answer:
// which host is this channel pointing at.
func (s *ChannelSource) seal(channelType string, cfg map[string]any) (map[string]any, error) {
	if len(cfg) == 0 {
		return map[string]any{}, nil
	}

	specs := specsFor(channelType)
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}

	for _, spec := range specs {
		if !spec.Private {
			continue
		}
		value, ok := out[spec.Name].(string)
		if !ok || value == "" || secret.IsSealed(value) {
			// Absent, not a string, empty, or already sealed. The last case
			// matters: an operator editing a channel through the API may send
			// back the masked placeholder rather than retyping the credential,
			// and sealing a sealed value would encrypt the ciphertext.
			continue
		}
		if s.cipher == nil {
			return nil, fmt.Errorf(
				"parameter %q holds a credential and this deployment has no key configured; "+
					"set secret_key (generate one with `notifyrelay --gen-key`)", spec.Name)
		}

		sealed, err := s.cipher.Seal(value)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", spec.Name, err)
		}
		out[spec.Name] = sealed
	}

	return out, nil
}

// open decrypts every value that carries the sealed marker.
//
// Driven by the marker rather than by the schema, so that a row for a channel
// type this binary does not have registered is still readable — the operator
// may be looking at a database written by a build with more channels than this
// one, and refusing to open the row would hide the reason.
func (s *ChannelSource) open(cfg map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		value, ok := v.(string)
		if !ok || !secret.IsSealed(value) {
			out[k] = v
			continue
		}
		if s.cipher == nil {
			return nil, fmt.Errorf(
				"parameter %q is sealed but this deployment has no key configured; "+
					"set secret_key to the key this value was sealed with", k)
		}

		plain, err := s.cipher.Open(value)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", k, err)
		}
		out[k] = plain
	}
	return out, nil
}

// specsFor returns a channel type's parameter schema, or nil when the type is
// not registered in this binary.
//
// Nil is not an error. A row can name a channel type this build does not have —
// a downgrade, or a plugin that was removed — and the useful behaviour is to
// leave its values alone rather than to refuse to read the table.
func specsFor(channelType string) []channel.ParamSpec {
	d, ok := channel.Lookup(channelType)
	if !ok {
		return nil
	}
	return d.ParamSchema
}


// MaskSecrets returns a copy of a channel's configuration with the values of
// private parameters removed, along with the names of the ones that were set.
//
// The values are removed rather than replaced with asterisks: a placeholder is
// a value, and a form that posts it back would set the credential to the
// placeholder. Reporting *which* parameters are configured separately is what
// lets the UI show "a password is set" without ever holding the password.
func MaskSecrets(channelType string, cfg map[string]any) (map[string]any, []string) {
	masked := make(map[string]any, len(cfg))
	for k, v := range cfg {
		masked[k] = v
	}

	var set []string
	for _, spec := range specsFor(channelType) {
		if !spec.Private {
			continue
		}
		value, ok := masked[spec.Name]
		if !ok {
			continue
		}
		delete(masked, spec.Name)

		if s, _ := value.(string); s != "" {
			set = append(set, spec.Name)
		}
	}

	sort.Strings(set)
	return masked, set
}

// MergeEdit returns cfg with the credentials it does not mention filled in from
// what is already stored.
//
// A form cannot send back a credential it was never shown, so "absent" has to
// mean "unchanged" — otherwise every edit of a channel's rate limit would clear
// its password. The three cases are distinguished explicitly:
//
//	absent          keep whatever is stored
//	""              clear it
//	any other value set it
//
// An empty string therefore means "clear" rather than "unchanged", which is why
// the UI must omit a field the operator did not touch rather than send it empty.
//
// It is separate from Save because the caller has to validate the *merged*
// result. Validating what the client sent would refuse an edit that changes a
// channel's host, on the grounds that the credential the form never showed is
// missing — and the operator would have no way to satisfy it from the form.
func (s *ChannelSource) MergeEdit(ctx context.Context, cfg ChannelConfig) (ChannelConfig, error) {
	existing, err := s.Get(ctx, cfg.Name)
	if err != nil {
		return cfg, err
	}
	if existing == nil {
		return cfg, nil
	}

	merged := make(map[string]any, len(cfg.Config))
	for k, v := range cfg.Config {
		merged[k] = v
	}

	for _, spec := range specsFor(cfg.Type) {
		if !spec.Private {
			continue
		}
		if _, mentioned := merged[spec.Name]; mentioned {
			continue
		}
		if value, ok := existing.Config[spec.Name]; ok {
			merged[spec.Name] = value
		}
	}

	cfg.Config = merged
	return cfg, nil
}

// SaveEdit merges and stores. Callers that validate should use MergeEdit and
// Save separately, so that validation sees what will actually be stored.
func (s *ChannelSource) SaveEdit(ctx context.Context, cfg ChannelConfig) error {
	merged, err := s.MergeEdit(ctx, cfg)
	if err != nil {
		return err
	}
	return s.Save(ctx, merged)
}
