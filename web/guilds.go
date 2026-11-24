package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/triggerwords"
)

const (
	maxPromptBytes          = 256 * 1024
	maxReengageMessageRunes = 500
)

var resettableKeys = map[string]struct{}{
	"state":                   {},
	"threshold":               {},
	"thresholdSexe":           {},
	"temperature":             {},
	"messagescount":           {},
	"memory_enabled":          {},
	"trigger_words":           {},
	"prompt":                  {},
	"voice_prompt":            {},
	"horoscope_channel":       {},
	"farting_friday_channel":  {},
	"voice_enabled":           {},
	"voice_auto_join":         {},
	"voice_auto_join_channel": {},
	"reengage_message":        {},
}

type guildSummary struct {
	ID      string
	Name    string
	IconURL string
	InCache bool
}

type guildsPageData struct {
	Title     string
	ActiveNav string
	Guilds    []guildSummary
}

type channelOption struct {
	ID   string
	Name string
}

type generalSectionData struct {
	GuildID string
	Success string
	Error   string

	State         string
	StateOverride bool

	Threshold         string
	ThresholdOverride bool
	ThresholdDefault  string

	ThresholdSexe         string
	ThresholdSexeOverride bool
	ThresholdSexeDefault  string

	Temperature         string
	TemperatureOverride bool
	TemperatureDefault  string

	MessagesCount         string
	MessagesCountOverride bool
	MessagesCountDefault  string

	MemoryEnabled         bool
	MemoryEnabledOverride bool

	TriggerWords         string
	TriggerWordsOverride bool
	TriggerWordsDefault  string
}

type promptsSectionData struct {
	GuildID string
	Success string
	Error   string

	Prompt         string
	PromptOverride bool
	PromptDefault  string

	VoicePrompt         string
	VoicePromptOverride bool
	VoicePromptDefault  string
}

type channelsSectionData struct {
	GuildID      string
	Success      string
	Error        string
	TextChannels []channelOption

	HoroscopeChannel         string
	HoroscopeChannelOverride bool
	HoroscopeInCache         bool

	FartingFridayChannel         string
	FartingFridayChannelOverride bool
	FartingFridayInCache         bool
}

type voiceSectionData struct {
	GuildID       string
	Success       string
	Error         string
	VoiceChannels []channelOption

	VoiceEnabled         bool
	VoiceEnabledOverride bool

	AutoJoin         bool
	AutoJoinOverride bool

	AutoJoinChannel         string
	AutoJoinChannelOverride bool
	AutoJoinChannelInCache  bool
}

type reengageChannelRow struct {
	ChannelID         string
	ChannelName       string
	Enabled           bool
	EnabledOverride   bool
	Chance            string
	ChanceOverride    bool
	Threshold         string
	ThresholdOverride bool
}

type reengageSectionData struct {
	GuildID      string
	Success      string
	Error        string
	TextChannels []channelOption

	Message         string
	MessageOverride bool
	MessageDefault  string

	DefaultChance    string
	DefaultThreshold string

	Channels []reengageChannelRow

	// Form values preserved on validation error when adding/editing a channel.
	FormChannelID string
	FormEnabled   bool
	FormChance    string
	FormThreshold string
}

type bookkeepingRow struct {
	Key       string
	Label     string
	RawValue  string
	Formatted string
}

type guildPageData struct {
	Title       string
	ActiveNav   string
	Guild       guildSummary
	General     generalSectionData
	Prompts     promptsSectionData
	Channels    channelsSectionData
	Voice       voiceSectionData
	Reengage    reengageSectionData
	Bookkeeping []bookkeepingRow
}

func (s *Server) handleGuildList(w http.ResponseWriter, r *http.Request) {
	guilds, err := s.listGuilds(r.Context())
	if err != nil {
		logger.Error("Failed to list guilds for web admin", zap.Error(err))
		http.Error(w, "Failed to load guilds", http.StatusInternalServerError)
		return
	}

	s.renderPage(w, "guilds", http.StatusOK, guildsPageData{
		Title:     "Guilds",
		ActiveNav: "guilds",
		Guilds:    guilds,
	})
}

func (s *Server) handleGuildDetail(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	data, err := s.buildGuildPageData(r.Context(), guildID)
	if err != nil {
		logger.Error("Failed to load guild settings for web admin",
			zap.String("guild_id", guildID),
			zap.Error(err))
		http.Error(w, "Failed to load guild settings", http.StatusInternalServerError)
		return
	}

	s.renderPage(w, "guild", http.StatusOK, data)
}

