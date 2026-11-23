package ai

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Media tag rendering, shared with the training data: see
// docs/06-media-tags.md (revision 2) in discord-ia-waldo. Models trained on v0
// prompts know the bracketed placeholders; v1 prompts carry descriptive tags.
const (
	MediaTagsV0 = "v0"
	MediaTagsV1 = "v1"

	mediaDescriptionMaxRunes = 60
	mediaGifMaxWords         = 8
)

var (
	mediaURLPattern = regexp.MustCompile(`(?i)(?:https?://|www\.)\S+`)

	// Legacy URL kind patterns, searched anywhere in the whole URL string.
	mediaGifKindPattern     = regexp.MustCompile(`(?i)tenor\.com|giphy\.com|klipy\.com|\.gif\b`)
	mediaYouTubeKindPattern = regexp.MustCompile(`(?i)youtube\.com|youtu\.be`)
	mediaTweetKindPattern   = regexp.MustCompile(`(?i)twitter\.com|//x\.com|twimg\.com|fxtwitter|vxtwitter|fixupx`)
	mediaTikTokKindPattern  = regexp.MustCompile(`(?i)tiktok\.com|tnktok\.com`)
	mediaInstaKindPattern   = regexp.MustCompile(`(?i)instagram\.com`)
	mediaSonKindPattern     = regexp.MustCompile(`(?i)soundcloud\.com|spotify\.com|deezer\.com`)
	mediaSteamKindPattern   = regexp.MustCompile(`(?i)steampowered\.com|steamcommunity\.com`)
	mediaDiscordCDNPattern  = regexp.MustCompile(`(?i)cdn\.discordapp\.com|media\.discordapp\.net`)
)

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
	if text := mediaURLPattern.ReplaceAllStringFunc(msg.Content, func(raw string) string {
		return "[" + mediaURLKind(raw, false) + "]"
	}); text != "" {
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
		for _, sticker := range msg.Stickers {
			if strings.TrimSpace(sticker) != "" {
				parts = append(parts, "[image]")
			}
		}
	}
	return collapseMediaWhitespace(strings.Join(parts, " "))
}

func renderMediaMessageV1(msg Message) string {
	urls := mediaURLPattern.FindAllString(msg.Content, -1)
	nonGifURLs := 0
	for _, raw := range urls {
		if mediaURLKind(raw, true) != "gif" {
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
			continue
		}
		parts = append(parts, "[sticker: "+name+"]")
	}
	return collapseMediaWhitespace(strings.Join(parts, " "))
}

