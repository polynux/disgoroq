package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/commands"
	"polynux/disgoroq/database"
	"polynux/disgoroq/emoji"
	"polynux/disgoroq/handlers"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
	"polynux/disgoroq/scheduler"
	"polynux/disgoroq/utils"
)

var (
	Token   string
	GroqKey string

	local                   bool
	sendDirectHoroscope     bool
	sendDirectFartingFriday bool
	clearCommands           bool
)

func init() {
	err := godotenv.Load(".env.local")
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	Token = os.Getenv("DISCORD_TOKEN")
	GroqKey = os.Getenv("GROQ_API_KEY")

	if Token == "" {
		log.Fatal("No discord token found in .env file")
	}

	if GroqKey == "" {
		log.Fatal("No Groq key found in .env file")
	}

	flag.BoolVar(&local, "local", false, "Use local database")
	flag.BoolVar(&sendDirectHoroscope, "sendDirectHoroscope", false, "Send horoscope directly")
	flag.BoolVar(&sendDirectFartingFriday, "sendFartingFriday", false, "Send farting friday directly")
	flag.BoolVar(&clearCommands, "clearCommands", false, "Clear all registered commands")
	flag.Parse()
}

func main() {
	logger.Info("Starting DisgoroQ bot")

	dg, err := discordgo.New("Bot " + Token)
	if err != nil {
		logger.Fatal("Error creating Discord session", zap.Error(err))
		return
	}

	utils.InitializeDB(local)
	defer func() {
		logger.Info("Closing database")
		if err := utils.DB.Close(); err != nil {
			logger.Error("Error closing database", zap.Error(err))
		}
		defer logger.Sync()
	}()

	eventRepo := logger.NewEventRepository(utils.DB, logger.Log)
	logger.SetEventRepository(eventRepo)

	// Load AI service configuration with retry and fallback support
	aiConfig := ai.LoadServiceConfig()
	if err := aiConfig.Validate(); err != nil {
		logger.Fatal("Invalid AI service configuration", zap.Error(err))
		return
	}

	// Create AI service with retry and fallback capabilities
	aiService := ai.NewService(aiConfig)
	if aiService == nil {
		logger.Fatal("Failed to initialize AI service")
		return
	}

	// Log AI service configuration
	logger.Info("AI service initialized",
		zap.String("primary_provider", "groq"),
		zap.Bool("fallback_enabled", aiService.IsFallbackAvailable()),
		zap.Int("max_retries", aiConfig.RetryConfig.MaxRetries),
		zap.Duration("retry_delay", aiConfig.RetryConfig.InitialDelay))

	// Initialize memory service for conversation context
	var memoryService memory.Service
	if os.Getenv("MEMORY_ENABLED") != "false" {
		// Create memory repository using the existing database connection
		memoryRepo := memory.NewRepository(utils.DB)

		// Create Ollama embedding provider for vector search
		ollamaURL := os.Getenv("MEMORY_OLLAMA_URL")
		if ollamaURL == "" {
			ollamaURL = os.Getenv("OLLAMA_API_URL")
			if ollamaURL == "" {
				ollamaURL = "http://localhost:11434"
			}
		}
		embeddingModel := os.Getenv("MEMORY_EMBEDDING_MODEL")
		if embeddingModel == "" {
			embeddingModel = "nomic-embed-text"
		}
		embeddingProvider, err := memory.NewOllamaEmbeddingProviderWithConfig(ollamaURL, embeddingModel)
		if err != nil {
			logger.Warn("Failed to create embedding provider, continuing without memory",
				zap.Error(err),
				zap.String("ollama_url", ollamaURL))
			memoryService = nil
		}

		// Create AI service adapter for memory system
		memoryAIService := &aiServiceAdapter{service: aiService}

		// Create AI summarizer using the existing AI service
		summaryModel := os.Getenv("MEMORY_SUMMARY_MODEL")
		if summaryModel == "" {
			summaryModel = "llama3-8b-8192" // Default model for summarization
		}
		summarizer := memory.NewSummarizer(memoryAIService, summaryModel)

		// Configure memory service
		memoryConfig := memory.ServiceConfig{
			BufferThreshold:    10,            // Summarize after 10 messages
			SummaryInterval:    1 * time.Hour, // Minimum 1 hour between summaries
			MaxContextMessages: 5,             // Include last 5 messages in context
			MaxSummaryContext:  3,             // Include top 3 relevant summaries
		}

		// Override config from environment if provided
		if threshold := os.Getenv("MEMORY_BUFFER_THRESHOLD"); threshold != "" {
			if val, err := strconv.Atoi(threshold); err == nil && val > 0 {
				memoryConfig.BufferThreshold = val
			}
		}
		if interval := os.Getenv("MEMORY_SUMMARY_INTERVAL"); interval != "" {
			if val, err := strconv.Atoi(interval); err == nil && val > 0 {
				memoryConfig.SummaryInterval = time.Duration(val) * time.Second
			}
		}
		if maxMsgs := os.Getenv("MEMORY_MAX_CONTEXT_MESSAGES"); maxMsgs != "" {
			if val, err := strconv.Atoi(maxMsgs); err == nil && val > 0 {
				memoryConfig.MaxContextMessages = val
			}
		}
		if maxSummaries := os.Getenv("MEMORY_MAX_SUMMARY_CONTEXT"); maxSummaries != "" {
			if val, err := strconv.Atoi(maxSummaries); err == nil && val > 0 {
				memoryConfig.MaxSummaryContext = val
			}
		}

		memoryService = memory.NewService(memoryRepo, embeddingProvider, summarizer, memoryConfig)

		// Test memory service health
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := embeddingProvider.HealthCheck(ctx); err != nil {
			logger.Warn("Memory service embedding provider unavailable, continuing without memory",
				zap.Error(err),
				zap.String("ollama_url", ollamaURL),
				zap.String("embedding_model", embeddingModel))
			memoryService = nil
		} else {
			logger.Info("Memory service initialized",
				zap.String("ollama_url", ollamaURL),
				zap.String("embedding_model", embeddingModel),
				zap.String("summary_model", summaryModel),
				zap.Int("buffer_threshold", memoryConfig.BufferThreshold),
				zap.Duration("summary_interval", memoryConfig.SummaryInterval))
		}
	} else {
		logger.Info("Memory service disabled by configuration")
	}

	repo := database.NewRepository()

	// Initialize emoji manager for shortcode conversion
	emojiManager := emoji.NewManager(dg)
	logger.Info("Emoji manager initialized")

	messageHandler := handlers.NewMessageHandler(dg, aiService, repo, memoryService, emojiManager)
	dg.AddHandler(messageHandler.Handle)
	dg.AddHandler(handlers.HandleGuildCreate)
	dg.AddHandler(handlers.HandleGuildDelete)

	registry := commands.NewRegistry(dg, local)
	commands.RegisterAll(registry, repo, memoryService)
	dg.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		registry.HandleCommand(i)
	})

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsGuilds

	err = dg.Open()
	if err != nil {
		logger.Fatal("Error opening discord connection", zap.Error(err))
		return
	}
	defer dg.Close()

	logger.Info("Bot is now running. Press CTRL-C to exit.")

	if clearCommands {
		clearAllCommands(dg)
		return
	}

	err = registry.Register()
	if err != nil {
		logger.Fatal("Error registering commands", zap.Error(err))
	}
	logger.Info("Commands registered successfully")

	sched := scheduler.New(dg, aiService, repo, emojiManager)
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
		Model:    model,
		Messages: aiMessages,
	}

	response, err := a.service.Chat(ctx, request)
	if err != nil {
		return "", err
	}

	return response.Content, nil
}

