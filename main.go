package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/conneroisu/groq-go"
	"github.com/go-co-op/gocron/v2"
	"github.com/joho/godotenv"
	"github.com/ollama/ollama/api"

	"polynux/disgoroq/db"
	"polynux/disgoroq/horoscope"
	"polynux/disgoroq/utils"
)

var (
	Token                string
	GroqKey              string
	defaultThreshold             = 0.1
	defaultThresholdSexe         = 0.05
	defaultMaxTokens             = 100
	defaultTemperature   float32 = 0.5
	defaultMessagesCount         = 100
	rateLimit            int64   = 10

	defaultMemberPermissions int64 = discordgo.PermissionManageMessages

	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "ping",
			Description: "Replies with Pong!",
		},
		{
			Name:        "horoscope",
			Description: "Get the horoscope for a sign",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "sign",
					Description: "The sign for the horoscope (belier, taureau, etc...)",
					Required:    true,
				},
			},
		},
		{
			Name:        "horoscopechannel",
			Description: "Set the channel for the horoscope",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel for the horoscope",
					Required:    true,
				},
			},
		},
		{
			Name:        "temperature",
			Description: "Set the temperature for the bot",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "temperature",
					Description: "The temperature for the bot (0.0-1.0)",
					Required:    true,
				},
			},
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		{
			Name:                     "toggle",
			Description:              "Toggle the bot on or off",
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		{
			Name:        "threshold",
			Description: "Set the threshold for the bot (activation probability; 0.0-1.0)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "threshold",
					Description: "The threshold activation (0.0-1.0)",
					Required:    true,
				},
			},
		},
		{
			Name:        "thresholdsexe",
			Description: "Set the threshold for the bot to say sexe (activation probability; 0.0-1.0)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "thresholdsexe",
					Description: "The thresholdsexe activation (0.0-1.0)",
					Required:    true,
				},
			},
		},
		{
			Name:        "messagescount",
			Description: "Set the number of messages to consider for the bot",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "messagescount",
					Description: "The number of messages to consider for the bot (1-100)",
					Required:    true,
				},
			},
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		{
			Name:                     "clean",
			Description:              "Clean the bot's messages",
			DefaultMemberPermissions: &defaultMemberPermissions,
		},
		{
			Name:        "prompt",
			Description: "Set the prompt for the bot",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "set",
					Description: "Set a custom prompt for the bot",
					Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "custom",
							Description: "Set a custom prompt for the bot",
							Type:        discordgo.ApplicationCommandOptionSubCommand,
							Options: []*discordgo.ApplicationCommandOption{
								{
									Name:        "prompt",
									Description: "The custom prompt for the bot",
									Type:        discordgo.ApplicationCommandOptionString,
									Required:    true,
									MaxLength:   1000,
								},
							},
						},
						{
							Name:        "default",
							Description: "Put back the default prompt",
							Type:        discordgo.ApplicationCommandOptionSubCommand,
						},
					},
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ping": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Pong!",
				},
			})
		},
		"horoscope": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			sign := i.ApplicationCommandData().Options[0].StringValue()
			horo, err := horoscope.GetHoroscope(sign)
			if err != nil {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "Error getting horoscope",
					},
				})
				return
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: horo,
				},
			})
		},
		"horoscopechannel": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			channelID := i.ApplicationCommandData().Options[0].ChannelValue(s)
			err := utils.Q.SetGuildSetting(context.Background(), db.SetGuildSettingParams{
				GuildID: i.GuildID,
				Name:    "horoscope_channel",
				Value:   channelID.ID,
			})
			content := fmt.Sprintf("Horoscope channel set to %v", channelID.ID)
			if err != nil {
				content = "Error setting horoscope channel"
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
		},
		"temperature": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			temperature := i.ApplicationCommandData().Options[0].FloatValue()
			err := utils.Q.SetGuildSetting(context.Background(), db.SetGuildSettingParams{
				GuildID: i.GuildID,
				Name:    "temperature",
				Value:   strconv.FormatFloat(temperature, 'f', -1, 32),
			})
			content := fmt.Sprintf("Temperature set to %v", temperature)
			if err != nil {
				content = "Error setting temperature"
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
		},
		"threshold": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			threshold := i.ApplicationCommandData().Options[0].FloatValue()
			err := utils.Q.SetGuildSetting(context.Background(), db.SetGuildSettingParams{
				GuildID: i.GuildID,
				Name:    "threshold",
				Value:   strconv.FormatFloat(threshold, 'f', -1, 32),
			})
			content := fmt.Sprintf("Threshold set to %v", threshold)
			if err != nil {
				content = "Error setting threshold"
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
		},
		"thresholdsexe": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			thresholdSexe := i.ApplicationCommandData().Options[0].FloatValue()
			err := utils.Q.SetGuildSetting(context.Background(), db.SetGuildSettingParams{
				GuildID: i.GuildID,
				Name:    "thresholdSexe",
				Value:   strconv.FormatFloat(thresholdSexe, 'f', -1, 32),
			})
			content := fmt.Sprintf("Threshold set to %v", thresholdSexe)
			if err != nil {
				content = "Error setting sexe threshold"
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
		},
		"toggle": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			current, _ := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
				Name:    "state",
				GuildID: i.GuildID,
			})
			newState := "off"
			if current == "off" {
				newState = "on"
			}
			err := utils.Q.SetGuildSetting(context.Background(), db.SetGuildSettingParams{
				GuildID: i.GuildID,
				Name:    "state",
				Value:   newState,
			})
			content := "Bot is now " + newState
			if err != nil {
				content = "Error toggling bot"
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
		},
		"clean": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			messages, err := s.ChannelMessages(i.ChannelID, 100, "", "", "")
			if err != nil {
				fmt.Println("error getting messages,", err)
				return
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Cleaning messages...",
				},
			})
			messagesToDelete := make([]string, 0)
			for idx := range messages {
				if messages[idx].Author.ID == s.State.User.ID {
					messagesToDelete = append(messagesToDelete, messages[idx].ID)
				}
			}
			s.ChannelMessagesBulkDelete(i.ChannelID, messagesToDelete)
			str := fmt.Sprintf("Messages cleaned")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &str,
			})
			time.AfterFunc(10*time.Second, func() {
				s.InteractionResponseDelete(i.Interaction)
			})
		},
		"messagescount": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			messagesCount := i.ApplicationCommandData().Options[0].IntValue()
			err := utils.Q.SetGuildSetting(context.Background(), db.SetGuildSettingParams{
				GuildID: i.GuildID,
				Name:    "messagescount",
				Value:   strconv.FormatInt(messagesCount, 10),
			})
			content := fmt.Sprintf("Messages count set to %v", messagesCount)
			if err != nil {
				content = "Error setting messages count"
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
		},
		"prompt": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			options := i.ApplicationCommandData().Options
			if options[0].Name != "set" {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "Wrong option!",
					},
				})
				return
			}

			options = options[0].Options
			if options[0].Name == "default" {
				content := "Prompt set to default"
				err := utils.Q.DeleteGuildSetting(context.Background(), db.DeleteGuildSettingParams{
					GuildID: i.GuildID,
					Name:    "prompt",
				})
				if err != nil {
					content = "Error setting prompt"
				}
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: content,
					},
				})
				return
			}

			if options[0].Name != "custom" {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "Wrong option!",
					},
				})
				return
			}
			value := options[0].Options[0].StringValue()
			err := utils.Q.SetGuildSetting(context.Background(), db.SetGuildSettingParams{
				GuildID: i.GuildID,
				Name:    "prompt",
				Value:   value,
			})
			content := fmt.Sprintf("Prompt correctly set")
			if err != nil {
				content = "Error setting prompt"
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: content,
				},
			})
		},
	}
)