func (s *Server) handleGuildGeneralSave(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	settings, err := s.loadGuildSettings(r.Context(), guildID)
	if err != nil {
		logger.Error("Failed to load guild settings before general save", zap.String("guild_id", guildID), zap.Error(err))
		http.Error(w, "Failed to load guild settings", http.StatusInternalServerError)
		return
	}

	stateVal := strings.TrimSpace(r.FormValue("state"))
	thresholdRaw := strings.TrimSpace(r.FormValue("threshold"))
	thresholdSexeRaw := strings.TrimSpace(r.FormValue("thresholdSexe"))
	temperatureRaw := strings.TrimSpace(r.FormValue("temperature"))
	messagesCountRaw := strings.TrimSpace(r.FormValue("messagescount"))
	memoryEnabled := r.FormValue("memory_enabled") == "on" || r.FormValue("memory_enabled") == "true"
	triggerWordsRaw := r.FormValue("trigger_words")

	renderErr := func(msg string) {
		sec := s.buildGeneralSection(guildID, settings)
		sec.State = stateVal
		sec.Threshold = thresholdRaw
		sec.ThresholdSexe = thresholdSexeRaw
		sec.Temperature = temperatureRaw
		sec.MessagesCount = messagesCountRaw
		sec.MemoryEnabled = memoryEnabled
		sec.TriggerWords = triggerWordsRaw
		sec.Error = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_general", sec, true)
	}

	if stateVal != "on" && stateVal != "off" {
		renderErr("State must be 'on' or 'off'.")
		return
	}

	threshold, err := parseFloatRange(thresholdRaw, 0, 1)
	if err != nil {
		renderErr("Threshold must be a number between 0.0 and 1.0.")
		return
	}

	thresholdSexe, err := parseFloatRange(thresholdSexeRaw, 0, 1)
	if err != nil {
		renderErr("Threshold (sexe) must be a number between 0.0 and 1.0.")
		return
	}

	temperature, err := parseFloatRange(temperatureRaw, 0, 1)
	if err != nil {
		renderErr("Temperature must be a number between 0.0 and 1.0.")
		return
	}

	messagesCount, err := strconv.Atoi(messagesCountRaw)
	if err != nil || messagesCount < 1 || messagesCount > 100 {
		renderErr("Messages count must be an integer between 1 and 100.")
		return
	}

	parsedTriggers := triggerwords.Parse(triggerWordsRaw)
	if err := triggerwords.Validate(parsedTriggers); err != nil {
		renderErr("Invalid trigger words: " + err.Error())
		return
	}

	ctx := r.Context()

	if _, had := settings["state"]; had || stateVal != "on" {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "state", stateVal); err != nil {
			renderErr("Failed to save state: " + err.Error())
			return
		}
	}

	if _, had := settings["threshold"]; had || threshold != database.DefaultThreshold {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "threshold", strconv.FormatFloat(threshold, 'f', -1, 64)); err != nil {
			renderErr("Failed to save threshold: " + err.Error())
			return
		}
	}

	if _, had := settings["thresholdSexe"]; had || thresholdSexe != database.DefaultThresholdSexe {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "thresholdSexe", strconv.FormatFloat(thresholdSexe, 'f', -1, 64)); err != nil {
			renderErr("Failed to save thresholdSexe: " + err.Error())
			return
		}
	}

	if _, had := settings["temperature"]; had || float32(temperature) != database.DefaultTemperature {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "temperature", strconv.FormatFloat(temperature, 'f', -1, 32)); err != nil {
			renderErr("Failed to save temperature: " + err.Error())
			return
		}
	}

	if _, had := settings["messagescount"]; had || messagesCount != database.DefaultMessagesCount {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "messagescount", strconv.Itoa(messagesCount)); err != nil {
			renderErr("Failed to save messagescount: " + err.Error())
			return
		}
	}

	if _, had := settings["memory_enabled"]; had || !memoryEnabled {
		if err := s.deps.Repo.SetMemoryEnabled(ctx, guildID, memoryEnabled); err != nil {
			renderErr("Failed to save memory_enabled: " + err.Error())
			return
		}
	}

	defaultTriggers := s.defaultTriggerWords()
	if _, had := settings["trigger_words"]; had || !stringSlicesEqual(parsedTriggers, defaultTriggers) {
		if err := s.deps.Repo.SetTriggerWords(ctx, guildID, parsedTriggers); err != nil {
			renderErr("Failed to save trigger_words: " + err.Error())
			return
		}
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		renderErr("Saved, but failed to reload settings: " + err.Error())
		return
	}

	sec := s.buildGeneralSection(guildID, updated)
	sec.Success = "General settings saved."
	s.renderSectionOrPage(w, r, guildID, "guild_section_general", sec, false)
}

