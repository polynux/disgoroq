package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/medialibrary"
)

const (
	dataDefaultLimit = 50
	dataMaxLimit     = 200
	dataMaxPage      = 100000

	dataAttachmentPreviewRunes = 300
	dataMessageJSONPrettyMax   = 256 * 1024
	dataMediaSearchLimit       = 20
	dataMaxPurgeDays           = 36500
)

type dataTab struct {
	ID    string
	Label string
}

var dataTabs = []dataTab{
	{ID: "summaries", Label: "Summaries"},
	{ID: "buffer", Label: "Message buffer"},
	{ID: "messages", Label: "Cached messages"},
	{ID: "attachments", Label: "Attachments"},
	{ID: "votes", Label: "Votes"},
	{ID: "events", Label: "Events"},
	{ID: "media", Label: "Media library"},
}

// dataFilters holds the raw query-string filter values echoed back into forms.
type dataFilters struct {
	GuildID   string
	ChannelID string
	UserID    string
	Processed string

	AuthorID       string
	Text           string
	IncludeDeleted string

	CacheKind string
	Provider  string
	Model     string

	BotOnly string
	Sort    string

	EventType string
	HasError  string
	From      string
	To        string

	MediaKind string
	MediaText string

	SearchGuild string
	SearchQuery string
	SearchMode  string
}

type dataPagination struct {
	Page    int
	Limit   int
	HasPrev bool
	HasNext bool
	PrevURL string
	NextURL string
}

// dataActionURLs holds fully built action URLs (path + current filters) so
// forms submit without JavaScript and stay on the current tab and filters.
type dataActionURLs struct {
	Forget      string
	Purge       string
	MediaDelete string
	MediaEdit   string
}

type dataPageData struct {
	Title     string
	ActiveNav string
	Tab       string
	Tabs      []dataTab
	Guilds    []guildSummary
	Filters   dataFilters
	Actions   dataActionURLs
	Notice    string
	Error     string

	Pagination dataPagination

	MediaDisabled bool
	EventTypes    []string

	Summaries    []summaryRowView
	Buffer       []bufferRowView
	Messages     []messageRowView
	Attachments  []attachmentRowView
	Votes        []voteRowView
	Events       []eventRowView
	Media        []mediaRowView
	SearchHits   []mediaSearchRowView
	SearchActive bool
}

type summaryRowView struct {
	ID           int64
	GuildID      string
	GuildName    string
	UserID       string
	AuthorName   string
	SummaryText  string
	MessageCount int64
	CreatedAt    string
	UpdatedAt    string
	DeleteURL    string
}

type bufferRowView struct {
	GuildID          string
	GuildName        string
	ChannelID        string
	MessageID        string
	UserID           string
	AuthorNick       string
	Content          string
	ImageDescription string
	Timestamp        string
	HasImage         bool
	Processed        bool
}

type messageRowView struct {
	MessageID           string
	ChannelID           string
	GuildID             string
	GuildName           string
	AuthorID            string
	AuthorUsername      string
	Content             string
	ReferencedMessageID string
	CreatedAt           string
	EditedAt            string
	DeletedAt           string
	PrettyJSON          string
	HasJSON             bool
	IsEdited            bool
	IsDeleted           bool
}

type attachmentRowView struct {
	ID            int64
	CacheKind     string
	AttachmentKey string
	SourceURL     string
	Filename      string
	ContentType   string
	Size          string
	Provider      string
	Model         string
	CreatedAt     string
	Preview       string
	Content       string
	Truncated     bool
	DeleteURL     string
}

type voteRowView struct {
	GuildID        string
	GuildName      string
	ChannelID      string
	AuthorID       string
	AuthorName     string
	Content        string
	ReplyToContent string
	Provider       string
	Model          string
	IsBotReply     bool
	GoodCount      int64
	BadCount       int64
	CreatedAt      string
}

type eventRowView struct {
	Timestamp  string
	EventType  string
	GuildID    string
	GuildName  string
	ChannelID  string
	MessageID  string
	UserID     string
	DurationMS int64
	Error      string
	HasError   bool
	Details    string
}

type mediaRowView struct {
	ID          int64
	GuildID     string
	GuildName   string
	URL         string
	Kind        string
	Description string
	TriggerText string
	Uses        int64
	FirstSeen   string
	LastSeen    string
	IsImage     bool
	Linkable    bool
}

