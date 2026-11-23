package ai

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Media tag rendering, shared with the training data: see
// docs/06-media-tags.md in discord-ia-waldo. Models trained on v0 prompts know
// the bracketed placeholders; v1 prompts carry descriptive tags.
const (
	MediaTagsV0 = "v0"
	MediaTagsV1 = "v1"

	mediaDescriptionMaxRunes = 60
	mediaGifMaxWords         = 8
)

var mediaURLPattern = regexp.MustCompile(`(?i)(?:https?://|www\.)\S+`)

var mediaGifStopWords = map[string]bool{
	"gif": true, "gifs": true, "tenor": true, "giphy": true, "klipy": true,
	"attachment": true, "image": true, "img": true, "unknown": true,
	"video": true, "caption": true, "download": true, "media": true, "view": true,
}

var mediaFileExtensions = []string{".gif", ".png", ".jpg", ".jpeg", ".webp", ".mp4", ".webm"}

// RenderMediaMessage renders one message exactly as docs/06-media-tags.md
// defines it for the given version. Unknown versions fall back to v0.
func RenderMediaMessage(msg Message, version string) string {
	if mediaTagsVersion(version) == MediaTagsV1 {
		return renderMediaMessageV1(msg)
	}
	return renderMediaMessageV0(msg)
}

func mediaTagsVersion(version string) string {
	if strings.EqualFold(strings.TrimSpace(version), MediaTagsV1) {
		return MediaTagsV1
	}
	return MediaTagsV0
}

// renderMediaMessageV0 keeps the placeholders the live models were trained on:
// attachments and stickers are only rendered when the message has no text.
func renderMediaMessageV0(msg Message) string {
	parts := make([]string, 0, 1+len(msg.Attachments)+len(msg.Stickers))
	if text := mediaURLPattern.ReplaceAllStringFunc(msg.Content, mediaURLTagV0); text != "" {
		parts = append(parts, text)
	}
	if strings.TrimSpace(msg.Content) == "" {
		for _, attachment := range msg.Attachments {
			if attachmentIsGIF(attachment) {
				parts = append(parts, "[gif]")
			} else {
				parts = append(parts, "[image]")
			}
		}
		for range msg.Stickers {
			parts = append(parts, "[image]")
		}
	}
	return collapseMediaWhitespace(strings.Join(parts, " "))
}

func renderMediaMessageV1(msg Message) string {
	urls := mediaURLPattern.FindAllString(msg.Content, -1)
	nonGifURLs := 0
	for _, raw := range urls {
		if parsed := parseMediaURL(raw); parsed == nil || mediaURLKindV1(parsed) != "gif" {
			nonGifURLs++
		}
	}

	parts := make([]string, 0, 1+len(msg.Attachments)+len(msg.Stickers))
	text := mediaURLPattern.ReplaceAllStringFunc(msg.Content, func(raw string) string {
		return mediaURLTagV1(raw, msg.Embeds, nonGifURLs)
	})
	if text != "" {
		parts = append(parts, text)
	}
	for _, attachment := range msg.Attachments {
		parts = append(parts, mediaAttachmentTagV1(attachment))
	}
	for _, sticker := range msg.Stickers {
		name := collapseMediaWhitespace(sticker)
		if name == "" {
			parts = append(parts, "[sticker]")
			continue
		}
		parts = append(parts, "[sticker: "+name+"]")
	}
	return collapseMediaWhitespace(strings.Join(parts, " "))
}

func mediaURLTagV0(raw string) string {
	parsed := parseMediaURL(raw)
	return "[" + mediaURLKindV0(parsed) + "]"
}

func mediaURLTagV1(raw string, embeds []MessageEmbed, nonGifURLs int) string {
	parsed := parseMediaURL(raw)
	kind := mediaURLKindV1(parsed)
	if kind == "gif" {
		words := gifWordsFromSegment(lastURLPathSegment(parsed))
		if len(words) == 0 {
			return "[gif]"
		}
		return "[gif: " + strings.Join(words, " ") + "]"
	}
	if kind == "image" || kind == "lien" {
		return "[" + kind + "]"
	}

	description := ""
	if embed := mediaEmbedForURL(embeds, raw, nonGifURLs); embed != nil {
		description = embed.Title
		if description == "" {
			description = embed.Description
		}
		description = cleanMediaDescription(description)
	}
	if description == "" {
		return "[" + kind + "]"
	}
	return "[" + kind + ": " + description + "]"
}

func mediaEmbedForURL(embeds []MessageEmbed, raw string, nonGifURLs int) *MessageEmbed {
	for idx := range embeds {
		if embeds[idx].URL == raw {
			return &embeds[idx]
		}
	}
	if nonGifURLs == 1 && len(embeds) == 1 {
		return &embeds[0]
	}
	return nil
}

func mediaURLKindV0(parsed *url.URL) string {
	if parsed == nil {
		return "lien"
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case hostContainsAny(host, "tenor.com", "giphy.com", "klipy.com"):
		return "gif"
	case strings.HasSuffix(strings.ToLower(parsed.Path), ".gif"):
		return "gif"
	case hostContainsAny(host, "youtube.com") || host == "youtu.be":
		return "youtube"
	case hostContainsAny(host, "twitter.com", "twimg.com", "fxtwitter", "vxtwitter", "fixupx") || host == "x.com" || strings.HasSuffix(host, ".x.com"):
		return "tweet"
	case hostContainsAny(host, "tiktok.com", "tnktok.com"):
		return "tiktok"
	case hostContainsAny(host, "instagram.com"):
		return "insta"
	case hostContainsAny(host, "soundcloud.com", "spotify.com", "deezer.com"):
		return "son"
	case hostContainsAny(host, "steampowered.com", "steamcommunity.com"):
		return "steam"
	case isDiscordCDN(host):
		return "image"
	default:
		return "lien"
	}
}

