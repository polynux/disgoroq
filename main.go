package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/commands"
	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/emoji"
	"polynux/disgoroq/handlers"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/medialibrary"
	"polynux/disgoroq/memory"
	"polynux/disgoroq/scheduler"
	"polynux/disgoroq/utils"
	voicepkg "polynux/disgoroq/voice"
)

var (
	cfg *config.Config

	local                   bool
	sendDirectHoroscope     bool
	sendDirectFartingFriday bool
	clearCommands           bool
	importMediaLibrary      string
	mediaSearchQuery        string
	mediaSearchGuild        string
	mediaSearchTrigger      bool
	mediaCheckURL           string
)

func init() {
	flag.BoolVar(&local, "local", false, "Use local database")
	flag.BoolVar(&sendDirectHoroscope, "sendDirectHoroscope", false, "Send horoscope directly")
	flag.BoolVar(&sendDirectFartingFriday, "sendFartingFriday", false, "Send farting friday directly")
	flag.BoolVar(&clearCommands, "clearCommands", false, "Clear all registered commands")
	flag.StringVar(&importMediaLibrary, "import-media-library", "", "Import media library entries from a JSONL file and exit")
	flag.StringVar(&mediaSearchQuery, "media-search", "", "Search media library entries by query text and exit (requires -guild)")
	flag.StringVar(&mediaSearchGuild, "guild", "", "Guild ID for -media-search")
	flag.BoolVar(&mediaSearchTrigger, "trigger", false, "Search trigger embeddings instead of description embeddings in -media-search")
	flag.StringVar(&mediaCheckURL, "media-check", "", "Dry-run refresh and link validation check for a media URL and exit")
	if !runningUnderGoTest() {
		flag.Parse()
	}
}

func runningUnderGoTest() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}