func (s *Server) handleGuildPromptsSave(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	settings, err := s.loadGuildSettings(r.Context(), guildID)
	if err != nil {
		http.Error(w, "Failed to load guild settings", http.StatusInternalServerError)
		return
	}

	promptVal := r.FormValue("prompt")
	voicePromptVal := r.FormValue("voice_prompt")

	renderErr := func(msg string) {
		sec := s.buildPromptsSection(guildID, settings)
		sec.Prompt = promptVal
		sec.VoicePrompt = voicePromptVal
		sec.Error = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_prompts", sec, true)
	}

	if len(promptVal) > maxPromptBytes || !utf8.ValidString(promptVal) {
		renderErr("System prompt must be valid UTF-8 and at most 256 KiB.")
		return
	}
	if len(voicePromptVal) > maxPromptBytes || !utf8.ValidString(voicePromptVal) {
		renderErr("Voice prompt must be valid UTF-8 and at most 256 KiB.")
		return
	}

	ctx := r.Context()
	if strings.TrimSpace(promptVal) == "" {
		if err := s.deps.Repo.DeleteGuildSetting(ctx, guildID, "prompt"); err != nil {
			renderErr("Failed to reset system prompt: " + err.Error())
			return
		}
	} else {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "prompt", promptVal); err != nil {
			renderErr("Failed to save system prompt: " + err.Error())
			return
		}
	}

	if strings.TrimSpace(voicePromptVal) == "" {
		if err := s.deps.Repo.DeleteGuildSetting(ctx, guildID, "voice_prompt"); err != nil {
			renderErr("Failed to reset voice prompt: " + err.Error())
			return
		}
	} else {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "voice_prompt", voicePromptVal); err != nil {
			renderErr("Failed to save voice prompt: " + err.Error())
			return
		}
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		renderErr("Saved, but failed to reload settings: " + err.Error())
		return
	}

	sec := s.buildPromptsSection(guildID, updated)
	sec.Success = "Prompts saved."
	s.renderSectionOrPage(w, r, guildID, "guild_section_prompts", sec, false)
}

func (s *Server) handleGuildChannelsSave(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	settings, err := s.loadGuildSettings(r.Context(), guildID)
	if err != nil {
		http.Error(w, "Failed to load guild settings", http.StatusInternalServerError)
		return
	}

	horoscopeCh := strings.TrimSpace(r.FormValue("horoscope_channel"))
	fartingCh := strings.TrimSpace(r.FormValue("farting_friday_channel"))

	renderErr := func(msg string) {
		sec := s.buildChannelsSection(guildID, settings)
		sec.HoroscopeChannel = horoscopeCh
		sec.FartingFridayChannel = fartingCh
		sec.Error = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_channels", sec, true)
	}

	if horoscopeCh != "" {
		if _, err := snowflake.Parse(horoscopeCh); err != nil {
			renderErr("Horoscope channel ID must be a valid Discord snowflake.")
			return
		}
	}
	if fartingCh != "" {
		if _, err := snowflake.Parse(fartingCh); err != nil {
			renderErr("Farting Friday channel ID must be a valid Discord snowflake.")
			return
		}
	}

	ctx := r.Context()
	if horoscopeCh == "" {
		if err := s.deps.Repo.DeleteGuildSetting(ctx, guildID, "horoscope_channel"); err != nil {
			renderErr("Failed to clear horoscope channel: " + err.Error())
			return
		}
	} else {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "horoscope_channel", horoscopeCh); err != nil {
			renderErr("Failed to save horoscope channel: " + err.Error())
			return
		}
	}

	if fartingCh == "" {
		if err := s.deps.Repo.DeleteGuildSetting(ctx, guildID, "farting_friday_channel"); err != nil {
			renderErr("Failed to clear Farting Friday channel: " + err.Error())
			return
		}
	} else {
		if err := s.deps.Repo.SetGuildSetting(ctx, guildID, "farting_friday_channel", fartingCh); err != nil {
			renderErr("Failed to save Farting Friday channel: " + err.Error())
			return
		}
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		renderErr("Saved, but failed to reload settings: " + err.Error())
		return
	}

	sec := s.buildChannelsSection(guildID, updated)
	sec.Success = "Scheduled channels saved."
	s.renderSectionOrPage(w, r, guildID, "guild_section_channels", sec, false)
}

func (s *Server) handleGuildVoiceSave(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	settings, err := s.loadGuildSettings(r.Context(), guildID)
	if err != nil {
		http.Error(w, "Failed to load guild settings", http.StatusInternalServerError)
		return
	}

	voiceEnabled := r.FormValue("voice_enabled") == "on" || r.FormValue("voice_enabled") == "true"
	autoJoin := r.FormValue("voice_auto_join") == "on" || r.FormValue("voice_auto_join") == "true"
	autoJoinCh := strings.TrimSpace(r.FormValue("voice_auto_join_channel"))

	renderErr := func(msg string) {
		sec := s.buildVoiceSection(guildID, settings)
		sec.VoiceEnabled = voiceEnabled
		sec.AutoJoin = autoJoin
		sec.AutoJoinChannel = autoJoinCh
		sec.Error = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_voice", sec, true)
	}

	if autoJoinCh != "" {
		if _, err := snowflake.Parse(autoJoinCh); err != nil {
			renderErr("Auto-join channel ID must be a valid Discord snowflake.")
			return
		}
	}

	ctx := r.Context()
	if _, had := settings["voice_enabled"]; had || !voiceEnabled {
		if err := s.deps.Repo.SetVoiceEnabled(ctx, guildID, voiceEnabled); err != nil {
			renderErr("Failed to save voice_enabled: " + err.Error())
			return
		}
	}
	if _, had := settings["voice_auto_join"]; had || autoJoin {
		if err := s.deps.Repo.SetVoiceAutoJoin(ctx, guildID, autoJoin); err != nil {
			renderErr("Failed to save voice_auto_join: " + err.Error())
			return
		}
	}
	if autoJoinCh == "" {
		if err := s.deps.Repo.DeleteGuildSetting(ctx, guildID, "voice_auto_join_channel"); err != nil {
			renderErr("Failed to clear auto-join channel: " + err.Error())
			return
		}
	} else {
		if err := s.deps.Repo.SetVoiceAutoJoinChannel(ctx, guildID, autoJoinCh); err != nil {
			renderErr("Failed to save auto-join channel: " + err.Error())
			return
		}
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		renderErr("Saved, but failed to reload settings: " + err.Error())
		return
	}

	sec := s.buildVoiceSection(guildID, updated)
	sec.Success = "Voice settings saved."
	s.renderSectionOrPage(w, r, guildID, "guild_section_voice", sec, false)
}