func mediaURLTagV1(raw string, embeds []MessageEmbed, nonGifURLs int) string {
	kind := mediaURLKind(raw, true)
	if kind == "gif" {
		words := gifWordsFromSegment(lastURLPathSegment(raw))
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
		description = cleanMediaDescription(embed.Title)
		if description == "" {
			description = cleanMediaDescription(embed.Description)
		}
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

// mediaURLKind classifies a URL by the legacy regexes over the whole string.
// v1 adds the Discord-CDN last-segment gif-word clause.
func mediaURLKind(raw string, v1 bool) string {
	switch {
	case mediaGifKindPattern.MatchString(raw),
		v1 && mediaDiscordCDNPattern.MatchString(raw) && segmentHasGifWord(lastURLPathSegment(raw)):
		return "gif"
	case mediaYouTubeKindPattern.MatchString(raw):
		return "youtube"
	case mediaTweetKindPattern.MatchString(raw):
		return "tweet"
	case mediaTikTokKindPattern.MatchString(raw):
		return "tiktok"
	case mediaInstaKindPattern.MatchString(raw):
		return "insta"
	case mediaSonKindPattern.MatchString(raw):
		return "son"
	case mediaSteamKindPattern.MatchString(raw):
		return "steam"
	case mediaDiscordCDNPattern.MatchString(raw):
		return "image"
	default:
		return "lien"
	}
}

// mediaURLPath returns the path part after scheme://host and before the first
// ? or #, without parsing the URL strictly.
func mediaURLPath(raw string) string {
	value := raw
	if idx := strings.Index(value, "://"); idx >= 0 {
		value = value[idx+3:]
	}
	if idx := strings.IndexAny(value, "?#"); idx >= 0 {
		value = value[:idx]
	}
	slash := strings.IndexByte(value, '/')
	if slash < 0 {
		return ""
	}
	return value[slash:]
}

// lastURLPathSegment returns the last non-empty raw path segment.
func lastURLPathSegment(raw string) string {
	segments := strings.Split(mediaURLPath(raw), "/")
	for idx := len(segments) - 1; idx >= 0; idx-- {
		if segments[idx] != "" {
			return segments[idx]
		}
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

// mediaSegmentWords percent-decodes the segment, lowercases it without its
// final extension, and splits it on -, _, ., + and whitespace (gif word step 2).
func mediaSegmentWords(segment string) []string {
	trimmed := stripMediaExtension(strings.TrimSpace(unquoteMediaSegment(segment)))
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

// unquoteMediaSegment decodes valid %XX escapes as UTF-8 and keeps everything
// else, like Python's urllib.parse.unquote (bare % stays literal).
func unquoteMediaSegment(value string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	decoded := make([]byte, 0, len(value))
	for idx := 0; idx < len(value); idx++ {
		if value[idx] == '%' && idx+2 < len(value) {
			high, highOK := hexValue(value[idx+1])
			low, lowOK := hexValue(value[idx+2])
			if highOK && lowOK {
				decoded = append(decoded, high<<4|low)
				idx += 2
				continue
			}
		}
		decoded = append(decoded, value[idx])
	}
	return decodeUTF8ReplacingInvalid(decoded)
}

func hexValue(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

func decodeUTF8ReplacingInvalid(value []byte) string {
	if utf8.Valid(value) {
		return string(value)
	}
	var builder strings.Builder
	builder.Grow(len(value))
	for len(value) > 0 {
		r, size := utf8.DecodeRune(value)
		if r == utf8.RuneError && size == 1 {
			builder.WriteRune(utf8.RuneError)
			value = value[1:]
			continue
		}
		builder.WriteRune(r)
		value = value[size:]
	}
	return builder.String()
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

// attachmentContentType lowercases a content type and drops its parameters.
func attachmentContentType(attachment MessageAttachment) string {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	return contentType
}

func attachmentIsGIF(attachment MessageAttachment) bool {
	return attachmentContentType(attachment) == "image/gif" ||
		strings.HasSuffix(strings.ToLower(attachment.Filename), ".gif")
}

// attachmentMediaType returns the content type, inferred from the filename
// extension when it is empty.
func attachmentMediaType(attachment MessageAttachment) string {
	if contentType := attachmentContentType(attachment); contentType != "" {
		return contentType
	}
	extension := strings.ToLower(attachment.Filename)
	if idx := strings.LastIndexByte(extension, '.'); idx >= 0 {
		extension = extension[idx+1:]
	}
	switch extension {
	case "png", "jpg", "jpeg", "webp", "gif":
		return "image"
	case "mp4", "webm", "mov":
		return "video"
	case "mp3", "ogg", "wav", "m4a", "opus":
		return "audio"
	default:
		return ""
	}
}

func mediaAttachmentTagV1(attachment MessageAttachment) string {
	if attachmentIsGIF(attachment) {
		words := gifWordsFromSegment(attachment.Filename)
		if len(words) == 0 {
			return "[gif]"
		}
		return "[gif: " + strings.Join(words, " ") + "]"
	}

	mediaType := attachmentMediaType(attachment)
	switch {
	case strings.HasPrefix(mediaType, "image"):
		return "[image]"
	case strings.HasPrefix(mediaType, "video"):
		return "[video]"
	case strings.HasPrefix(mediaType, "audio"):
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
