package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
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
)

func init() {
	flag.BoolVar(&local, "local", false, "Use local database")
	flag.BoolVar(&sendDirectHoroscope, "sendDirectHoroscope", false, "Send horoscope directly")
	flag.BoolVar(&sendDirectFartingFriday, "sendFartingFriday", false, "Send farting friday directly")
	flag.BoolVar(&clearCommands, "clearCommands", false, "Clear all registered commands")
	flag.Parse()
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

	// Create AI service configuration from central config
	aiServiceConfig := ai.ServiceConfig{
		PrimaryProvider:           cfg.AI.PrimaryProvider,
		GroqAPIKey:                cfg.AI.Groq.APIKey,
		GroqModel:                 cfg.AI.Groq.Model,
		GroqVisionModel:           cfg.AI.Groq.VisionModel,
		GroqThinkingEnabled:       cfg.AI.Groq.ThinkingEnabled,
		OllamaEnabled:             cfg.AI.Ollama.Enabled,
		OllamaURL:                 cfg.AI.Ollama.URL,
		OllamaModel:               cfg.AI.Ollama.Model,
		OllamaVisionModel:         cfg.AI.Ollama.VisionModel,
		OllamaThinkingEnabled:     cfg.AI.Ollama.ThinkingEnabled,
		OpencodeEnabled:           cfg.AI.Opencode.Enabled,
		OpencodeBaseURL:           cfg.AI.Opencode.BaseURL,
		OpencodeAPIKey:            cfg.AI.Opencode.APIKey,
		OpencodeModel:             cfg.AI.Opencode.Model,
		OpencodeVisionModel:       cfg.AI.Opencode.VisionModel,
		OpencodeThinkingEnabled:   cfg.AI.Opencode.ThinkingEnabled,
		OpenrouterEnabled:         cfg.AI.Openrouter.Enabled,
		OpenrouterBaseURL:         cfg.AI.Openrouter.BaseURL,
		OpenrouterAPIKey:          cfg.AI.Openrouter.APIKey,
		OpenrouterModel:           cfg.AI.Openrouter.Model,
		OpenrouterVisionModel:     cfg.AI.Openrouter.VisionModel,
		OpenrouterThinkingEnabled: cfg.AI.Openrouter.ThinkingEnabled,
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
		AttachmentCache:   repo,
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
		zap.Bool("fallback_enabled", aiService.IsFallbackAvailable()),
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
			// Create AI service adapter for memory system
			memoryAIService := &aiServiceAdapter{service: aiService}

			// Create AI summarizer using the existing AI service
			summarizer := memory.NewSummarizer(memoryAIService, cfg.Memory.SummaryModel)

			// Configure memory service from central config
			memoryServiceConfig := memory.ServiceConfig{
				BufferThreshold:    cfg.Memory.BufferThreshold,
				SummaryInterval:    cfg.Memory.SummaryInterval,
				MaxContextMessages: cfg.Memory.MaxContextMessages,
				MaxSummaryContext:  cfg.Memory.MaxSummaryContext,
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
					zap.String("summary_model", cfg.Memory.SummaryModel),
					zap.Int("buffer_threshold", memoryServiceConfig.BufferThreshold),
					zap.Duration("summary_interval", memoryServiceConfig.SummaryInterval))
			}
		}
	} else {
		logger.Info("Memory service disabled by configuration")
	}

	// Initialize emoji manager for shortcode conversion
	emojiManager := emoji.NewManager(client, cfg.Emoji)
	logger.Info("Emoji manager initialized")

	messageHandler := handlers.NewMessageHandler(client, aiService, repo, memoryService, emojiManager, cfg.Bot.DefaultPrompt, cfg.Bot.TriggerWords)
	client.AddEventListeners(
		bot.NewListenerFunc(messageHandler.HandleMessageCreate),
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
		registry.HandleCommand(e)
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

	sched := scheduler.New(client, aiService, repo, emojiManager, cfg.Horoscope, memoryService, cfg.Reengage, cfg.Bot.DefaultPrompt)
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

	// Close voice orchestrator if initialized
	if voiceOrchestrator != nil {
		if err := voiceOrchestrator.Close(); err != nil {
			logger.Error("Error closing voice orchestrator", zap.Error(err))
		}
	}
}

// aiServiceAdapter adapts the existing ai.Service to memory.AIService interface
type aiServiceAdapter struct {
	service *ai.Service
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