func (s *Server) handleGuildVoiceReset(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	if err := s.deps.Repo.DeleteVoiceConfig(ctx, guildID); err != nil {
		logger.Error("Failed to reset voice config", zap.String("guild_id", guildID), zap.Error(err))
		http.Error(w, "Failed to reset voice config", http.StatusInternalServerError)
		return
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		http.Error(w, "Failed to reload settings", http.StatusInternalServerError)
		return
	}

	sec := s.buildVoiceSection(guildID, updated)
	sec.Success = "Voice settings reset to defaults."
	s.renderSectionOrPage(w, r, guildID, "guild_section_voice", sec, false)
}

func (s *Server) handleGuildReengageMessageSave(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	settings, err := s.loadGuildSettings(r.Context(), guildID)
	if err != nil {
		http.Error(w, "Failed to load guild settings", http.StatusInternalServerError)
		return
	}

	msgVal := r.FormValue("reengage_message")
	renderErr := func(msg string) {
		sec := s.buildReengageSection(guildID, settings)
		sec.Message = msgVal
		sec.Error = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_reengage", sec, true)
	}

	if utf8.RuneCountInString(msgVal) > maxReengageMessageRunes {
		renderErr(fmt.Sprintf("Reengage message must be at most %d characters.", maxReengageMessageRunes))
		return
	}

	ctx := r.Context()
	if strings.TrimSpace(msgVal) == "" {
		if err := s.deps.Repo.DeleteGuildSetting(ctx, guildID, "reengage_message"); err != nil {
			renderErr("Failed to reset reengage message: " + err.Error())
			return
		}
	} else {
		if err := s.deps.Repo.SetReengageMessage(ctx, guildID, msgVal); err != nil {
			renderErr("Failed to save reengage message: " + err.Error())
			return
		}
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		renderErr("Saved, but failed to reload settings: " + err.Error())
		return
	}

	sec := s.buildReengageSection(guildID, updated)
	sec.Success = "Reengage message saved."
	s.renderSectionOrPage(w, r, guildID, "guild_section_reengage", sec, false)
}

func (s *Server) handleGuildReengageChannelSave(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	settings, err := s.loadGuildSettings(r.Context(), guildID)
	if err != nil {
		http.Error(w, "Failed to load guild settings", http.StatusInternalServerError)
		return
	}

	channelID := strings.TrimSpace(r.FormValue("channel_id"))
	enabled := r.FormValue("enabled") == "on" || r.FormValue("enabled") == "true"
	chanceRaw := strings.TrimSpace(r.FormValue("chance"))
	thresholdRaw := strings.TrimSpace(r.FormValue("threshold"))

	renderErr := func(msg string) {
		sec := s.buildReengageSection(guildID, settings)
		sec.FormChannelID = channelID
		sec.FormEnabled = enabled
		sec.FormChance = chanceRaw
		sec.FormThreshold = thresholdRaw
		sec.Error = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_reengage", sec, true)
	}

	if channelID == "" {
		renderErr("Channel ID is required.")
		return
	}
	if _, err := snowflake.Parse(channelID); err != nil {
		renderErr("Channel ID must be a valid Discord snowflake.")
		return
	}

	var (
		hasChance    bool
		chanceVal    float64
		hasThreshold bool
		thresholdVal int
	)
	if chanceRaw != "" {
		parsed, err := parseFloatRange(chanceRaw, 0, 1)
		if err != nil {
			renderErr("Reengage chance must be a number between 0.0 and 1.0.")
			return
		}
		hasChance = true
		chanceVal = parsed
	}
	if thresholdRaw != "" {
		parsed, err := strconv.Atoi(thresholdRaw)
		if err != nil || parsed < 1 {
			renderErr("Reengage threshold must be an integer of at least 1 minute.")
			return
		}
		hasThreshold = true
		thresholdVal = parsed
	}

	ctx := r.Context()
	if err := s.deps.Repo.SetReengageEnabled(ctx, guildID, channelID, enabled); err != nil {
		renderErr("Failed to save channel reengage state: " + err.Error())
		return
	}
	if hasChance {
		if err := s.deps.Repo.SetReengageChance(ctx, guildID, channelID, chanceVal); err != nil {
			renderErr("Failed to save channel reengage chance: " + err.Error())
			return
		}
	} else {
		if err := s.deps.Repo.DeleteGuildSetting(ctx, guildID, "reengage_chance:"+channelID); err != nil {
			renderErr("Failed to reset channel reengage chance: " + err.Error())
			return
		}
	}
	if hasThreshold {
		if err := s.deps.Repo.SetReengageThreshold(ctx, guildID, channelID, thresholdVal); err != nil {
			renderErr("Failed to save channel reengage threshold: " + err.Error())
			return
		}
	} else {
		if err := s.deps.Repo.DeleteGuildSetting(ctx, guildID, "reengage_threshold:"+channelID); err != nil {
			renderErr("Failed to reset channel reengage threshold: " + err.Error())
			return
		}
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		renderErr("Saved, but failed to reload settings: " + err.Error())
		return
	}

	sec := s.buildReengageSection(guildID, updated)
	sec.Success = fmt.Sprintf("Reengage settings saved for channel %s.", channelID)
	s.renderSectionOrPage(w, r, guildID, "guild_section_reengage", sec, false)
}