var local bool

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

	dg.AddHandler(messageCreate)
	dg.AddHandler(joiningGuild)
	dg.AddHandler(leavingGuild)

	dg.AddHandler(userCommand)

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsGuilds

	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening discord connection,", err)
		return
	}
	defer dg.Close()

	log.Println("Bot is now running.  Press CTRL-C to exit.")

	registerCommands(dg)
	log.Println("Commands registered")

	scheduler := scheduleHoroscope(dg)
	defer scheduler.Shutdown()

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}

func scheduleHoroscope(s *discordgo.Session) gocron.Scheduler {
	location, _ := time.LoadLocation("Europe/Paris")
	logger := gocron.NewLogger(gocron.LogLevelInfo)
	scheduler, schedulerErr := gocron.NewScheduler(gocron.WithLocation(location), gocron.WithLogger(logger))
	if schedulerErr != nil {
		log.Println("error creating scheduler,", schedulerErr)
	}

	_, jobErr := scheduler.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(12, 0, 0))),
		gocron.NewTask(
			sendHoroscope,
			s,
		),
	)
	if jobErr != nil {
		log.Println("error creating job,", jobErr)
	}

	scheduler.Start()

	return scheduler
}

func sendHoroscope(s *discordgo.Session) {
	horoscopes, _ := horoscope.GetHoroscopes()
	horoscopeMessage := ""
	for key, value := range horoscopes {
		horoscopeMessage += fmt.Sprintf("%s\n%s\n\n", key, value)
	}
	instructions := `Tu es un createur d'horoscope. Tous les messages que tu recevras sont des horoscopes a modifier.
    Reponds de maniere GOOFY, c'est tres important. Ta reponse doit etre tres courte, deux phrases ou trois pour chaque horoscope.
    Rajoute de temps en temps des émojis goofy.
    Le signe astro doit etre en gras sous cette forme "**SIGNE**"`

	params := GroqParams{
		MaxTokens:    2000,
		Temperature:  1,
		Instructions: instructions,
		Content:      horoscopeMessage,
	}

	response, err := askGroq(context.Background(), &params)
	if err != nil {
		fmt.Println("error getting response,", err)
		return
	}
	responses := make([]string, 0)
	if len(response) > 2000 {
		for i := 0; i < len(response); i += 2000 {
			responses = append(responses, response[i:min(i+2000, len(response))])
		}
	} else {
		responses = append(responses, response)
	}

	guilds, err := utils.Q.GetAllGuilds(context.Background())
	if err != nil {
		fmt.Println("error getting guilds,", err)
		return
	}
	for _, guild := range guilds {
		channelID, err := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
			Name:    "horoscope_channel",
			GuildID: guild,
		})
		if err != nil {
			log.Println("error getting horoscope channel,", err)
			continue
		}
		_, err = s.ChannelMessageSend(channelID, "Horoscope du jour:")
		if err != nil {
			fmt.Println("error sending horoscope,", err)
			continue
		}
		for _, value := range responses {
			_, err = s.ChannelMessageSend(channelID, value)
			if err != nil {
				fmt.Println("error sending horoscope,", err)
				continue
			}
		}
	}
}

