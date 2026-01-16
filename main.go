package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/commands"
	"polynux/disgoroq/database"
	"polynux/disgoroq/handlers"
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
	dg, err := discordgo.New("Bot " + Token)
	if err != nil {
		log.Fatal("Error creating Discord session,", err)
		return
	}

	utils.InitializeDB(local)
	defer func() {
		log.Println("closing db")
		if err := utils.DB.Close(); err != nil {
			log.Println("error closing db,", err)
		}
	}()

	groqProvider := ai.NewGroqProvider(GroqKey)
	repo := database.NewRepository()

	messageHandler := handlers.NewMessageHandler(dg, groqProvider, repo)
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
		log.Fatal("Error opening discord connection,", err)
		return
	}
	defer dg.Close()

	log.Println("Bot is now running. Press CTRL-C to exit.")

	if clearCommands {
		clearAllCommands(dg)
		return
	}

	err = registry.Register()
	if err != nil {
		log.Fatal("Error registering commands,", err)
	}
	log.Println("Commands registered")

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
}

func clearAllCommands(dg *discordgo.Session) {
	log.Println("Clearing all commands...")

	guilds := dg.State.Guilds

	totalDeleted := 0

	log.Println("Clearing global commands...")
	existing, err := dg.ApplicationCommands(dg.State.User.ID, "")
	if err != nil {
		log.Printf("Error fetching global commands: %v", err)
	} else {
		for _, cmd := range existing {
			err := dg.ApplicationCommandDelete(dg.State.User.ID, "", cmd.ID)
			if err != nil {
				log.Printf("Error deleting global command %s: %v", cmd.Name, err)
			} else {
				log.Printf("Deleted global command: %s", cmd.Name)
				totalDeleted++
			}
		}
	}

	for _, guild := range guilds {
		log.Printf("Clearing commands for guild: %s", guild.Name)
		existing, err := dg.ApplicationCommands(dg.State.User.ID, guild.ID)
		if err != nil {
			log.Printf("Error fetching commands for guild %s: %v", guild.Name, err)
			continue
		}
		for _, cmd := range existing {
			err := dg.ApplicationCommandDelete(dg.State.User.ID, guild.ID, cmd.ID)
			if err != nil {
				log.Printf("Error deleting guild command %s from %s: %v", cmd.Name, guild.Name, err)
			} else {
				log.Printf("Deleted guild command %s from %s", cmd.Name, guild.Name)
				totalDeleted++
			}
		}
	}

	log.Printf("Cleared %d total commands. Exiting.", totalDeleted)
}