func (s *Server) handleGuildReengageChannelReset(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	channelID := strings.TrimSpace(r.PathValue("channelID"))
	if guildID == "" || channelID == "" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	if err := s.deps.Repo.DeleteReengageConfig(ctx, guildID, channelID); err != nil {
		logger.Error("Failed to delete channel reengage config",
			zap.String("guild_id", guildID),
			zap.String("channel_id", channelID),
			zap.Error(err))
		http.Error(w, "Failed to reset channel reengage settings", http.StatusInternalServerError)
		return
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		http.Error(w, "Failed to reload settings", http.StatusInternalServerError)
		return
	}

	sec := s.buildReengageSection(guildID, updated)
	sec.Success = fmt.Sprintf("Reengage overrides cleared for channel %s.", channelID)
	s.renderSectionOrPage(w, r, guildID, "guild_section_reengage", sec, false)
}

func (s *Server) handleGuildSettingReset(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.PathValue("id"))
	if guildID == "" {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	key := strings.TrimSpace(r.FormValue("key"))
	section := strings.TrimSpace(r.FormValue("section"))
	if _, ok := resettableKeys[key]; !ok {
		http.Error(w, "Invalid setting key", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	var err error
	if key == "trigger_words" {
		err = s.deps.Repo.DeleteTriggerWords(ctx, guildID)
	} else {
		err = s.deps.Repo.DeleteGuildSetting(ctx, guildID, key)
	}
	if err != nil {
		logger.Error("Failed to reset guild setting",
			zap.String("guild_id", guildID),
			zap.String("key", key),
			zap.Error(err))
		http.Error(w, "Failed to reset setting", http.StatusInternalServerError)
		return
	}

	updated, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		http.Error(w, "Failed to reload settings", http.StatusInternalServerError)
		return
	}

	msg := fmt.Sprintf("Reset %s to default.", key)
	switch section {
	case "general":
		sec := s.buildGeneralSection(guildID, updated)
		sec.Success = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_general", sec, false)
	case "prompts":
		sec := s.buildPromptsSection(guildID, updated)
		sec.Success = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_prompts", sec, false)
	case "channels":
		sec := s.buildChannelsSection(guildID, updated)
		sec.Success = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_channels", sec, false)
	case "voice":
		sec := s.buildVoiceSection(guildID, updated)
		sec.Success = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_voice", sec, false)
	case "reengage":
		sec := s.buildReengageSection(guildID, updated)
		sec.Success = msg
		s.renderSectionOrPage(w, r, guildID, "guild_section_reengage", sec, false)
	default:
		http.Redirect(w, r, "/guilds/"+guildID, http.StatusSeeOther)
	}
}

func (s *Server) renderSectionOrPage(w http.ResponseWriter, r *http.Request, guildID, block string, sectionData any, isError bool) {
	if isHTMXRequest(r) {
		s.renderFragment(w, block, http.StatusOK, sectionData)
		return
	}
	if isError {
		// Re-render full page with 422 for non-htmx clients.
		data, err := s.buildGuildPageData(r.Context(), guildID)
		if err != nil {
			http.Error(w, "Failed to render page", http.StatusInternalServerError)
			return
		}
		switch v := sectionData.(type) {
		case generalSectionData:
			data.General = v
		case promptsSectionData:
			data.Prompts = v
		case channelsSectionData:
			data.Channels = v
		case voiceSectionData:
			data.Voice = v
		case reengageSectionData:
			data.Reengage = v
		}
		s.renderPage(w, "guild", http.StatusUnprocessableEntity, data)
		return
	}
	http.Redirect(w, r, "/guilds/"+guildID, http.StatusSeeOther)
}

func (s *Server) listGuilds(ctx context.Context) ([]guildSummary, error) {
	byID := make(map[string]guildSummary)

	if s.deps.Client != nil && s.deps.Client.Caches != nil {
		for g := range s.deps.Client.Caches.Guilds() {
			id := g.ID.String()
			icon := ""
			if u := g.IconURL(); u != nil {
				icon = *u
			}
			byID[id] = guildSummary{
				ID:      id,
				Name:    g.Name,
				IconURL: icon,
				InCache: true,
			}
		}
	}

	if s.deps.Repo != nil {
		dbGuilds, err := s.deps.Repo.GetAllGuilds(ctx)
		if err != nil {
			return nil, err
		}
		for _, id := range dbGuilds {
			if _, exists := byID[id]; !exists {
				byID[id] = guildSummary{
					ID:      id,
					Name:    "Guild " + id,
					InCache: false,
				}
			}
		}
	}

	list := make([]guildSummary, 0, len(byID))
	for _, g := range byID {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].InCache != list[j].InCache {
			return list[i].InCache
		}
		ni, nj := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if ni != nj {
			return ni < nj
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

func (s *Server) lookupGuildSummary(guildID string) guildSummary {
	summary := guildSummary{
		ID:      guildID,
		Name:    "Guild " + guildID,
		InCache: false,
	}
	if s.deps.Client == nil || s.deps.Client.Caches == nil {
		return summary
	}
	sf, err := snowflake.Parse(guildID)
	if err != nil {
		return summary
	}
	g, ok := s.deps.Client.Caches.Guild(sf)
	if !ok {
		return summary
	}
	summary.Name = g.Name
	summary.InCache = true
	if u := g.IconURL(); u != nil {
		summary.IconURL = *u
	}
	return summary
}

func (s *Server) guildChannels(guildID string) (textChannels []channelOption, voiceChannels []channelOption, nameByID map[string]string) {
	nameByID = make(map[string]string)
	if s.deps.Client == nil || s.deps.Client.Caches == nil {
		return nil, nil, nameByID
	}
	sf, err := snowflake.Parse(guildID)
	if err != nil {
		return nil, nil, nameByID
	}

	for ch := range s.deps.Client.Caches.ChannelsForGuild(sf) {
		id := ch.ID().String()
		name := ch.Name()
		nameByID[id] = name
		switch ch.Type() {
		case discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews:
			textChannels = append(textChannels, channelOption{ID: id, Name: name})
		case discord.ChannelTypeGuildVoice, discord.ChannelTypeGuildStageVoice:
			voiceChannels = append(voiceChannels, channelOption{ID: id, Name: name})
		}
	}

	sortChannels := func(opts []channelOption) {
		sort.Slice(opts, func(i, j int) bool {
			ni, nj := strings.ToLower(opts[i].Name), strings.ToLower(opts[j].Name)
			if ni != nj {
				return ni < nj
			}
			return opts[i].ID < opts[j].ID
		})
	}
	sortChannels(textChannels)
	sortChannels(voiceChannels)
	return textChannels, voiceChannels, nameByID
}

func (s *Server) loadGuildSettings(ctx context.Context, guildID string) (map[string]string, error) {
	if s.deps.Repo == nil {
		return map[string]string{}, nil
	}
	return s.deps.Repo.GetGuildSettingsMap(ctx, guildID)
}

func (s *Server) buildGuildPageData(ctx context.Context, guildID string) (guildPageData, error) {
	settings, err := s.loadGuildSettings(ctx, guildID)
	if err != nil {
		return guildPageData{}, err
	}

	guild := s.lookupGuildSummary(guildID)
	return guildPageData{
		Title:       guild.Name,
		ActiveNav:   "guilds",
		Guild:       guild,
		General:     s.buildGeneralSection(guildID, settings),
		Prompts:     s.buildPromptsSection(guildID, settings),
		Channels:    s.buildChannelsSection(guildID, settings),
		Voice:       s.buildVoiceSection(guildID, settings),
		Reengage:    s.buildReengageSection(guildID, settings),
		Bookkeeping: s.buildBookkeeping(guildID, settings),
	}, nil
}

func (s *Server) buildGeneralSection(guildID string, settings map[string]string) generalSectionData {
	sec := generalSectionData{
		GuildID:              guildID,
		State:                "on",
		Threshold:            strconv.FormatFloat(database.DefaultThreshold, 'f', -1, 64),
		ThresholdDefault:     strconv.FormatFloat(database.DefaultThreshold, 'f', -1, 64),
		ThresholdSexe:        strconv.FormatFloat(database.DefaultThresholdSexe, 'f', -1, 64),
		ThresholdSexeDefault: strconv.FormatFloat(database.DefaultThresholdSexe, 'f', -1, 64),
		Temperature:          strconv.FormatFloat(database.DefaultTemperature, 'f', -1, 32),
		TemperatureDefault:   strconv.FormatFloat(database.DefaultTemperature, 'f', -1, 32),
		MessagesCount:        strconv.Itoa(database.DefaultMessagesCount),
		MessagesCountDefault: strconv.Itoa(database.DefaultMessagesCount),
		MemoryEnabled:        true,
		TriggerWordsDefault:  strings.Join(s.defaultTriggerWords(), ", "),
	}
	sec.TriggerWords = sec.TriggerWordsDefault

	if v, ok := settings["state"]; ok {
		sec.State = v
		sec.StateOverride = true
	}
	if v, ok := settings["threshold"]; ok {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			sec.Threshold = strconv.FormatFloat(parsed, 'f', -1, 64)
			sec.ThresholdOverride = true
		}
	}
	if v, ok := settings["thresholdSexe"]; ok {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			sec.ThresholdSexe = strconv.FormatFloat(parsed, 'f', -1, 64)
			sec.ThresholdSexeOverride = true
		}
	}
	if v, ok := settings["temperature"]; ok {
		if parsed, err := strconv.ParseFloat(v, 32); err == nil {
			sec.Temperature = strconv.FormatFloat(parsed, 'f', -1, 32)
			sec.TemperatureOverride = true
		}
	}
	if v, ok := settings["messagescount"]; ok {
		if parsed, err := strconv.Atoi(v); err == nil {
			sec.MessagesCount = strconv.Itoa(parsed)
			sec.MessagesCountOverride = true
		}
	}
	if v, ok := settings["memory_enabled"]; ok {
		if parsed, err := strconv.ParseBool(v); err == nil {
			sec.MemoryEnabled = parsed
			sec.MemoryEnabledOverride = true
		}
	}
	if v, ok := settings["trigger_words"]; ok {
		var words []string
		if err := json.Unmarshal([]byte(v), &words); err == nil {
			sec.TriggerWords = strings.Join(triggerwords.NormalizeAll(words), ", ")
			sec.TriggerWordsOverride = true
		}
	}
	return sec
}

func (s *Server) buildPromptsSection(guildID string, settings map[string]string) promptsSectionData {
	sec := promptsSectionData{
		GuildID:            guildID,
		PromptDefault:      s.defaultPrompt(),
		VoicePromptDefault: s.defaultVoicePrompt(),
	}
	if v, ok := settings["prompt"]; ok {
		sec.Prompt = v
		sec.PromptOverride = true
	}
	if v, ok := settings["voice_prompt"]; ok {
		sec.VoicePrompt = v
		sec.VoicePromptOverride = true
	}
	return sec
}

func (s *Server) buildChannelsSection(guildID string, settings map[string]string) channelsSectionData {
	textChannels, _, _ := s.guildChannels(guildID)
	sec := channelsSectionData{
		GuildID:      guildID,
		TextChannels: textChannels,
	}
	if v, ok := settings["horoscope_channel"]; ok {
		sec.HoroscopeChannel = v
		sec.HoroscopeChannelOverride = true
		sec.HoroscopeInCache = containsChannel(textChannels, v)
	}
	if v, ok := settings["farting_friday_channel"]; ok {
		sec.FartingFridayChannel = v
		sec.FartingFridayChannelOverride = true
		sec.FartingFridayInCache = containsChannel(textChannels, v)
	}
	return sec
}

func (s *Server) buildVoiceSection(guildID string, settings map[string]string) voiceSectionData {
	_, voiceChannels, _ := s.guildChannels(guildID)
	sec := voiceSectionData{
		GuildID:       guildID,
		VoiceChannels: voiceChannels,
		VoiceEnabled:  true,
		AutoJoin:      false,
	}
	if v, ok := settings["voice_enabled"]; ok {
		if parsed, err := strconv.ParseBool(v); err == nil {
			sec.VoiceEnabled = parsed
			sec.VoiceEnabledOverride = true
		}
	}
	if v, ok := settings["voice_auto_join"]; ok {
		if parsed, err := strconv.ParseBool(v); err == nil {
			sec.AutoJoin = parsed
			sec.AutoJoinOverride = true
		}
	}
	if v, ok := settings["voice_auto_join_channel"]; ok {
		sec.AutoJoinChannel = v
		sec.AutoJoinChannelOverride = true
		sec.AutoJoinChannelInCache = containsChannel(voiceChannels, v)
	}
	return sec
}

func (s *Server) buildReengageSection(guildID string, settings map[string]string) reengageSectionData {
	textChannels, _, nameByID := s.guildChannels(guildID)
	reengageCfg := s.reengageConfig()
	defaultChanceStr := strconv.FormatFloat(reengageCfg.DefaultChance, 'f', -1, 64)
	defaultThresholdStr := strconv.Itoa(reengageCfg.DefaultInactivityMinutes)

	sec := reengageSectionData{
		GuildID:          guildID,
		TextChannels:     textChannels,
		MessageDefault:   reengageCfg.ReengageMessage,
		DefaultChance:    defaultChanceStr,
		DefaultThreshold: defaultThresholdStr,
	}
	if v, ok := settings["reengage_message"]; ok {
		sec.Message = v
		sec.MessageOverride = true
	}

	channelIDs := make(map[string]struct{})
	for k := range settings {
		switch {
		case strings.HasPrefix(k, "reengage_enabled:"):
			channelIDs[strings.TrimPrefix(k, "reengage_enabled:")] = struct{}{}
		case strings.HasPrefix(k, "reengage_chance:"):
			channelIDs[strings.TrimPrefix(k, "reengage_chance:")] = struct{}{}
		case strings.HasPrefix(k, "reengage_threshold:"):
			channelIDs[strings.TrimPrefix(k, "reengage_threshold:")] = struct{}{}
		}
	}

	rows := make([]reengageChannelRow, 0, len(channelIDs))
	for chID := range channelIDs {
		row := reengageChannelRow{
			ChannelID:   chID,
			ChannelName: nameByID[chID],
			Chance:      defaultChanceStr,
			Threshold:   defaultThresholdStr,
		}
		if v, ok := settings["reengage_enabled:"+chID]; ok {
			if parsed, err := strconv.ParseBool(v); err == nil {
				row.Enabled = parsed
				row.EnabledOverride = true
			}
		}
		if v, ok := settings["reengage_chance:"+chID]; ok {
			if parsed, err := strconv.ParseFloat(v, 64); err == nil {
				row.Chance = strconv.FormatFloat(parsed, 'f', -1, 64)
				row.ChanceOverride = true
			}
		}
		if v, ok := settings["reengage_threshold:"+chID]; ok {
			if parsed, err := strconv.Atoi(v); err == nil {
				row.Threshold = strconv.Itoa(parsed)
				row.ThresholdOverride = true
			}
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].ChannelID < rows[j].ChannelID
	})
	sec.Channels = rows
	return sec
}