func clearAllCommands(dg *discordgo.Session) {
	logger.Info("Clearing all commands")

	guilds := dg.State.Guilds

	totalDeleted := 0

	logger.Info("Clearing global commands")
	existing, err := dg.ApplicationCommands(dg.State.User.ID, "")
	if err != nil {
		logger.Error("Error fetching global commands", zap.Error(err))
	} else {
		for _, cmd := range existing {
			err := dg.ApplicationCommandDelete(dg.State.User.ID, "", cmd.ID)
			if err != nil {
				logger.Error("Error deleting global command",
					zap.Error(err),
					zap.String("command", cmd.Name),
				)
			} else {
				logger.Debug("Deleted global command", zap.String("command", cmd.Name))
				totalDeleted++
			}
		}
	}

	for _, guild := range guilds {
		logger.Debug("Clearing commands for guild", zap.String("guild", guild.Name))
		existing, err := dg.ApplicationCommands(dg.State.User.ID, guild.ID)
		if err != nil {
			logger.Error("Error fetching commands for guild",
				zap.Error(err),
				zap.String("guild", guild.Name),
			)
			continue
		}
		for _, cmd := range existing {
			err := dg.ApplicationCommandDelete(dg.State.User.ID, guild.ID, cmd.ID)
			if err != nil {
				logger.Error("Error deleting guild command",
					zap.Error(err),
					zap.String("command", cmd.Name),
					zap.String("guild", guild.Name),
				)
			} else {
				logger.Debug("Deleted guild command",
					zap.String("command", cmd.Name),
					zap.String("guild", guild.Name),
				)
				totalDeleted++
			}
		}
	}

	logger.Info("Commands cleared", zap.Int("total_deleted", totalDeleted))
}