type mediaSearchRowView struct {
	GuildID     string
	GuildName   string
	URL         string
	Kind        string
	Description string
	TriggerText string
	Similarity  string
	Uses        int64
}

func normalizeDataTab(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	for _, tab := range dataTabs {
		if tab.ID == raw {
			return raw
		}
	}
	return "summaries"
}

func parseDataPage(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		return 1
	}
	if value > dataMaxPage {
		return dataMaxPage
	}
	return value
}

func parseDataLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		return dataDefaultLimit
	}
	if value > dataMaxLimit {
		return dataMaxLimit
	}
	return value
}

func parseCheckbox(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "on", "true", "yes":
		return true
	default:
		return false
	}
}

func normalizeProcessed(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes":
		return "yes"
	case "no":
		return "no"
	default:
		return "all"
	}
}

func normalizeVoteSort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "good":
		return "good"
	case "bad":
		return "bad"
	default:
		return "newest"
	}
}

func normalizeSearchMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "trigger") {
		return "trigger"
	}
	return "desc"
}

func formatUnixUTC(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatByteSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func truncateRunes(value string, limit int) (string, bool) {
	if utf8.RuneCountInString(value) <= limit {
		return value, false
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…", true
}

func prettyJSON(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return "", false
	}
	if len(raw) > dataMessageJSONPrettyMax {
		return raw, true
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return raw, true
	}
	return buf.String(), true
}

func isSafeHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func parseFlexibleTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func dataURL(r *http.Request, page int) string {
	values := r.URL.Query()
	values.Set("page", strconv.Itoa(page))
	return "/data?" + values.Encode()
}

// dataActionURL returns path with the current filters as its query string.
// Building the whole URL here keeps html/template from re-encoding the query.
func dataActionURL(r *http.Request, path string) string {
	if r.URL.RawQuery == "" {
		return path
	}
	return path + "?" + r.URL.RawQuery
}

func (s *Server) guildDisplayName(guildID string) string {
	if strings.TrimSpace(guildID) == "" {
		return ""
	}
	summary := s.lookupGuildSummary(guildID)
	if summary.InCache {
		return summary.Name
	}
	return ""
}

// requireDataRepo rejects data pages and actions when the database is not wired.
func (s *Server) requireDataRepo(w http.ResponseWriter) bool {
	if s.deps.Repo == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	if !s.requireDataRepo(w) {
		return
	}
	data, err := s.buildDataPage(r, "", "")
	if err != nil {
		logger.Error("Failed to load data page",
			zap.String("tab", r.URL.Query().Get("tab")),
			zap.Error(err))
		http.Error(w, "Failed to load data page", http.StatusInternalServerError)
		return
	}
	s.renderPage(w, "data", http.StatusOK, data)
}

func (s *Server) buildDataPage(r *http.Request, notice, errMsg string) (dataPageData, error) {
	query := r.URL.Query()

	tab := normalizeDataTab(query.Get("tab"))
	page := parseDataPage(query.Get("page"))
	limit := parseDataLimit(query.Get("limit"))
	offset := (page - 1) * limit
	if offset < 0 {
		offset = 0
	}

	filters := dataFilters{
		GuildID:        strings.TrimSpace(query.Get("guild_id")),
		ChannelID:      strings.TrimSpace(query.Get("channel_id")),
		UserID:         strings.TrimSpace(query.Get("user_id")),
		Processed:      normalizeProcessed(query.Get("processed")),
		AuthorID:       strings.TrimSpace(query.Get("author_id")),
		Text:           strings.TrimSpace(query.Get("text")),
		CacheKind:      strings.TrimSpace(query.Get("cache_kind")),
		Provider:       strings.TrimSpace(query.Get("provider")),
		Model:          strings.TrimSpace(query.Get("model")),
		Sort:           normalizeVoteSort(query.Get("sort")),
		EventType:      strings.TrimSpace(query.Get("event_type")),
		From:           strings.TrimSpace(query.Get("from")),
		To:             strings.TrimSpace(query.Get("to")),
		MediaKind:      strings.TrimSpace(query.Get("media_kind")),
		MediaText:      strings.TrimSpace(query.Get("media_text")),
		SearchGuild:    strings.TrimSpace(query.Get("search_guild")),
		SearchQuery:    strings.TrimSpace(query.Get("search_query")),
		SearchMode:     normalizeSearchMode(query.Get("search_mode")),
		IncludeDeleted: "0",
		BotOnly:        "0",
		HasError:       "0",
	}
	if parseCheckbox(query.Get("include_deleted")) {
		filters.IncludeDeleted = "1"
	}
	if parseCheckbox(query.Get("bot_only")) {
		filters.BotOnly = "1"
	}
	if parseCheckbox(query.Get("has_error")) {
		filters.HasError = "1"
	}

	guilds, err := s.listGuilds(r.Context())
	if err != nil {
		// Guild list only feeds filter suggestions; do not fail the page.
		logger.Warn("Failed to list guilds for data filters", zap.Error(err))
		guilds = nil
	}

	data := dataPageData{
		Title:     "Data",
		ActiveNav: "data",
		Tab:       tab,
		Tabs:      dataTabs,
		Guilds:    guilds,
		Filters:   filters,
		Actions: dataActionURLs{
			Forget:      dataActionURL(r, "/data/summaries/forget"),
			Purge:       dataActionURL(r, "/data/attachments/purge"),
			MediaDelete: dataActionURL(r, "/data/media/delete"),
			MediaEdit:   dataActionURL(r, "/data/media/edit"),
		},
		Notice:        notice,
		Error:         errMsg,
		MediaDisabled: s.deps.Media == nil,
		Pagination: dataPagination{
			Page:  page,
			Limit: limit,
		},
	}
	if page > 1 {
		data.Pagination.HasPrev = true
		data.Pagination.PrevURL = dataURL(r, page-1)
	}

	if s.deps.Repo == nil && tab != "media" {
		return data, errors.New("database repository is not configured")
	}

	switch tab {
	case "summaries":
		rows, hasNext, listErr := s.deps.Repo.ListConversationSummaries(r.Context(), filters.GuildID, filters.UserID, limit, offset)
		if listErr != nil {
			return data, listErr
		}
		data.Pagination.HasNext = hasNext
		for _, row := range rows {
			name := row.AuthorName
			if name == "" {
				name = row.UserID
			}
			data.Summaries = append(data.Summaries, summaryRowView{
				ID:           row.ID,
				GuildID:      row.GuildID,
				GuildName:    s.guildDisplayName(row.GuildID),
				UserID:       row.UserID,
				AuthorName:   name,
				SummaryText:  row.SummaryText,
				MessageCount: row.MessageCount,
				CreatedAt:    formatUnixUTC(row.CreatedAt),
				UpdatedAt:    formatUnixUTC(row.UpdatedAt),
				DeleteURL:    dataActionURL(r, fmt.Sprintf("/data/summaries/%d/delete", row.ID)),
			})
		}

	case "buffer":
		processed := -1
		switch filters.Processed {
		case "yes":
			processed = 1
		case "no":
			processed = 0
		}
		rows, hasNext, listErr := s.deps.Repo.ListMessageBuffer(r.Context(), filters.GuildID, filters.ChannelID, filters.UserID, processed, limit, offset)
		if listErr != nil {
			return data, listErr
		}
		data.Pagination.HasNext = hasNext
		for _, row := range rows {
			data.Buffer = append(data.Buffer, bufferRowView{
				GuildID:          row.GuildID,
				GuildName:        s.guildDisplayName(row.GuildID),
				ChannelID:        row.ChannelID,
				MessageID:        row.MessageID,
				UserID:           row.UserID,
				AuthorNick:       row.AuthorNick,
				Content:          row.Content,
				ImageDescription: row.ImageDescription,
				Timestamp:        formatUnixUTC(row.Timestamp),
				HasImage:         row.HasImage,
				Processed:        row.Processed,
			})
		}

	case "messages":
		rows, hasNext, listErr := s.deps.Repo.ListDiscordMessages(
			r.Context(),
			filters.GuildID,
			filters.ChannelID,
			filters.AuthorID,
			filters.Text,
			filters.IncludeDeleted == "1",
			limit,
			offset,
		)
		if listErr != nil {
			return data, listErr
		}
		data.Pagination.HasNext = hasNext
		for _, row := range rows {
			pretty, hasJSON := prettyJSON(row.MessageJSON)
			data.Messages = append(data.Messages, messageRowView{
				MessageID:           row.MessageID,
				ChannelID:           row.ChannelID,
				GuildID:             row.GuildID,
				GuildName:           s.guildDisplayName(row.GuildID),
				AuthorID:            row.AuthorID,
				AuthorUsername:      row.AuthorUsername,
				Content:             row.Content,
				ReferencedMessageID: row.ReferencedMessageID,
				CreatedAt:           formatUnixUTC(row.CreatedAt),
				EditedAt:            formatUnixUTC(row.EditedAt),
				DeletedAt:           formatUnixUTC(row.DeletedAt),
				PrettyJSON:          pretty,
				HasJSON:             hasJSON,
				IsEdited:            row.EditedAt > 0,
				IsDeleted:           row.DeletedAt > 0,
			})
		}

	case "attachments":
		rows, hasNext, listErr := s.deps.Repo.ListAttachmentCache(r.Context(), filters.CacheKind, filters.Provider, filters.Model, limit, offset)
		if listErr != nil {
			return data, listErr
		}
		data.Pagination.HasNext = hasNext
		for _, row := range rows {
			preview, truncated := truncateRunes(row.Content, dataAttachmentPreviewRunes)
			data.Attachments = append(data.Attachments, attachmentRowView{
				ID:            row.ID,
				CacheKind:     row.CacheKind,
				AttachmentKey: row.AttachmentKey,
				SourceURL:     row.SourceURL,
				Filename:      row.Filename,
				ContentType:   row.ContentType,
				Size:          formatByteSize(row.SizeBytes),
				Provider:      row.Provider,
				Model:         row.Model,
				CreatedAt:     formatUnixUTC(row.CreatedAt),
				Preview:       preview,
				Content:       row.Content,
				Truncated:     truncated,
				DeleteURL:     dataActionURL(r, fmt.Sprintf("/data/attachments/%d/delete", row.ID)),
			})
		}

	case "votes":
		rows, hasNext, listErr := s.deps.Repo.ListMessageVotes(r.Context(), filters.GuildID, filters.BotOnly == "1", filters.Sort, limit, offset)
		if listErr != nil {
			return data, listErr
		}
		data.Pagination.HasNext = hasNext
		for _, row := range rows {
			data.Votes = append(data.Votes, voteRowView{
				GuildID:        row.GuildID,
				GuildName:      s.guildDisplayName(row.GuildID),
				ChannelID:      row.ChannelID,
				AuthorID:       row.AuthorID,
				AuthorName:     row.AuthorName,
				Content:        row.Content,
				ReplyToContent: row.ReplyToContent,
				Provider:       row.Provider,
				Model:          row.Model,
				IsBotReply:     row.IsBotReply != 0,
				GoodCount:      row.GoodCount,
				BadCount:       row.BadCount,
				CreatedAt:      formatUnixUTC(row.CreatedAt),
			})
		}

	case "events":
		eventTypes, typesErr := s.deps.Repo.ListBotEventTypes(r.Context())
		if typesErr != nil {
			return data, typesErr
		}
		data.EventTypes = eventTypes

		now := time.Now().UTC()
		from, fromOK := parseFlexibleTime(filters.From)
		if !fromOK {
			from = now.Add(-24 * time.Hour)
		}
		to, toOK := parseFlexibleTime(filters.To)
		if !toOK {
			to = now
		}
		data.Filters.From = from.UTC().Format("2006-01-02T15:04")
		data.Filters.To = to.UTC().Format("2006-01-02T15:04")

		rows, hasNext, listErr := s.deps.Repo.ListBotEvents(r.Context(), database.EventListFilter{
			From:      from.Unix(),
			To:        to.Unix(),
			EventType: filters.EventType,
			GuildID:   filters.GuildID,
			HasError:  filters.HasError == "1",
		}, limit, offset)
		if listErr != nil {
			return data, listErr
		}
		data.Pagination.HasNext = hasNext
		for _, row := range rows {
			details := ""
			if row.Details != nil {
				if encoded, marshalErr := json.MarshalIndent(row.Details, "", "  "); marshalErr == nil {
					details = string(encoded)
				}
			}
			data.Events = append(data.Events, eventRowView{
				Timestamp:  row.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"),
				EventType:  string(row.EventType),
				GuildID:    row.GuildID,
				GuildName:  s.guildDisplayName(row.GuildID),
				ChannelID:  row.ChannelID,
				MessageID:  row.MessageID,
				UserID:     row.UserID,
				DurationMS: row.DurationMS,
				Error:      row.Error,
				HasError:   row.Error != "",
				Details:    details,
			})
		}

	case "media":
		if s.deps.MediaRepo == nil {
			return data, errors.New("media library repository is not configured")
		}

		rows, hasNext, listErr := s.deps.MediaRepo.ListEntries(r.Context(), medialibrary.ListFilter{
			GuildID: filters.GuildID,
			Kind:    filters.MediaKind,
			Text:    filters.MediaText,
		}, limit, offset)
		if listErr != nil {
			return data, listErr
		}
		data.Pagination.HasNext = hasNext
		for _, row := range rows {
			data.Media = append(data.Media, mediaRowView{
				ID:          row.ID,
				GuildID:     row.GuildID,
				GuildName:   s.guildDisplayName(row.GuildID),
				URL:         row.URL,
				Kind:        row.Kind,
				Description: row.Description,
				TriggerText: row.TriggerText,
				Uses:        row.Uses,
				FirstSeen:   formatUnixUTC(row.FirstSeen),
				LastSeen:    formatUnixUTC(row.LastSeen),
				IsImage:     row.Kind == "image" || row.Kind == "gif",
				Linkable:    isSafeHTTPURL(row.URL),
			})
		}

		if filters.SearchQuery != "" || filters.SearchGuild != "" {
			data.SearchActive = true
			s.fillMediaSearch(r.Context(), &data, filters)
		}
	}

	if data.Pagination.HasNext {
		if page < dataMaxPage {
			data.Pagination.NextURL = dataURL(r, page+1)
		} else {
			data.Pagination.HasNext = false
		}
	}

	return data, nil
}

// fillMediaSearch runs the media library vector search for the owner-facing
// tuning form. Validation and service errors are shown inline, never returned.
func (s *Server) fillMediaSearch(ctx context.Context, data *dataPageData, filters dataFilters) {
	if s.deps.Media == nil {
		data.Error = "Media library is disabled; edit and search are unavailable."
		return
	}
	if filters.SearchQuery == "" {
		data.Error = "Enter a search query."
		return
	}
	if filters.SearchGuild == "" {
		data.Error = "Select a guild to search."
		return
	}

	hits, err := s.deps.Media.Search(ctx, filters.SearchGuild, filters.SearchQuery, filters.SearchMode == "trigger", dataMediaSearchLimit)
	if err != nil {
		logger.Warn("Media library admin search failed", zap.Error(err))
		data.Error = "Search failed: " + err.Error()
		return
	}
	for _, hit := range hits {
		data.SearchHits = append(data.SearchHits, mediaSearchRowView{
			GuildID:     hit.Entry.GuildID,
			GuildName:   s.guildDisplayName(hit.Entry.GuildID),
			URL:         hit.Entry.URL,
			Kind:        hit.Entry.Kind,
			Description: hit.Entry.Description,
			TriggerText: hit.Entry.TriggerText,
			Similarity:  strconv.FormatFloat(hit.Similarity, 'f', 3, 64),
			Uses:        hit.Entry.Uses,
		})
	}
}

func (s *Server) respondData(w http.ResponseWriter, r *http.Request, notice, errMsg string) {
	data, err := s.buildDataPage(r, notice, errMsg)
	if err != nil {
		logger.Error("Failed to render data action response", zap.Error(err))
		http.Error(w, "Failed to load data page", http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		s.renderFragment(w, "data_panel", http.StatusOK, data)
		return
	}
	s.renderPage(w, "data", http.StatusOK, data)
}

func (s *Server) handleDataSummaryDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireDataRepo(w) {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid summary ID", http.StatusBadRequest)
		return
	}

	affected, err := s.deps.Repo.DeleteConversationSummary(r.Context(), id)
	if err != nil {
		logger.Error("Failed to delete summary from web admin", zap.Int64("summary_id", id), zap.Error(err))
		s.respondData(w, r, "", "Failed to delete summary.")
		return
	}
	if affected == 0 {
		s.respondData(w, r, fmt.Sprintf("Summary #%d was already gone.", id), "")
		return
	}
	s.respondData(w, r, fmt.Sprintf("Deleted summary #%d.", id), "")
}

func (s *Server) handleDataSummariesForget(w http.ResponseWriter, r *http.Request) {
	if !s.requireDataRepo(w) {
		return
	}
	guildID := strings.TrimSpace(r.FormValue("guild_id"))
	userID := strings.TrimSpace(r.FormValue("user_id"))
	if guildID == "" || userID == "" {
		s.respondData(w, r, "", "Guild ID and user ID are required to forget a user.")
		return
	}

	affected, err := s.deps.Repo.DeleteConversationSummariesForUser(r.Context(), guildID, userID)
	if err != nil {
		logger.Error("Failed to delete user summaries from web admin",
			zap.String("guild_id", guildID), zap.String("user_id", userID), zap.Error(err))
		s.respondData(w, r, "", "Failed to delete user summaries.")
		return
	}
	if affected == 0 {
		s.respondData(w, r, "No summaries found for that user in this guild.", "")
		return
	}
	s.respondData(w, r, fmt.Sprintf("Deleted %d summar%s for user %s.", affected, pluralSummary(affected), userID), "")
}

func pluralSummary(count int64) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}

