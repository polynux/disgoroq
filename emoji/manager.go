package emoji

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
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
	session *discordgo.Session
	cache   map[string]*cacheEntry
	mutex   sync.RWMutex
	ttl     time.Duration
}

// NewManager creates a new emoji manager
func NewManager(session *discordgo.Session) *Manager {
	ttlMinutes := 60
	if envTTL := os.Getenv("EMOJI_CACHE_TTL_MINUTES"); envTTL != "" {
		if minutes, err := strconv.Atoi(envTTL); err == nil && minutes > 0 {
			ttlMinutes = minutes
		}
	}

	return &Manager{
		session: session,
		cache:   make(map[string]*cacheEntry),
		ttl:     time.Duration(ttlMinutes) * time.Minute,
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
	discordEmojis, err := m.session.GuildEmojis(guildID)
	if err != nil {
		return nil, err
	}

	emojis := make([]Emoji, 0, len(discordEmojis))
	for _, e := range discordEmojis {
		emojis = append(emojis, Emoji{
			Name:     e.Name,
			ID:       e.ID,
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

// GetAllEmojis returns emojis from all guilds the bot is in
func (m *Manager) GetAllEmojis() []Emoji {
	if m.session == nil || m.session.State == nil {
		return []Emoji{}
	}

	allEmojis := make([]Emoji, 0)
	seen := make(map[string]bool)

	for _, guild := range m.session.State.Guilds {
		emojis := m.GetEmojisForGuild(guild.ID)
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