func main() {
	logger.InitFromConfig(&config.DefaultConfig().Logging)

	// Load .env.local file if it exists
	if err := godotenv.Load(".env.local"); err != nil {
		// Try .env as fallback
		if err := godotenv.Load(".env"); err != nil {
			logger.Info("No .env.local or .env file found, using environment variables")
		}
	}

	// Load configuration
	var configErr error
	cfg, configErr = config.Load(config.DefaultConfigPath)
	if configErr != nil {
		logger.Fatal("Failed to load configuration", zap.Error(configErr))
	}

	// Initialize logger with configuration
	logger.InitFromConfig(&cfg.Logging)

	// CLI modes run without opening a Discord connection and exit when done.
	cliModes := 0
	if importMediaLibrary != "" {
		cliModes++
	}
	if mediaSearchQuery != "" {
		cliModes++
	}
	if mediaCheckURL != "" {
		cliModes++
	}
	if cliModes > 1 {
		logger.Fatal("Cannot combine -import-media-library, -media-search, and -media-check")
	}
	if mediaCheckURL != "" {
		cliCtx, cliStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
		cliErr := runMediaCheck(cliCtx, cfg, mediaCheckURL, os.Stdout)
		cliStop()
		logger.Sync()
		if cliErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", cliErr)
			os.Exit(1)
		}
		return
	}
	if importMediaLibrary != "" || mediaSearchQuery != "" {
		utils.SetLogger(logger.Log)
		utils.InitializeDB(&cfg.Database, local)

		cliCtx, cliStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
		var cliErr error
		if importMediaLibrary != "" {
			cliErr = runMediaLibraryImport(cliCtx, cfg, importMediaLibrary, os.Stdout)
		} else {
			cliErr = runMediaLibrarySearch(cliCtx, cfg, mediaSearchQuery, mediaSearchGuild, mediaSearchTrigger, os.Stdout)
		}
		cliStop()

		if err := utils.DB.Close(); err != nil {
			logger.Error("Error closing database", zap.Error(err))
		}
		logger.Sync()
		if cliErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", cliErr)
			os.Exit(1)
		}
		return
	}

	logger.Info("Starting DisgoroQ bot")

	// Create slog adapter for disgo
	slogLogger := logger.NewSlogLogger()

	// Create disgo client
	client, err := disgo.New(cfg.Discord.Token,
		bot.WithLogger(slogLogger),
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(
				gateway.IntentGuilds,
				gateway.IntentGuildMessages,
				gateway.IntentGuildMessageReactions,
				gateway.IntentMessageContent,
				gateway.IntentGuildVoiceStates,
			),
		),
		bot.WithCacheConfigOpts(
			cache.WithCaches(cache.FlagGuilds|cache.FlagVoiceStates),
		),
		bot.WithVoiceManagerConfigOpts(
			voice.WithDaveSessionCreateFunc(golibdave.NewSession),
		),
	)
	if err != nil {
		logger.Fatal("Error creating Discord client", zap.Error(err))
		return
	}

	// Initialize database
	utils.SetLogger(logger.Log)
	utils.InitializeDB(&cfg.Database, local)
	defer func() {
		logger.Info("Closing database")
		if err := utils.DB.Close(); err != nil {
			logger.Error("Error closing database", zap.Error(err))
		}
		defer logger.Sync()
	}()

	eventRepo := logger.NewEventRepository(utils.DB, logger.Log)
	logger.SetEventRepository(eventRepo)
	repo := database.NewRepository()

	// Initialize media library service if enabled.
	var mediaLibraryService *medialibrary.Service
	if cfg.MediaLibrary.Enabled {
		embURL, embModel := resolveMediaEmbeddingConfig(cfg)
		mediaEmbedder, err := memory.NewOllamaEmbeddingProviderWithConfig(embURL, embModel)
		if err != nil {
			logger.Warn("Failed to create media library embedding provider; vector search disabled, exact match still active",
				zap.String("ollama_url", embURL),
				zap.String("embedding_model", embModel),
				zap.Error(err))
			mediaLibraryService = medialibrary.NewService(medialibrary.NewRepository(utils.DB), nil, cfg.MediaLibrary)
		} else {
			mediaLibraryService = medialibrary.NewService(medialibrary.NewRepository(utils.DB), mediaEmbedder, cfg.MediaLibrary)
			hcCtx, hcCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if hcErr := mediaEmbedder.HealthCheck(hcCtx); hcErr != nil {
				logger.Warn("Media library embedding provider unavailable at startup; exact match active, vector search will retry",
					zap.String("ollama_url", embURL),
					zap.String("embedding_model", embModel),
					zap.Error(hcErr))
			} else {
				logger.Info("Media library initialized",
					zap.String("ollama_url", embURL),
					zap.String("embedding_model", embModel),
					zap.Float64("min_similarity", cfg.MediaLibrary.MinSimilarity),
					zap.Float64("min_trigger_similarity", cfg.MediaLibrary.MinTriggerSimilarity),
					zap.Bool("cross_guild", cfg.MediaLibrary.CrossGuild),
					zap.Bool("allow_discord_cdn", cfg.MediaLibrary.AllowDiscordCDN),
					zap.Int("repeat_window", cfg.MediaLibrary.RepeatWindow),
					zap.Bool("random_gif_fallback", cfg.MediaLibrary.RandomGifFallback),
					zap.Bool("gif_on_request", cfg.MediaLibrary.GifOnRequest),
					zap.Bool("validate_links", cfg.MediaLibrary.ValidateLinks))
			}
			hcCancel()
		}
	} else {
		logger.Info("Media library disabled by configuration")
	}
	if mediaLibraryService != nil {
		mediaRefreshClient := medialibrary.NewDiscordRefreshClient(
			cfg.Discord.Token,
			rest.WithLogger(slogLogger),
		)
		mediaLibraryService.SetDiscordRestClient(mediaRefreshClient)
		defer func() {
			mediaLibraryService.Close()
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer closeCancel()
			mediaRefreshClient.Close(closeCtx)
		}()
	}

	// Create AI service configuration from central config
	aiServiceConfig := ai.ServiceConfig{
		PrimaryProvider:             cfg.AI.PrimaryProvider,
		VisionProvider:              cfg.AI.VisionProvider,
		GroqAPIKey:                  cfg.AI.Groq.APIKey,
		GroqModel:                   cfg.AI.Groq.Model,
		GroqVisionModel:             cfg.AI.Groq.VisionModel,
		GroqThinkingEnabled:         cfg.AI.Groq.ThinkingEnabled,
		OllamaEnabled:               cfg.AI.Ollama.Enabled,
		OllamaURL:                   cfg.AI.Ollama.URL,
		OllamaModel:                 cfg.AI.Ollama.Model,
		OllamaVisionModel:           cfg.AI.Ollama.VisionModel,
		OllamaThinkingEnabled:       cfg.AI.Ollama.ThinkingEnabled,
		OllamaCompletion:            cfg.AI.Ollama.Completion,
		OllamaCompletionSpeaker:     cfg.AI.Ollama.CompletionSpeaker,
		OllamaCompletionPromptChars: cfg.AI.Ollama.CompletionPromptChars,
		OllamaCompletionBotLines:    cfg.AI.Ollama.CompletionBotLines,
		OllamaCompletionMediaTags:   cfg.AI.Ollama.CompletionMediaTags,
		OpencodeEnabled:             cfg.AI.Opencode.Enabled,
		OpencodeBaseURL:             cfg.AI.Opencode.BaseURL,
		OpencodeAPIKey:              cfg.AI.Opencode.APIKey,
		OpencodeModel:               cfg.AI.Opencode.Model,
		OpencodeVisionModel:         cfg.AI.Opencode.VisionModel,
		OpencodeThinkingEnabled:     cfg.AI.Opencode.ThinkingEnabled,
		OpenrouterEnabled:           cfg.AI.Openrouter.Enabled,
		OpenrouterBaseURL:           cfg.AI.Openrouter.BaseURL,
		OpenrouterAPIKey:            cfg.AI.Openrouter.APIKey,
		OpenrouterModel:             cfg.AI.Openrouter.Model,
		OpenrouterVisionModel:       cfg.AI.Openrouter.VisionModel,
		OpenrouterThinkingEnabled:   cfg.AI.Openrouter.ThinkingEnabled,
		RetryConfig: ai.RetryConfig{
			MaxRetries:    cfg.AI.Retry.MaxRetries,
			InitialDelay:  cfg.AI.Retry.InitialDelay,
			MaxDelay:      cfg.AI.Retry.MaxDelay,
			BackoffFactor: cfg.AI.Retry.BackoffFactor,
			RetryOnEmpty:  cfg.AI.Retry.RetryOnEmpty,
			RetryOnError:  cfg.AI.Retry.RetryOnError,
		},
		MinResponseLength: cfg.AI.MinResponseLength,
		FallbackEnabled:   cfg.AI.FallbackEnabled,
		FallbackOrder:     append([]string(nil), cfg.AI.FallbackOrder...),
		AttachmentCache:   repo,
		ToolConfig: ai.ToolRuntimeConfig{
			Enabled:          cfg.AI.Tools.Enabled,
			MaxRounds:        cfg.AI.Tools.MaxRounds,
			MaxCallsPerRound: cfg.AI.Tools.MaxCallsPerRound,
			MaxCallsTotal:    cfg.AI.Tools.MaxCallsTotal,
			Timeout:          cfg.AI.Tools.Timeout,
			Web: ai.WebToolConfig{
				Enabled:              cfg.AI.Tools.Web.Enabled,
				AllowedSchemes:       append([]string(nil), cfg.AI.Tools.Web.AllowedSchemes...),
				UserAgent:            cfg.AI.Tools.Web.UserAgent,
				MaxBytes:             cfg.AI.Tools.Web.MaxBytes,
				MaxCharacters:        cfg.AI.Tools.Web.MaxCharacters,
				AllowPrivateNetworks: cfg.AI.Tools.Web.AllowPrivateNetworks,
			},
			Search: ai.SearchToolConfig{
				Enabled:         cfg.AI.Tools.Search.Enabled,
				Provider:        cfg.AI.Tools.Search.Provider,
				BaseURL:         cfg.AI.Tools.Search.BaseURL,
				UserAgent:       cfg.AI.Tools.Search.UserAgent,
				MaxResults:      cfg.AI.Tools.Search.MaxResults,
				MaxCharacters:   cfg.AI.Tools.Search.MaxCharacters,
				DefaultLanguage: cfg.AI.Tools.Search.DefaultLanguage,
				SafeSearch:      cfg.AI.Tools.Search.SafeSearch,
			},
		},
	}
	if mediaLibraryService != nil {
		aiServiceConfig.MediaResolver = mediaLibraryService
	}

	if err := aiServiceConfig.Validate(); err != nil {
		logger.Fatal("Invalid AI service configuration", zap.Error(err))
		return
	}

	// Create AI service with retry and fallback capabilities
	aiService := ai.NewService(aiServiceConfig)
	if aiService == nil {
		logger.Fatal("Failed to initialize AI service")
		return
	}

	// Log AI service configuration
	logger.Info("AI service initialized",
		zap.String("primary_provider", aiServiceConfig.PrimaryProvider),
		zap.String("vision_provider", aiServiceConfig.VisionProvider),
		zap.Bool("fallback_enabled", aiService.IsFallbackAvailable()),
		zap.Strings("fallback_order", aiServiceConfig.FallbackOrder),
		zap.Int("max_retries", aiServiceConfig.RetryConfig.MaxRetries),
		zap.Duration("retry_delay", aiServiceConfig.RetryConfig.InitialDelay))

	// Initialize memory service for conversation context
	var memoryService memory.Service
	if cfg.Memory.Enabled {
		// Create memory repository using the existing database connection
		memoryRepo := memory.NewRepository(utils.DB)

		// Create Ollama embedding provider for vector search
		embeddingProvider, err := memory.NewOllamaEmbeddingProviderWithConfig(
			cfg.Memory.OllamaURL,
			cfg.Memory.EmbeddingModel,
		)
		if err != nil {
			logger.Warn("Failed to create embedding provider, continuing without memory",
				zap.Error(err),
				zap.String("ollama_url", cfg.Memory.OllamaURL))
			memoryService = nil
		} else {
			// Build a dedicated summarization provider honoring
			// memory.summary_provider / memory.summary_model instead of the
			// chat provider chain (which overrides the requested model).
			summaryProviderCfg := buildSummaryProviderConfig(cfg)

			summaryProvider, err := ai.NewSummaryProvider(summaryProviderCfg)
			if err != nil {
				logger.Warn("Failed to create summary provider, continuing without memory",
					zap.Error(err),
					zap.String("summary_provider", cfg.Memory.SummaryProvider),
					zap.String("summary_model", cfg.Memory.SummaryModel))
				memoryService = nil
			} else {
				// Create AI summarizer backed by the dedicated summary provider
				memoryAIService := &aiServiceAdapter{service: summaryProvider}
				summarizer := memory.NewSummarizer(memoryAIService, cfg.Memory.SummaryModel)

				// Configure memory service from central config
				memoryServiceConfig := memory.ServiceConfig{
					BufferThreshold:   cfg.Memory.BufferThreshold,
					SummaryInterval:   cfg.Memory.SummaryInterval,
					MaxSummaryContext: cfg.Memory.MaxSummaryContext,
				}

				memoryService = memory.NewService(memoryRepo, embeddingProvider, summarizer, memoryServiceConfig)

				// Test memory service health
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				if err := embeddingProvider.HealthCheck(ctx); err != nil {
					logger.Warn("Memory service embedding provider unavailable, continuing without memory",
						zap.Error(err),
						zap.String("ollama_url", cfg.Memory.OllamaURL),
						zap.String("embedding_model", cfg.Memory.EmbeddingModel))
					memoryService = nil
				} else {
					logger.Info("Memory service initialized",
						zap.String("ollama_url", cfg.Memory.OllamaURL),
						zap.String("embedding_model", cfg.Memory.EmbeddingModel),
						zap.String("summary_provider", cfg.Memory.SummaryProvider),
						zap.String("summary_model", cfg.Memory.SummaryModel),
						zap.Int("buffer_threshold", memoryServiceConfig.BufferThreshold),
						zap.Duration("summary_interval", memoryServiceConfig.SummaryInterval))
				}
			}
		}
	} else {
		logger.Info("Memory service disabled by configuration")
	}

	// Initialize emoji manager for shortcode conversion
	emojiManager := emoji.NewManager(client, cfg.Emoji)
	logger.Info("Emoji manager initialized")

	messageHandler := handlers.NewMessageHandler(client, aiService, repo, memoryService, emojiManager, cfg.Bot.DefaultPrompt, cfg.Bot.TriggerWords, cfg.Votes)
	if mediaLibraryService != nil {
		messageHandler.SetMediaLibrary(mediaLibraryService)
	}
	voteHandler := handlers.NewVoteHandler(client, repo, cfg.Votes)
	client.AddEventListeners(
		bot.NewListenerFunc(messageHandler.HandleMessageCreate),
		bot.NewListenerFunc(messageHandler.HandleMessageUpdate),
		bot.NewListenerFunc(messageHandler.HandleMessageDelete),
		bot.NewListenerFunc(voteHandler.HandleGuildMessageReactionAdd),
		bot.NewListenerFunc(voteHandler.HandleGuildMessageReactionRemove),
		bot.NewListenerFunc(voteHandler.HandleGuildMessageReactionRemoveEmoji),
		bot.NewListenerFunc(voteHandler.HandleGuildMessageReactionRemoveAll),
		bot.NewListenerFunc(handlers.HandleGuildJoin),
		bot.NewListenerFunc(handlers.HandleGuildLeave),
	)

	// Initialize voice orchestrator if enabled
	var voiceOrchestrator *voicepkg.Orchestrator
	if cfg.Voice.Enabled {
		sttClient := voicepkg.NewWhisperClient(voicepkg.WhisperConfig{
			SocketPath: cfg.Voice.STT.SocketPath,
			Model:      cfg.Voice.STT.Model,
			Language:   cfg.Voice.STT.Language,
		})
		ttsClient := voicepkg.NewTTSHTTPClient(voicepkg.TTSHTTPConfig{
			Endpoint:     cfg.Voice.TTS.Endpoint,
			Model:        cfg.Voice.TTS.Model,
			DefaultVoice: cfg.Voice.TTS.DefaultVoice,
			SampleRate:   cfg.Voice.TTS.SampleRate,
			TimeoutMs:    cfg.Voice.TTS.TimeoutMs,
		})

		// Create orchestrator
		voiceOrchestrator = voicepkg.NewOrchestrator(voicepkg.OrchestratorConfig{
			STTClient:         sttClient,
			TTSClient:         ttsClient,
			AIService:         aiService,
			MemoryService:     memoryService,
			Client:            client,
			Repository:        repo,
			DefaultPrompt:     cfg.Bot.DefaultPrompt,
			VoiceConfig:       cfg.Voice,
			VoiceSystemPrompt: cfg.Voice.VoiceSystemPrompt,
		})

		// Add voice state handler for auto-join
		voiceHandler := handlers.NewVoiceHandler(voiceOrchestrator, repo, client)
		client.AddEventListeners(
			bot.NewListenerFunc(voiceHandler.HandleVoiceStateUpdate),
			bot.NewListenerFunc(voiceHandler.HandleVoiceServerUpdate),
		)

		logger.Info("Voice orchestrator initialized",
			zap.String("stt_socket", cfg.Voice.STT.SocketPath),
			zap.String("tts_endpoint", cfg.Voice.TTS.Endpoint))
	} else {
		logger.Info("Voice chat disabled by configuration")
	}

	registry, err := commands.NewRegistry(client, local, cfg.Discord.DevGuildIDs)
	if err != nil {
		logger.Fatal("Invalid command registry configuration", zap.Error(err))
	}
	commands.RegisterAll(registry, repo, memoryService, cfg.Reengage, cfg.Bot.DefaultPrompt, cfg.Bot.TriggerWords, cfg.Voice.VoiceSystemPrompt, voiceOrchestrator, client)
	client.AddEventListeners(bot.NewListenerFunc(func(e *events.ApplicationCommandInteractionCreate) {
		go func() {
			// disgo only recovers panics in inline listeners.
			defer func() {
				if r := recover(); r != nil {
					logger.Error("Recovered panic in command handler", zap.Any("panic", r))
				}
			}()
			registry.HandleCommand(e)
		}()
	}))

	err = client.OpenGateway(context.Background())
	if err != nil {
		logger.Fatal("Error opening discord connection", zap.Error(err))
		return
	}
	defer client.Close(context.Background())

	logger.Info("Bot is now running. Press CTRL-C to exit.")

	if clearCommands {
		clearAllCommands(client)
		return
	}

	err = registry.Register(context.Background())
	if err != nil {
		logger.Fatal("Error registering commands", zap.Error(err))
	}
	logger.Info("Commands registered successfully")

	sched := scheduler.New(client, aiService, repo, emojiManager, cfg.Horoscope, memoryService, cfg.Reengage, cfg.Bot.DefaultPrompt, cfg.Memory)
	sched.Start()
	defer sched.Shutdown()

	if sendDirectHoroscope {
		sched.SendHoroscope()
	}
	if sendDirectFartingFriday {
		sched.SendFartingFriday()
	}

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	logger.Info("Shutting down gracefully")

	// Close memory service (cancels in-flight summarizations)
	if memoryService != nil {
		memoryService.Close()
	}

	// Close voice orchestrator if initialized
	if voiceOrchestrator != nil {
		if err := voiceOrchestrator.Close(); err != nil {
			logger.Error("Error closing voice orchestrator", zap.Error(err))
		}
	}
}