func registerCommands(s *discordgo.Session) {
	_, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", commands)
	if err != nil {
		log.Panicf("Cannot create commands: %v", err)
	}
}

func userCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	handler, ok := commandHandlers[i.ApplicationCommandData().Name]
	if !ok {
		return
	}
	handler(s, i)
}

func joiningGuild(s *discordgo.Session, m *discordgo.GuildCreate) {
	registeredCommands := make([]*discordgo.ApplicationCommand, len(commands))
	for i, v := range commands {
		cmd, err := s.ApplicationCommandCreate(s.State.User.ID, "", v)
		if err != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
		registeredCommands[i] = cmd
	}
}

func leavingGuild(s *discordgo.Session, m *discordgo.GuildDelete) {
	for _, v := range commands {
		err := s.ApplicationCommandDelete(s.State.User.ID, m.ID, v.ID)
		if err != nil {
			log.Panicf("Cannot delete '%v' command: %v", v.Name, err)
		}
	}
}

func getMessages(s *discordgo.Session, channelID string, num int) ([]*discordgo.Message, error) {
	if num <= 100 {
		messages, err := s.ChannelMessages(channelID, num, "", "", "")
		if err != nil {
			log.Println("error getting messages,", err)
			return nil, err
		}
		return messages, nil
	}

	messages := []*discordgo.Message{}
	for num > 0 {
		var toGet int
		if num > 100 {
			toGet = 100
		} else {
			toGet = num
		}
		lastMessage := ""
		if len(messages) > 0 {
			lastMessage = messages[len(messages)-1].ID
		}
		newMessages, err := s.ChannelMessages(channelID, toGet, lastMessage, "", "")
		if err != nil {
			fmt.Println("error getting messages,", err)
			return nil, err
		}
		messages = append(messages, newMessages...)
		num -= toGet
	}
	return messages, nil
}