func mediaURLKindV1(parsed *url.URL) string {
	if parsed == nil {
		return "lien"
	}
	host := strings.ToLower(parsed.Hostname())
	switch {
	case hostContainsAny(host, "tenor.com", "giphy.com", "klipy.com"):
		return "gif"
	case strings.HasSuffix(strings.ToLower(parsed.Path), ".gif"):
		return "gif"
	case isDiscordCDN(host) && segmentHasGifWord(lastURLPathSegment(parsed)):
		return "gif"
	case hostContainsAny(host, "youtube.com") || host == "youtu.be":
		return "youtube"
	case hostContainsAny(host, "twitter.com", "twimg.com", "fxtwitter", "vxtwitter", "fixupx") || host == "x.com" || strings.HasSuffix(host, ".x.com"):
		return "tweet"
	case hostContainsAny(host, "tiktok.com", "tnktok.com"):
		return "tiktok"
	case hostContainsAny(host, "instagram.com"):
		return "insta"
	case hostContainsAny(host, "soundcloud.com", "spotify.com", "deezer.com"):
		return "son"
	case hostContainsAny(host, "steampowered.com", "steamcommunity.com"):
		return "steam"
	case isDiscordCDN(host):
		return "image"
	default:
		return "lien"
	}
}

func hostContainsAny(host string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(host, needle) {
			return true
		}
	}
	return false
}

func isDiscordCDN(host string) bool {
	return hostContainsAny(host, "cdn.discordapp.com", "media.discordapp.net")
}

func parseMediaURL(raw string) *url.URL {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return nil
	}
	return parsed
}

// lastURLPathSegment returns the last non-empty, percent-decoded path segment.
func lastURLPathSegment(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	segments := strings.Split(parsed.EscapedPath(), "/")
	for idx := len(segments) - 1; idx >= 0; idx-- {
		if segments[idx] == "" {
			continue
		}
		if decoded, err := url.PathUnescape(segments[idx]); err == nil {
			return decoded
		}
		return segments[idx]
	}
	return ""
}

func segmentHasGifWord(segment string) bool {
	for _, word := range mediaSegmentWords(segment) {
		if word == "gif" {
			return true
		}
	}
	return false
}

// mediaSegmentWords lowercases the segment without its final extension and
// splits it on -, _, ., + and whitespace (gif word step 2).
func mediaSegmentWords(segment string) []string {
	trimmed := stripMediaExtension(strings.TrimSpace(segment))
	if trimmed == "" {
		return nil
	}
	return strings.FieldsFunc(strings.ToLower(trimmed), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '+' || unicode.IsSpace(r)
	})
}

// gifWordsFromSegment applies the gif word rules: drop generic words, digit-only
// words and long ids, then keep the first 8.
func gifWordsFromSegment(segment string) []string {
	words := make([]string, 0, mediaGifMaxWords)
	for _, word := range mediaSegmentWords(segment) {
		if mediaGifStopWords[word] || isDigitsOnly(word) {
			continue
		}
		if utf8.RuneCountInString(word) >= 8 && containsDigit(word) {
			continue
		}
		words = append(words, word)
		if len(words) == mediaGifMaxWords {
			break
		}
	}
	return words
}

func stripMediaExtension(value string) string {
	lowered := strings.ToLower(value)
	for _, extension := range mediaFileExtensions {
		if strings.HasSuffix(lowered, extension) {
			return value[:len(value)-len(extension)]
		}
	}
	return value
}

func isDigitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func containsDigit(value string) bool {
	for _, r := range value {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func attachmentIsGIF(attachment MessageAttachment) bool {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	return contentType == "image/gif" || strings.HasSuffix(strings.ToLower(attachment.Filename), ".gif")
}

func mediaAttachmentTagV1(attachment MessageAttachment) string {
	if attachmentIsGIF(attachment) {
		words := gifWordsFromSegment(attachment.Filename)
		if len(words) == 0 {
			return "[gif]"
		}
		return "[gif: " + strings.Join(words, " ") + "]"
	}

	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "[image]"
	case strings.HasPrefix(contentType, "video/"):
		return "[video]"
	case strings.HasPrefix(contentType, "audio/"):
		return "[son]"
	default:
		return "[fichier]"
	}
}

// cleanMediaDescription collapses whitespace, removes brackets, and cuts the
// description to 60 code points at its last space.
func cleanMediaDescription(value string) string {
	cleaned := strings.Join(strings.Fields(value), " ")
	cleaned = strings.ReplaceAll(cleaned, "[", "")
	cleaned = strings.ReplaceAll(cleaned, "]", "")
	cleaned = strings.TrimSpace(cleaned)

	runes := []rune(cleaned)
	if len(runes) <= mediaDescriptionMaxRunes {
		return cleaned
	}
	cut := string(runes[:mediaDescriptionMaxRunes])
	if idx := strings.LastIndex(cut, " "); idx >= 0 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut)
}

func collapseMediaWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
