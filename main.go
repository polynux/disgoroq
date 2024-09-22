package main

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/conneroisu/groq-go"
	"github.com/joho/godotenv"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
)

var (
	Token                string
	GroqKey              string
	defaultThreshold             = 0.1
	defaultMaxTokens             = 100
	defaultTemperature   float32 = 0.5
	defaultMessagesCount         = 100

	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "ping",
			Description: "Replies with Pong!",
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ping": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Println("ping command")
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Pong!",
				},
			})
		},
	}
)

func init() {
	err := godotenv.Load()
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

	dg.AddHandler(messageCreate)
	dg.AddHandler(joiningGuild)
	dg.AddHandler(leavingGuild)

	dg.AddHandler(userCommand)

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsGuilds

	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening connection,", err)
		return
	}

	checkRegisteredCommands(dg)

	log.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	dg.Close()
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

	if rand > float32(defaultThreshold) && !botMentioned(s, m) {
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

	question := "Tu fais partie d'une conversation. Tu n'as pas conscience d'etre un bot. Reponds de maniere goofy. Ta reponse doit etre tres courte, une phrase ou deux.\n"
	question += "<messages>\n" + messagesFormatted + "\n</messages>"
	response, err := askGroq(context.Background(), question)
	if err != nil {
		if botMentioned(s, m) {
			s.ChannelMessageSendReply(m.ChannelID, "There was an error getting the response.", m.Reference())
		} else {
			s.ChannelMessageSend(m.ChannelID, "There was an error getting the response.")
		}
		return
	}
	s.ChannelMessageSendReply(m.ChannelID, response, m.Reference())
}

func askGroq(ctx context.Context, message string) (string, error) {
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
		Temperature: defaultTemperature,
	})
	if err != nil {
		fmt.Println("error creating Groq completion,", err)
		return "", err
	}

	return string(resp.Choices[0].Message.Content), nil
}