func botMentioned(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	for i := range m.Mentions {
		if m.Mentions[i].ID == s.State.User.ID {
			return true
		}
	}
	return false
}

type imageToProcess struct {
	id  string
	url string
}

func getImagesToProcess(messages []*discordgo.Message) []imageToProcess {
	attachmentCount := 0
	imagesToProcess := make([]imageToProcess, 0)
	for idx := len(messages) - 1; idx >= 0; idx-- {
		for _, attachment := range messages[idx].Attachments {
			if attachmentCount > 5 {
				return imagesToProcess
			}
			if attachment.ContentType == "image/jpeg" || attachment.ContentType == "image/png" {
				if attachment.Size > 10000000 {
					continue
				}
				imagesToProcess = append(imagesToProcess, imageToProcess{
					id:  messages[idx].ID,
					url: attachment.URL,
				})
				attachmentCount++
			}
		}
	}

	return imagesToProcess
}

type processedImage struct {
	id          string
	description string
}

func processImageInMessages(s *discordgo.Session, m *discordgo.MessageCreate, messages []*discordgo.Message) map[string]string {
	imagesToProcess := getImagesToProcess(messages)

	describedImages := make(map[string]string)

	ch := make(chan processedImage, len(imagesToProcess))

	for _, img := range imagesToProcess {
		go func(img imageToProcess) {
			response, err := describeImage(context.Background(), &GroqImageParams{
				Instruction: "Décris cette image en 3-4 phrases ultra-courtes (max 5 mots chacune) qui capturent l'essentiel de la scène. UNIQUEMENT LES PHRASES. UNE PAR LIGNE.",
				ImageURL:    img.url,
			})
			if err != nil {
				fmt.Println("error getting response,", err)
				fmt.Println("Image URL:", img.url)
				ch <- processedImage{
					id:          img.id,
					description: "",
				}
				return
			}
			ch <- processedImage{
				id:          img.id,
				description: response,
			}
		}(img)
	}
	s.ChannelTyping(m.ChannelID)

	for range imagesToProcess {
		img := <-ch
		describedImages[img.id] = img.description
	}

	return describedImages
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	thresholdDb, err := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
		Name:    "threshold",
		GuildID: m.GuildID,
	})
	threshold := defaultThreshold
	if err == nil {
		value, err := strconv.ParseFloat(thresholdDb, 64)
		if err == nil {
			threshold = float64(value)
		}
	}

	thresholdSexeDb, err := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
		Name:    "thresholdSexe",
		GuildID: m.GuildID,
	})
	thresholdSexe := defaultThresholdSexe
	if err == nil {
		value, err := strconv.ParseFloat(thresholdSexeDb, 64)
		if err == nil {
			thresholdSexe = float64(value)
		}
	}

	randFloat := rand.Float32()
	if randFloat < float32(thresholdSexe) && !botMentioned(s, m) {
		s.ChannelMessageSend(m.ChannelID, "(et je parle de sexe evidemment)")
		return
	}

	randFloat = rand.Float32()
	if randFloat > float32(threshold) && !botMentioned(s, m) {
		return
	}

	state, err := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
		Name:    "state",
		GuildID: m.GuildID,
	})
	if state == "off" {
		return
	}

	lastMessage, _ := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
		Name:    "last_message",
		GuildID: m.GuildID,
	})
	var lastMessageTime int64 = 0
	if lastMessage != "" {
		lastMessageTime, _ = strconv.ParseInt(lastMessage, 10, 64)
	}

	if lastMessageTime > 0 && !botMentioned(s, m) {
		if time.Now().Unix()-lastMessageTime < rateLimit {
			s.ChannelMessageSend(m.ChannelID, "Please wait a bit before asking me again.")
			return
		}
	}

	err = utils.Q.SetGuildSetting(context.Background(), db.SetGuildSettingParams{
		GuildID: m.GuildID,
		Name:    "last_message",
		Value:   strconv.FormatInt(time.Now().Unix(), 10),
	})
	if err != nil {
		fmt.Println("error setting last message time,", err)
		return
	}

	s.ChannelTyping(m.ChannelID)

	messageCountDb, err := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
		Name:    "messagescount",
		GuildID: m.GuildID,
	})
	messageCount := defaultMessagesCount
	if err == nil {
		value, err := strconv.ParseInt(messageCountDb, 10, 64)
		if err == nil {
			messageCount = int(value)
		}
	}
	messages, err := getMessages(s, m.ChannelID, messageCount)
	if err != nil {
		fmt.Println("error getting messages,", err)
		return
	}

	processedImages := processImageInMessages(s, m, messages)

	messagesFormatted := ""
	memberCache := make(map[string]*discordgo.Member)
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if strings.Contains(messages[idx].Content, "Horoscope du jour:") && messages[idx].Author.ID == s.State.User.ID {
			idx--
			continue
		}
		imageDescription := ""
		_, found := processedImages[messages[idx].ID]
		if found {
			imageDescription = processedImages[messages[idx].ID]
		}
		var userMember *discordgo.Member
		var err error
		if cachedMember, exists := memberCache[m.Author.ID]; exists {
			userMember = cachedMember
		} else {
			userMember, err = s.GuildMember(m.GuildID, m.Author.ID)
			if err != nil {
				fmt.Println("error getting user member,", err)
				return
			}
			memberCache[m.Author.ID] = userMember
		}

		nick := userMember.Nick
		if nick == "" {
			nick = m.Author.Username
		}

		var sb strings.Builder
		sb.WriteString("<@")
		sb.WriteString(messages[idx].Author.ID)
		sb.WriteString(">")
		sb.WriteString(userMember.Nick)
		sb.WriteString(": ")

		if imageDescription != "" {
			sb.WriteString("<IMAGE_DESC>\n")
			sb.WriteString(strings.ReplaceAll(imageDescription, "\n", ""))
			sb.WriteString("</IMAGE_DESC>\n")
		}
		sb.WriteString(messages[idx].Content)
		sb.WriteString("\n\n")

		messagesFormatted += sb.String()
	}

	params := OllamaParams{
		MaxTokens:   defaultMaxTokens,
		Temperature: defaultTemperature,
	}

	temp, err := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
		Name:    "temperature",
		GuildID: m.GuildID,
	})
	if err == nil {
		value, err := strconv.ParseFloat(temp, 32)
		if err == nil {
			params.Temperature = float32(value)
		}
	}

	instructions := `Tu es %s, un pote complètement déjanté qui balance des vannes à tout-va. 
Tu ne dois JAMAIS :
- Mentionner que tu es une IA ou un assistant
- Sortir de ton personnage de pote déjanté
- Parler de manière formelle ou donner des suggestions
- Commencer tes phrases par "En tant que..."

Tu DOIS TOUJOURS :
- Parler comme un vrai pote qui déconne
- Utiliser des émojis et des références pop culture
- Répondre de manière ultra courte (2-3 phrases max)
- Rester dans le délire même sur des sujets sérieux
- Commencer direct par ta réponse sans introduction

Tu es un VRAI POTE FOU, pas un assistant - comporte-toi comme tel ! 🤪🚀🔥`
	botMember, err := s.GuildMember(m.GuildID, s.State.User.ID)
	if err != nil {
		fmt.Println("error getting bot member,", err)
		return
	}
	instructions = fmt.Sprintf(instructions, botMember.Nick)

	prompt, err := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
		Name:    "prompt",
		GuildID: m.GuildID,
	})
	if err == nil {
		params.Instructions = prompt
	} else {
		params.Instructions = instructions
	}

	content := "<messages>\n" + messagesFormatted + "\n</messages>"
	content += "\n\n" + "Le dernier message était: \n<message>" + messages[0].Content + "</message>\n"

	s.ChannelTyping(m.ChannelID)

	params.Content = content
	response, err := askOllama(&params)

	reference := &discordgo.MessageReference{
		MessageID: m.ID,
		ChannelID: m.ChannelID,
		GuildID:   m.GuildID,
	}
	if err != nil {
		if botMentioned(s, m) {
			s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
				Content:   "There was an error getting the response.",
				Reference: reference,
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse: []discordgo.AllowedMentionType{},
				},
			})
		} else {
			s.ChannelMessageSend(m.ChannelID, "There was an error getting the response.")
		}
		return
	}
	s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   response,
		Reference: reference,
		AllowedMentions: &discordgo.MessageAllowedMentions{
			Parse: []discordgo.AllowedMentionType{},
		},
	})
}

