package emoji

import (
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"polynux/disgoroq/config"
	appcontext "polynux/disgoroq/context"
)

// Emoji represents a Discord emoji
type Emoji struct {
	Name     string
	ID       string
	Animated bool
	GuildID  string
}

// cacheEntry holds cached emoji data with timestamp
type cacheEntry struct {
	emojis    []Emoji
	timestamp time.Time
}

// Manager handles emoji fetching and caching
type Manager struct {
	client *bot.Client
	cache  map[string]*cacheEntry
	mutex  sync.RWMutex
	ttl    time.Duration
}

// NewManager creates a new emoji manager.
// If cfg is nil, default configuration is used.
func NewManager(client *bot.Client, cfg config.EmojiConfig) *Manager {
	ttlMinutes := cfg.CacheTTLMinutes
	if ttlMinutes <= 0 {
		ttlMinutes = 60 // Default to 60 minutes
	}

	return &Manager{
		client: client,
		cache:  make(map[string]*cacheEntry),
		ttl:    time.Duration(ttlMinutes) * time.Minute,
	}
}

// GetEmojisForGuild returns emojis for a guild, using cache if available
func (m *Manager) GetEmojisForGuild(guildID string) []Emoji {
	m.mutex.RLock()
	entry, exists := m.cache[guildID]
	m.mutex.RUnlock()

	if exists && time.Since(entry.timestamp) < m.ttl {
		return entry.emojis
	}

	// Cache miss or expired - fetch fresh data
	emojis, err := m.fetchEmojis(guildID)
	if err != nil {
		// Return cached data even if expired, or empty slice
		if exists {
			return entry.emojis
		}
		return []Emoji{}
	}

	m.mutex.Lock()
	m.cache[guildID] = &cacheEntry{
		emojis:    emojis,
		timestamp: time.Now(),
	}
	m.mutex.Unlock()

	return emojis
}

// RefreshEmojis forces a refresh of emoji data for a guild
func (m *Manager) RefreshEmojis(guildID string) error {
	emojis, err := m.fetchEmojis(guildID)
	if err != nil {
		return err
	}

	m.mutex.Lock()
	m.cache[guildID] = &cacheEntry{
		emojis:    emojis,
		timestamp: time.Now(),
	}
	m.mutex.Unlock()

	return nil
}

// fetchEmojis fetches emojis from Discord API
func (m *Manager) fetchEmojis(guildID string) ([]Emoji, error) {
	ctx, cancel := appcontext.Message()
	defer cancel()

	guildIDSnowflake, err := snowflake.Parse(guildID)
	if err != nil {
		return nil, err
	}

	discordEmojis, err := m.client.Rest.GetEmojis(guildIDSnowflake, rest.WithCtx(ctx))
	if err != nil {
		return nil, err
	}

	emojis := make([]Emoji, 0, len(discordEmojis))
	for _, e := range discordEmojis {
		emojis = append(emojis, Emoji{
			Name:     e.Name,
			ID:       e.ID.String(),
			Animated: e.Animated,
			GuildID:  guildID,
		})
	}

	return emojis, nil
}

// FormatEmojiList formats emojis for AI system prompt
func (m *Manager) FormatEmojiList(emojis []Emoji) string {
	if len(emojis) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Emojis disponibles: ")

	for i, emoji := range emojis {
		if i > 0 {
			sb.WriteString(" ")
		}
		// Format as shortcode for AI understanding
		sb.WriteString(":" + emoji.Name + ":")
	}

	return sb.String()
}

// AllGuildEmojiPrompt returns a capped, prompt-ready description of custom emojis
// available across all guilds the bot can access.
func (m *Manager) AllGuildEmojiPrompt(limit int) string {
	if m == nil {
		return ""
	}

	emojis := m.GetAllEmojis()
	if limit > 0 && len(emojis) > limit {
		emojis = emojis[:limit]
	}
	if len(emojis) == 0 {
		return ""
	}

	return "\n\nTu peux aussi utiliser ces emojis personnalisés: " + m.FormatEmojiList(emojis)
}

// GetAllEmojis returns emojis from all guilds the bot is in
func (m *Manager) GetAllEmojis() []Emoji {
	if m.client == nil {
		return []Emoji{}
	}

	allEmojis := make([]Emoji, 0)
	seen := make(map[string]bool)

	for guild := range m.client.Caches.Guilds() {
		emojis := m.GetEmojisForGuild(guild.ID.String())
		for _, emoji := range emojis {
			// Deduplicate by ID
			if !seen[emoji.ID] {
				seen[emoji.ID] = true
				allEmojis = append(allEmojis, emoji)
			}
		}
	}

	return allEmojis
}

// FormatDiscordEmoji returns the Discord format for an emoji
// <:name:id> for regular, <a:name:id> for animated
func (e *Emoji) FormatDiscordEmoji() string {
	if e.Animated {
		return "<a:" + e.Name + ":" + e.ID + ">"
	}
	return "<:" + e.Name + ":" + e.ID + ">"
}

// ConvertShortcodesToDiscordEmojis replaces :name: shortcodes with Discord emoji format
// It searches through the guild's emojis to find matches and replaces them
func (m *Manager) ConvertShortcodesToDiscordEmojis(text string, guildID string) string {
	if m == nil || guildID == "" {
		return text
	}

	emojis := m.GetEmojisForGuild(guildID)
	if len(emojis) == 0 {
		return text
	}

	emojiMap := make(map[string]Emoji)
	for _, emoji := range emojis {
		emojiMap[emoji.Name] = emoji
	}

	result := text
	for name, emoji := range emojiMap {
		shortcode := ":" + name + ":"
		discordFormat := emoji.FormatDiscordEmoji()
		result = strings.ReplaceAll(result, shortcode, discordFormat)
	}

	return result
}