func (s *Server) handleDataAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireDataRepo(w) {
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid attachment cache ID", http.StatusBadRequest)
		return
	}

	affected, err := s.deps.Repo.DeleteAttachmentCacheByID(r.Context(), id)
	if err != nil {
		logger.Error("Failed to delete attachment cache row", zap.Int64("id", id), zap.Error(err))
		s.respondData(w, r, "", "Failed to delete attachment cache row.")
		return
	}
	if affected == 0 {
		s.respondData(w, r, "That attachment cache row was already gone.", "")
		return
	}
	s.respondData(w, r, "Deleted attachment cache row.", "")
}

func (s *Server) handleDataAttachmentPurge(w http.ResponseWriter, r *http.Request) {
	if !s.requireDataRepo(w) {
		return
	}
	raw := strings.TrimSpace(r.FormValue("days"))
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 || days > dataMaxPurgeDays {
		s.respondData(w, r, "", fmt.Sprintf("Enter a number of days between 1 and %d.", dataMaxPurgeDays))
		return
	}

	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	deleted, err := s.deps.Repo.DeleteAttachmentCacheOlderThan(r.Context(), cutoff)
	if err != nil {
		logger.Error("Failed to purge attachment cache", zap.Int("days", days), zap.Error(err))
		s.respondData(w, r, "", "Failed to purge attachment cache.")
		return
	}
	s.respondData(w, r, fmt.Sprintf("Purged %d attachment cache row(s) older than %d day(s).", deleted, days), "")
}