func (s *Server) buildBookkeeping(guildID string, settings map[string]string) []bookkeepingRow {
	_, _, nameByID := s.guildChannels(guildID)
	var rows []bookkeepingRow

	if v, ok := settings["last_message"]; ok {
		rows = append(rows, bookkeepingRow{
			Key:       "last_message",
			Label:     "Guild last message",
			RawValue:  v,
			Formatted: formatUnixTimestamp(v),
		})
	}

	var chKeys []string
	for k := range settings {
		if strings.HasPrefix(k, "channel_last_message:") {
			chKeys = append(chKeys, k)
		}
	}
	sort.Strings(chKeys)

	for _, k := range chKeys {
		chID := strings.TrimPrefix(k, "channel_last_message:")
		label := "Channel " + chID
		if name := nameByID[chID]; name != "" {
			label = "#" + name + " (" + chID + ")"
		}
		v := settings[k]
		rows = append(rows, bookkeepingRow{
			Key:       k,
			Label:     label,
			RawValue:  v,
			Formatted: formatUnixTimestamp(v),
		})
	}
	return rows
}

func (s *Server) defaultPrompt() string {
	if s.deps.Config != nil && s.deps.Config.Bot.DefaultPrompt != "" {
		return s.deps.Config.Bot.DefaultPrompt
	}
	return config.DefaultConfig().Bot.DefaultPrompt
}

