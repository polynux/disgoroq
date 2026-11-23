package ai

import (
	"regexp"
	"strings"
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

	// Unicode whitespace as Python's str \s matches it, unlike Go's ASCII \s.
	mediaSpaceClass = `\x09\x0a\x0b\x0c\x0d\x1c-\x1f\x20\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
)

var (
	mediaURLPattern = regexp.MustCompile(`(?i)(?:https?://|www\.)[^` + mediaSpaceClass + `]+`)

	// A scheme is only stripped when anchored at the start, like the reference.
	mediaSchemePattern = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)

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
	if trimMediaSpace(msg.Content) == "" {
		for _, attachment := range msg.Attachments {
			if attachmentIsGIF(attachment) {
				parts = append(parts, "[gif]")
			} else {
				parts = append(parts, "[image]")
			}
		}
		for _, sticker := range msg.Stickers {
			if trimMediaSpace(sticker) != "" {
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
	if loc := mediaSchemePattern.FindStringIndex(value); loc != nil {
		value = value[loc[1]:]
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
	trimmed := stripMediaExtension(unquoteMediaSegment(segment))
	if trimmed == "" {
		return nil
	}
	return strings.FieldsFunc(strings.ToLower(trimmed), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '+' || isPythonSpace(r)
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
		if r != utf8.RuneError || size > 1 {
			builder.WriteRune(r)
			value = value[size:]
			continue
		}
		builder.WriteRune(utf8.RuneError)
		value = value[invalidUTF8SubpartLen(value):]
	}
	return builder.String()
}

// invalidUTF8SubpartLen returns the maximal invalid UTF-8 subpart length, the
// way Python's decode(errors="replace") consumes one replacement per subpart.
func invalidUTF8SubpartLen(value []byte) int {
	lead := value[0]
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		if len(value) > 1 && isContinuationByte(value[1]) {
			return 2
		}
		return 1
	case lead >= 0xE0 && lead <= 0xEF:
		if len(value) < 2 {
			return 1
		}
		low, high := byte(0x80), byte(0xBF)
		switch lead {
		case 0xE0:
			low = 0xA0
		case 0xED:
			high = 0x9F
		}
		if value[1] < low || value[1] > high {
			return 1
		}
		if len(value) < 3 || !isContinuationByte(value[2]) {
			return 2
		}
		return 3
	case lead >= 0xF0 && lead <= 0xF4:
		if len(value) < 2 {
			return 1
		}
		low, high := byte(0x80), byte(0xBF)
		switch lead {
		case 0xF0:
			low = 0x90
		case 0xF4:
			high = 0x8F
		}
		if value[1] < low || value[1] > high {
			return 1
		}
		size := 2
		for size < 4 && len(value) > size && isContinuationByte(value[size]) {
			size++
		}
		return size
	default:
		return 1
	}
}

func isContinuationByte(b byte) bool {
	return b >= 0x80 && b <= 0xBF
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
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func containsDigit(value string) bool {
	for _, r := range value {
		if r >= '0' && r <= '9' {
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

// attachmentMediaClass classifies an attachment as image, video, audio or "";
// explicit content types must carry the slash, otherwise the extension is used.
func attachmentMediaClass(attachment MessageAttachment) string {
	if contentType := attachmentContentType(attachment); contentType != "" {
		switch {
		case strings.HasPrefix(contentType, "image/"):
			return "image"
		case strings.HasPrefix(contentType, "video/"):
			return "video"
		case strings.HasPrefix(contentType, "audio/"):
			return "audio"
		default:
			return ""
		}
	}
	extension := strings.ToLower(attachment.Filename)
	idx := strings.LastIndexByte(extension, '.')
	if idx < 0 {
		return ""
	}
	switch extension[idx+1:] {
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

	switch attachmentMediaClass(attachment) {
	case "image":
		return "[image]"
	case "video":
		return "[video]"
	case "audio":
		return "[son]"
	default:
		return "[fichier]"
	}
}

// cleanMediaDescription collapses whitespace, removes brackets, and cuts the
// description to 60 code points at its last space.
func cleanMediaDescription(value string) string {
	cleaned := strings.Join(mediaFields(value), " ")
	cleaned = strings.ReplaceAll(cleaned, "[", "")
	cleaned = strings.ReplaceAll(cleaned, "]", "")
	cleaned = trimMediaSpace(cleaned)

	runes := []rune(cleaned)
	if len(runes) <= mediaDescriptionMaxRunes {
		return cleaned
	}
	cut := string(runes[:mediaDescriptionMaxRunes])
	if idx := strings.LastIndex(cut, " "); idx >= 0 {
		cut = cut[:idx]
	}
	return trimMediaSpace(cut)
}

func collapseMediaWhitespace(value string) string {
	return strings.Join(mediaFields(value), " ")
}

func mediaFields(value string) []string {
	return strings.FieldsFunc(value, isPythonSpace)
}

func trimMediaSpace(value string) string {
	return strings.TrimFunc(value, isPythonSpace)
}

// isPythonSpace reports whether r is whitespace for Python's str \s.
func isPythonSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x1c, 0x1d, 0x1e, 0x1f,
		0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