// buildSummaryProviderConfig resolves provider-specific API key fallbacks and
// base URLs for the dedicated memory summarization provider.
func buildSummaryProviderConfig(cfg *config.Config) ai.SummaryProviderConfig {
	summaryCfg := ai.SummaryProviderConfig{
		Provider:  cfg.Memory.SummaryProvider,
		Model:     cfg.Memory.SummaryModel,
		OllamaURL: cfg.Memory.OllamaURL,
		APIKey:    cfg.Memory.SummaryAPIKey,
	}
	switch cfg.Memory.SummaryProvider {
	case config.SummaryProviderGroq:
		if summaryCfg.APIKey == "" {
			summaryCfg.APIKey = cfg.AI.Groq.APIKey
		}
	case config.SummaryProviderOpenrouter:
		if summaryCfg.APIKey == "" {
			summaryCfg.APIKey = cfg.AI.Openrouter.APIKey
		}
	case config.SummaryProviderOpencode:
		if summaryCfg.APIKey == "" {
			summaryCfg.APIKey = cfg.AI.Opencode.APIKey
		}
		summaryCfg.OpencodeBaseURL = cfg.AI.Opencode.BaseURL
	}
	return summaryCfg
}

// aiServiceAdapter adapts an ai.Provider to the memory.AIService interface.
// The provider (built by ai.NewSummaryProvider) honors the requested model.
type aiServiceAdapter struct {
	service ai.Provider
}