type OllamaParams struct {
	MaxTokens    int
	Temperature  float32
	Instructions string
	Content      string
}

func askOllama(params *OllamaParams) (string, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		log.Println(err)
		return "", err
	}

	req := &api.GenerateRequest{
		Model:  "dolphin3",
		System: params.Instructions,
		Prompt: params.Content,
		Stream: new(bool),
		Options: map[string]interface{}{
			"temperature":   params.Temperature,
			"num_predict":   params.MaxTokens,
			"repeat_last_n": -1,
			"top_k":         60,
		},
	}

	ctx := context.Background()
	response := ""
	respFunc := func(resp api.GenerateResponse) error {
		response = resp.Response
		return nil
	}

	err = client.Generate(ctx, req, respFunc)
	if err != nil {
		log.Println(err)
		return "", err
	}

	return response, nil
}

type GroqParams struct {
	MaxTokens    int
	Temperature  float32
	Instructions string
	Content      string
}

func askGroq(ctx context.Context, params *GroqParams) (string, error) {
	client, err := groq.NewClient(GroqKey)
	if err != nil {
		fmt.Println("error creating Groq client,", err)
		return "", err
	}

	resp, err := client.ChatCompletion(ctx, groq.ChatCompletionRequest{
		Model: groq.ModelLlama318BInstant,
		Messages: []groq.ChatCompletionMessage{
			{
				Role:    groq.RoleUser,
				Content: params.Instructions + "\n" + params.Content,
			},
		},
		MaxTokens:   params.MaxTokens,
		Temperature: params.Temperature,
	})
	if err != nil {
		fmt.Println("error creating Groq completion,", err)
		return "", err
	}

	return string(resp.Choices[0].Message.Content), nil
}

type GroqImageParams struct {
	Instruction string
	ImageURL    string
}

func describeImage(ctx context.Context, params *GroqImageParams) (string, error) {
	client, err := groq.NewClient(GroqKey)
	if err != nil {
		fmt.Println("error creating Groq client,", err)
		return "", err
	}

	resp, err := client.ChatCompletion(ctx, groq.ChatCompletionRequest{
		Model: groq.ModelLlama3211BVisionPreview,
		Messages: []groq.ChatCompletionMessage{
			{
				Role: groq.RoleUser,
				MultiContent: []groq.ChatMessagePart{
					{
						Type: groq.ChatMessagePartTypeText,
						Text: params.Instruction,
					},
					{
						Type: groq.ChatMessagePartTypeImageURL,
						ImageURL: &groq.ChatMessageImageURL{
							URL:    params.ImageURL,
							Detail: "auto",
						},
					},
				},
			},
		},
		MaxTokens:   100,
		Temperature: 0.2,
	})
	if err != nil {
		fmt.Println("error creating Groq completion,", err)
		return "", err
	}
	return string(resp.Choices[0].Message.Content), nil
}
