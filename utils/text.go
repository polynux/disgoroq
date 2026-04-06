package utils

import "strings"

var botTextReplacer = strings.NewReplacer("feur", "fleur")

// NormalizeBotText applies shared post-processing to bot-generated text before delivery.
func NormalizeBotText(text string) string {
	return botTextReplacer.Replace(text)
}