// Chat implements the memory.AIService interface
func (a *aiServiceAdapter) Chat(ctx context.Context, messages []memory.Message, model string) (string, error) {
	// Convert memory.Message format to ai.ChatRequest format
	aiMessages := make([]ai.Message, len(messages))
	for i, msg := range messages {
		aiMessages[i] = ai.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	request := &ai.ChatRequest{
		Model:       model,
		Messages:    aiMessages,
		ToolChoice:  &ai.ToolChoice{Mode: ai.ToolChoiceNone},
		Temperature: database.DefaultTemperature,
		MaxTokens:   database.DefaultMaxTokens,
	}

	response, err := a.service.Chat(ctx, request)
	if err != nil {
		return "", err
	}

	return response.Content, nil
}

func clearAllCommands(client *bot.Client) {
	logger.Info("Clearing all commands")

	ctx := context.Background()

	totalDeleted := 0

	logger.Info("Clearing global commands")
	existing, err := client.Rest.GetGlobalCommands(client.ID(), false, rest.WithCtx(ctx))
	if err != nil {
		logger.Error("Error fetching global commands", zap.Error(err))
	} else {
		for _, cmd := range existing {
			err := client.Rest.DeleteGlobalCommand(client.ID(), cmd.ID(), rest.WithCtx(ctx))
			if err != nil {
				logger.Error("Error deleting global command",
					zap.Error(err),
					zap.String("command", cmd.Name()))
			} else {
				logger.Debug("Deleted global command", zap.String("command", cmd.Name()))
				totalDeleted++
			}
		}
	}

	for guild := range client.Caches.Guilds() {
		logger.Debug("Clearing commands for guild", zap.String("guild", guild.Name))
		existing, err := client.Rest.GetGuildCommands(client.ID(), guild.ID, false, rest.WithCtx(ctx))
		if err != nil {
			logger.Error("Error fetching commands for guild",
				zap.Error(err),
				zap.String("guild", guild.Name),
			)
			continue
		}
		for _, cmd := range existing {
			err := client.Rest.DeleteGuildCommand(client.ID(), guild.ID, cmd.ID(), rest.WithCtx(ctx))
			if err != nil {
				logger.Error("Error deleting guild command",
					zap.Error(err),
					zap.String("command", cmd.Name()),
					zap.String("guild", guild.Name),
				)
			} else {
				logger.Debug("Deleted guild command",
					zap.String("command", cmd.Name()),
					zap.String("guild", guild.Name),
				)
				totalDeleted++
			}
		}
	}

	logger.Info("Commands cleared", zap.Int("total_deleted", totalDeleted))
}

// resolveMediaEmbeddingConfig determines the Ollama URL and embedding model for
// the media library so it works even when memory.enabled is false.
func resolveMediaEmbeddingConfig(cfg *config.Config) (string, string) {
	embURL := strings.TrimSpace(cfg.Memory.OllamaURL)
	if embURL == "" {
		embURL = strings.TrimSpace(cfg.AI.Ollama.URL)
	}
	if embURL == "" {
		embURL = "http://localhost:11434"
	}
	embModel := strings.TrimSpace(cfg.Memory.EmbeddingModel)
	if embModel == "" {
		embModel = "nomic-embed-text"
	}
	return embURL, embModel
}

func runMediaLibraryImport(ctx context.Context, cfg *config.Config, path string, out io.Writer) error {
	embURL, embModel := resolveMediaEmbeddingConfig(cfg)
	embedder, err := memory.NewOllamaEmbeddingProviderWithConfig(embURL, embModel)
	if err != nil {
		return fmt.Errorf("create embedding provider (%s, %s): %w", embURL, embModel, err)
	}
	hcCtx, hcCancel := context.WithTimeout(ctx, 10*time.Second)
	err = embedder.HealthCheck(hcCtx)
	hcCancel()
	if err != nil {
		return fmt.Errorf("embedding provider unavailable (%s, %s): %w", embURL, embModel, err)
	}

	svc := medialibrary.NewService(medialibrary.NewRepository(utils.DB), embedder, cfg.MediaLibrary)
	summary, err := svc.ImportFile(ctx, path, func(s medialibrary.ImportSummary) {
		logger.Info("Media library import progress",
			zap.Int("rows", s.Rows),
			zap.Int("embeddings_computed", s.EmbeddingsComputed),
			zap.Int("embeddings_reused", s.EmbeddingsReused),
			zap.Int("skipped", s.Skipped))
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Media library import complete: rows=%d embeddings_computed=%d embeddings_reused=%d skipped=%d\n",
		summary.Rows, summary.EmbeddingsComputed, summary.EmbeddingsReused, summary.Skipped)
	return nil
}

func runMediaLibrarySearch(ctx context.Context, cfg *config.Config, query, guildID string, useTrigger bool, out io.Writer) error {
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return fmt.Errorf("-guild is required with -media-search")
	}
	embURL, embModel := resolveMediaEmbeddingConfig(cfg)
	embedder, err := memory.NewOllamaEmbeddingProviderWithConfig(embURL, embModel)
	if err != nil {
		return fmt.Errorf("create embedding provider (%s, %s): %w", embURL, embModel, err)
	}

	svc := medialibrary.NewService(medialibrary.NewRepository(utils.DB), embedder, cfg.MediaLibrary)
	hits, err := svc.Search(ctx, guildID, query, useTrigger, 10)
	if err != nil {
		return err
	}
	_, _ = io.WriteString(out, formatMediaSearchResults(hits))
	return nil
}

func formatMediaSearchResults(hits []medialibrary.SearchHit) string {
	if len(hits) == 0 {
		return "No matches found.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-6s %-8s %-6s %-32s %s\n", "SIM", "KIND", "USES", "DESCRIPTION", "URL")
	for _, hit := range hits {
		desc := strings.ReplaceAll(hit.Entry.Description, "\n", " ")
		if desc == "" {
			desc = "-"
		}
		fmt.Fprintf(&b, "%.4f %-8s %-6d %-32s %s\n", hit.Similarity, hit.Entry.Kind, hit.Entry.Uses, desc, hit.Entry.URL)
	}
	return b.String()
}

func runMediaCheck(ctx context.Context, cfg *config.Config, rawURL string, out io.Writer) error {
	var restClient rest.Client
	if cfg != nil && strings.TrimSpace(cfg.Discord.Token) != "" {
		restClient = medialibrary.NewDiscordRefreshClient(
			cfg.Discord.Token,
			rest.WithLogger(logger.NewSlogLogger()),
		)
		defer func() {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer closeCancel()
			restClient.Close(closeCtx)
		}()
	}
	return runMediaCheckWithClients(ctx, cfg, rawURL, restClient, nil, out)
}

func runMediaCheckWithClients(ctx context.Context, cfg *config.Config, rawURL string, restClient medialibrary.DiscordRestClient, httpClient *http.Client, out io.Writer) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return fmt.Errorf("-media-check requires a non-empty URL")
	}

	timeout := 5 * time.Second
	if cfg != nil && cfg.MediaLibrary.LookupTimeoutMs > 0 {
		timeout = time.Duration(cfg.MediaLibrary.LookupTimeoutMs) * time.Millisecond
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	report := medialibrary.CheckURL(checkCtx, trimmed, restClient, httpClient)
	if cfg != nil {
		report.Error = redactSecret(report.Error, cfg.Discord.Token)
	}
	_, err := io.WriteString(out, formatMediaCheckReport(report))
	return err
}

func formatMediaCheckReport(report medialibrary.LinkCheckReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "is_attachment: %t\n", report.IsAttachment)
	refreshStatus := report.RefreshStatus
	if refreshStatus == "" {
		refreshStatus = "-"
	}
	fmt.Fprintf(&b, "refresh_status: %s\n", refreshStatus)
	refreshed := report.RefreshedHostPath
	if refreshed == "" {
		refreshed = "-"
	}
	fmt.Fprintf(&b, "refreshed_url: %s\n", refreshed)
	if report.HeadStatus != 0 {
		fmt.Fprintf(&b, "head_status: %d\n", report.HeadStatus)
	} else {
		fmt.Fprintf(&b, "head_status: -\n")
	}
	if report.GetStatus != 0 {
		fmt.Fprintf(&b, "get_status: %d\n", report.GetStatus)
	} else {
		fmt.Fprintf(&b, "get_status: -\n")
	}
	verdict := report.Verdict
	if verdict == "" {
		verdict = "unknown"
	}
	fmt.Fprintf(&b, "verdict: %s\n", verdict)
	if report.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", report.Error)
	}
	return b.String()
}

func redactSecret(s, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" || s == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[REDACTED]")
}


