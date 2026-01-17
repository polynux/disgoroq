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
	logger.Log.Info("Starting DisgoroQ bot")

	dg, err := discordgo.New("Bot " + Token)
	if err != nil {
		logger.Log.Fatal("Error creating Discord session", zap.Error(err))
		return
	}

	utils.InitializeDB(local)
	defer func() {
		logger.Log.Info("Closing database")
		if err := utils.DB.Close(); err != nil {
			logger.Log.Error("Error closing database", zap.Error(err))
		}
		defer logger.Sync()
	}()

	groqProvider := ai.NewGroqProvider(GroqKey)
	repo := database.NewRepository()

	messageHandler := handlers.NewMessageHandler(dg, groqProvider, repo)
	dg.AddHandler(messageHandler.Handle)
	dg.AddHandler(handlers.HandleGuildCreate)
	dg.AddHandler(handlers.HandleGuildDelete)

	registry := commands.NewRegistry(dg, local)
	commands.RegisterAll(registry, repo)

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsGuilds

	err = dg.Open()
	if err != nil {
		logger.Log.Fatal("Error opening discord connection", zap.Error(err))
		return
	}
	defer dg.Close()

	logger.Log.Info("Bot is now running. Press CTRL-C to exit.")

	if clearCommands {
		clearAllCommands(dg)
		return
	}

	err = registry.Register()
	if err != nil {
		logger.Log.Fatal("Error registering commands", zap.Error(err))
	}
	logger.Log.Info("Commands registered successfully")

	sched := scheduler.New(dg, groqProvider, repo)
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

	logger.Log.Info("Shutting down gracefully")
}

func clearAllCommands(dg *discordgo.Session) {
	logger.Log.Info("Clearing all commands")

	guilds := dg.State.Guilds

	totalDeleted := 0

	logger.Log.Info("Clearing global commands")
	existing, err := dg.ApplicationCommands(dg.State.User.ID, "")
	if err != nil {
		logger.Log.Error("Error fetching global commands", zap.Error(err))
	} else {
		for _, cmd := range existing {
			err := dg.ApplicationCommandDelete(dg.State.User.ID, "", cmd.ID)
			if err != nil {
				logger.Log.Error("Error deleting global command",
					zap.Error(err),
					zap.String("command", cmd.Name),
				)
			} else {
				logger.Log.Debug("Deleted global command", zap.String("command", cmd.Name))
				totalDeleted++
			}
		}
	}

	for _, guild := range guilds {
		logger.Log.Debug("Clearing commands for guild", zap.String("guild", guild.Name))
		existing, err := dg.ApplicationCommands(dg.State.User.ID, guild.ID)
		if err != nil {
			logger.Log.Error("Error fetching commands for guild",
				zap.Error(err),
				zap.String("guild", guild.Name),
			)
			continue
		}
		for _, cmd := range existing {
			err := dg.ApplicationCommandDelete(dg.State.User.ID, guild.ID, cmd.ID)
			if err != nil {
				logger.Log.Error("Error deleting guild command",
					zap.Error(err),
					zap.String("command", cmd.Name),
					zap.String("guild", guild.Name),
				)
			} else {
				logger.Log.Debug("Deleted guild command",
					zap.String("command", cmd.Name),
					zap.String("guild", guild.Name),
				)
				totalDeleted++
			}
		}
	}

	logger.Log.Info("Commands cleared", zap.Int("total_deleted", totalDeleted))
}
