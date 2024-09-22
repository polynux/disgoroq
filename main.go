package main

import (
	"fmt"
    "context"
	"github.com/bwmarrin/discordgo"
    "os"
    "log"
	"os/signal"
	"syscall"
    "math/rand"
    "github.com/conneroisu/groq-go"
    "github.com/joho/godotenv"
)

var (
    Token string
    GroqKey string
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

    dg.Identify.Intents = discordgo.IntentsGuildMessages

	err = dg.Open()
	if err != nil {
        log.Fatal("Error opening connection,", err)
		return
	}

    log.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	dg.Close()
}

func getMessages(s *discordgo.Session, channelID string, num int) ([]*discordgo.Message, error) {
    if (num <= 100) {
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

    rand := rand.Intn(100)

    if rand > 10 && !botMentioned(s, m){
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
        messagesFormatted = messagesFormatted + "<" + messages[i].Author.ID + "> " + messages[i].Content + "\n"
    }

    question := "Tu fais partie d'une conversation. Tu n'as pas conscience d'etre un bot. Reponds de maniere goofy. Ta reponse doit etre tres courte, une phrase ou deux.\n"
    question += "<messages>\n" + messagesFormatted + "\n</messages>"
    s.ChannelMessageSend(m.ChannelID, "Thinking...")
    response, err := askGroq(context.Background(), question)
    if err != nil {
        s.ChannelMessageSend(m.ChannelID, "There was an error getting the response.")
        return
    }
    s.ChannelMessageSend(m.ChannelID, response)
}

func askGroq(ctx context.Context, message string) (string,error) {
    client, err := groq.NewClient(GroqKey)
    if err != nil {
        fmt.Println("error creating Groq client,", err)
        return "", err
    }

    resp, err := client.CreateChatCompletion(ctx, groq.ChatCompletionRequest{
        Model: groq.Llama318BInstant,
        Messages: []groq.ChatCompletionMessage{
            {
                Role: groq.ChatMessageRoleUser,
                Content: message,
            },
        },
        MaxTokens: 100,
    })
    if err != nil {
        fmt.Println("error creating Groq completion,", err)
        return "", err
    }


    return string(resp.Choices[0].Message.Content), nil
}