func (s *Server) handleDataMediaDelete(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.FormValue("guild_id"))
	rawURL := strings.TrimSpace(r.FormValue("url"))
	if guildID == "" || rawURL == "" {
		s.respondData(w, r, "", "Guild ID and media URL are required.")
		return
	}
	if s.deps.MediaRepo == nil {
		s.respondData(w, r, "", "Media library repository is not configured.")
		return
	}

	affected, err := s.deps.MediaRepo.DeleteEntry(r.Context(), rawURL, guildID)
	if err != nil {
		logger.Error("Failed to delete media library entry", zap.String("guild_id", guildID), zap.Error(err))
		s.respondData(w, r, "", "Failed to delete media library entry.")
		return
	}
	if affected == 0 {
		s.respondData(w, r, "That media library entry was already gone.", "")
		return
	}
	s.respondData(w, r, "Deleted media library entry.", "")
}

func (s *Server) handleDataMediaEdit(w http.ResponseWriter, r *http.Request) {
	guildID := strings.TrimSpace(r.FormValue("guild_id"))
	rawURL := strings.TrimSpace(r.FormValue("url"))
	description := r.FormValue("description")
	triggerText := r.FormValue("trigger_text")

	if guildID == "" || rawURL == "" {
		s.respondData(w, r, "", "Guild ID and media URL are required.")
		return
	}
	if s.deps.Media == nil {
		s.respondData(w, r, "", "Media library is disabled; edit is unavailable.")
		return
	}

	if err := s.deps.Media.UpdateEntryText(r.Context(), guildID, rawURL, description, triggerText); err != nil {
		logger.Warn("Media library edit failed", zap.String("guild_id", guildID), zap.Error(err))
		s.respondData(w, r, "", "Edit failed: "+err.Error())
		return
	}
	s.respondData(w, r, "Updated media description and trigger text (embeddings recomputed).", "")
}
