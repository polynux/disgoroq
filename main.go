package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/conneroisu/groq-go"
	"github.com/joho/godotenv"

	"polynux/disgoroq/db"
	"polynux/disgoroq/utils"
)

var (
	Token                string
	GroqKey              string
	defaultThreshold             = 0.1
	defaultMaxTokens             = 100
	defaultTemperature   float32 = 0.5
	defaultMessagesCount         = 100
	rateLimit            int64   = 10

	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "ping",
			Description: "Replies with Pong!",
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
		},
		{
			Name:        "toggle",
			Description: "Toggle the bot on or off",
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
			Name:        "maxtokens",
			Description: "Set the maximum number of tokens for the bot",
		},
		{
			Name:        "messagescount",
			Description: "Set the number of messages to consider for the bot",
		},
		{
			Name:        "clean",
			Description: "Clean the bot's messages",
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
	}
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
}

func main() {
	dg, err := discordgo.New("Bot " + Token)
	if err != nil {
		log.Fatal("Error creating Discord session,", err)
		return
	}

	utils.InitializeDB()
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

	checkRegisteredCommands(dg)

	log.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}

func checkRegisteredCommands(s *discordgo.Session) {
	for _, v := range s.State.Guilds {
		registerCommands(s, v.ID)
	}
}

func registerCommands(s *discordgo.Session, guildID string) {
	registeredCommands := make([]*discordgo.ApplicationCommand, len(commands))
	for i, v := range commands {
		cmd, err := s.ApplicationCommandCreate(s.State.User.ID, guildID, v)
		if err != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
		registeredCommands[i] = cmd
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
	registerCommands(s, m.ID)
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

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	rand := rand.Float32()
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

	if rand > float32(threshold) && !botMentioned(s, m) {
		return
	}

	state, err := utils.Q.GetGuildSetting(context.Background(), db.GetGuildSettingParams{
		Name:    "state",
		GuildID: m.GuildID,
	})
	if err != nil || state == "off" {
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

	messages, err := getMessages(s, m.ChannelID, 100)
	if err != nil {
		fmt.Println("error getting messages,", err)
		return
	}
	messagesFormatted := ""
	for i := range messages {
		if messages[i].Author.ID == s.State.User.ID {
			continue
		}
		messagesFormatted = messagesFormatted + "<@" + messages[i].Author.ID + "> " + messages[i].Content + "\n"
	}

	params := GroqParams{
		MaxTokens:     defaultMaxTokens,
		Temperature:   defaultTemperature,
		MessagesCount: defaultMessagesCount,
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

	question := "Tu fais partie d'une conversation. Tu n'as pas conscience d'etre un bot. Reponds de maniere goofy. Ta reponse doit etre tres courte, une phrase ou deux.\n"
	question += "<messages>\n" + messagesFormatted + "\n</messages>"
	response, err := askGroq(context.Background(), question, &params)

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

type GroqParams struct {
	MaxTokens     int
	Temperature   float32
	MessagesCount int
}

func askGroq(ctx context.Context, message string, params *GroqParams) (string, error) {
	client, err := groq.NewClient(GroqKey)
	if err != nil {
		fmt.Println("error creating Groq client,", err)
		return "", err
	}

	resp, err := client.CreateChatCompletion(ctx, groq.ChatCompletionRequest{
		Model: groq.Llama318BInstant,
		Messages: []groq.ChatCompletionMessage{
			{
				Role:    groq.ChatMessageRoleUser,
				Content: message,
			},
		},
		MaxTokens:   defaultMaxTokens,
		Temperature: params.Temperature,
	})
	if err != nil {
		fmt.Println("error creating Groq completion,", err)
		return "", err
	}

	return string(resp.Choices[0].Message.Content), nil
}