func (s *Server) defaultVoicePrompt() string {
	if s.deps.Config != nil && s.deps.Config.Voice.VoiceSystemPrompt != "" {
		return s.deps.Config.Voice.VoiceSystemPrompt
	}
	return config.GetVoiceConfigDefaults().VoiceSystemPrompt
}

func (s *Server) defaultTriggerWords() []string {
	if s.deps.Config != nil && len(s.deps.Config.Bot.TriggerWords) > 0 {
		return triggerwords.NormalizeAll(s.deps.Config.Bot.TriggerWords)
	}
	return triggerwords.NormalizeAll(config.DefaultConfig().Bot.TriggerWords)
}

func (s *Server) reengageConfig() config.ReengageConfig {
	if s.deps.Config != nil {
		cfg := s.deps.Config.Reengage
		if cfg.DefaultInactivityMinutes <= 0 {
			cfg.DefaultInactivityMinutes = database.DefaultReengageThreshold
		}
		if cfg.ReengageMessage == "" {
			cfg.ReengageMessage = config.DefaultConfig().Reengage.ReengageMessage
		}
		return cfg
	}
	return config.DefaultConfig().Reengage
}

func parseFloatRange(raw string, minVal, maxVal float64) (float64, error) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < minVal || v > maxVal {
		return 0, fmt.Errorf("out of range")
	}
	return v, nil
}

func containsChannel(channels []channelOption, id string) bool {
	for _, ch := range channels {
		if ch.ID == id {
			return true
		}
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatUnixTimestamp(raw string) string {
	ts, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ts <= 0 {
		return raw
	}
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}
