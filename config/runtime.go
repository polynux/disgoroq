package config

import "sync/atomic"

// Runtime holds the current immutable Config snapshot and allows lock-free
// hot-apply updates for the supported subset of settings.
type Runtime struct {
	current atomic.Pointer[Config]
}

// NewRuntime creates a Runtime initialized with initial (or DefaultConfig if nil).
func NewRuntime(initial *Config) *Runtime {
	r := &Runtime{}
	if initial == nil {
		initial = DefaultConfig()
	}
	r.current.Store(initial)
	return r
}

// Current returns the active Config snapshot. Callers must treat it as immutable.
func (r *Runtime) Current() *Config {
	if r == nil {
		return DefaultConfig()
	}
	if cfg := r.current.Load(); cfg != nil {
		return cfg
	}
	return DefaultConfig()
}

// Store atomically publishes a new immutable Config snapshot.
func (r *Runtime) Store(cfg *Config) {
	if r == nil || cfg == nil {
		return
	}
	r.current.Store(cfg)
}

// HotApplyFields is the single source of truth for config.yaml dotted paths
// whose changes take effect immediately via Runtime without restarting the bot.
var HotApplyFields = map[string]struct{}{
	"bot.default_prompt":                   {},
	"bot.trigger_words":                    {},
	"voice.voice_system_prompt":            {},
	"votes.enabled":                        {},
	"votes.probability":                    {},
	"votes.good_emoji":                     {},
	"votes.bad_emoji":                      {},
	"reengage.default_inactivity_minutes":  {},
	"reengage.default_chance":              {},
	"reengage.reengage_message":            {},
	"media_library.min_similarity":         {},
	"media_library.min_trigger_similarity": {},
	"media_library.cross_guild":            {},
	"media_library.allow_discord_cdn":      {},
	"media_library.repeat_window":          {},
	"media_library.lookup_timeout_ms":      {},
	"media_library.random_gif_fallback":    {},
	"media_library.gif_on_request":         {},
	"media_library.validate_links":         {},
}

// IsHotApply reports whether the given dotted config path is applied live without restart.
func IsHotApply(path string) bool {
	_, ok := HotApplyFields[path]
	return ok
}
