package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/commands"
	"polynux/disgoroq/database"
	"polynux/disgoroq/emoji"
	"polynux/disgoroq/handlers"
	"polynux/disgoroq/logger"
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

	repo := database.NewRepository()

	emojiManager := emoji.NewManager(dg)

	messageHandler := handlers.NewMessageHandler(dg, aiService, repo, emojiManager)
	dg.AddHandler(messageHandler.Handle)
	dg.AddHandler(handlers.HandleGuildCreate)
	dg.AddHandler(handlers.HandleGuildDelete)

	registry := commands.NewRegistry(dg, local)
	commands.RegisterAll(registry, repo)
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
