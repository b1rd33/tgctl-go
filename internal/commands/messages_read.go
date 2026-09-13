package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// ChatRef is the JSON-serializable shape used by every read command.
type ChatRef struct {
	ChatID int64  `json:"chat_id"`
	Title  string `json:"title"`
}

// MessageSummaryDTO is the wire shape mirroring Python `_message_summary`.
type MessageSummaryDTO struct {
	MessageID  int64   `json:"message_id"`
	GroupedID  int64   `json:"grouped_id,omitempty"`
	Date       string  `json:"date"`
	IsOutgoing bool    `json:"is_outgoing"`
	Text       *string `json:"text"`
	MediaType  *string `json:"media_type"`
}

// FullMessageDTO mirrors Python `_full_message`.
type FullMessageDTO struct {
	ChatID       int64   `json:"chat_id"`
	MessageID    int64   `json:"message_id"`
	GroupedID    int64   `json:"grouped_id,omitempty"`
	SenderID     *int64  `json:"sender_id"`
	Date         string  `json:"date"`
	Text         *string `json:"text"`
	IsOutgoing   bool    `json:"is_outgoing"`
	ReplyToMsgID *int64  `json:"reply_to_msg_id"`
	HasMedia     bool    `json:"has_media"`
	MediaType    *string `json:"media_type"`
	MediaPath    *string `json:"media_path"`
	RawJSON      any     `json:"raw_json"`
	Deleted      bool    `json:"deleted,omitempty"`
}

func toSummaryDTO(s store.MessageSummary) MessageSummaryDTO {
	return MessageSummaryDTO{
		MessageID:  s.MessageID,
		GroupedID:  s.GroupedID,
		Date:       s.Date,
		IsOutgoing: s.IsOutgoing,
		Text:       s.Text,
		MediaType:  s.MediaType,
	}
}

func toFullMessageDTO(m *store.Message) FullMessageDTO {
	dto := FullMessageDTO{
		ChatID:       m.ChatID,
		MessageID:    m.MessageID,
		GroupedID:    m.GroupedID,
		SenderID:     m.SenderID,
		Date:         m.Date,
		Text:         m.Text,
		IsOutgoing:   m.IsOutgoing,
		ReplyToMsgID: m.ReplyToMsgID,
		HasMedia:     m.HasMedia,
		MediaType:    m.MediaType,
		MediaPath:    m.MediaPath,
	}
	if m.RawJSON != nil && *m.RawJSON != "" {
		var raw any
		if err := json.Unmarshal([]byte(*m.RawJSON), &raw); err == nil {
			dto.RawJSON = raw
		}
	}
	return dto
}

func remoteSummaryDTO(m client.BackfillMessage) MessageSummaryDTO {
	var text, media *string
	if m.Text != "" {
		text = &m.Text
	}
	if m.MediaType != "" {
		media = &m.MediaType
	}
	return MessageSummaryDTO{MessageID: m.MessageID, GroupedID: m.GroupedID, Date: m.Date, IsOutgoing: m.IsOutgoing, Text: text, MediaType: media}
}

func remoteFullMessageDTO(m client.BackfillMessage) FullMessageDTO {
	var sender, reply *int64
	if m.SenderID != 0 {
		sender = &m.SenderID
	}
	if m.ReplyToMsgID != 0 {
		reply = &m.ReplyToMsgID
	}
	var text, media *string
	if m.Text != "" {
		text = &m.Text
	}
	if m.MediaType != "" {
		media = &m.MediaType
	}
	dto := FullMessageDTO{ChatID: m.ChatID, MessageID: m.MessageID, GroupedID: m.GroupedID, SenderID: sender, Date: m.Date, Text: text, IsOutgoing: m.IsOutgoing, ReplyToMsgID: reply, HasMedia: m.HasMedia, MediaType: media, Deleted: m.Deleted}
	if m.RawJSON != "" {
		_ = json.Unmarshal([]byte(m.RawJSON), &dto.RawJSON)
	}
	return dto
}

func openRemoteReadClient(ctx context.Context, cfg CommandsConfig, p readPaths) (client.Client, error) {
	if cfg.ReadOnlyClientFactory == nil {
		return nil, fmt.Errorf("read-only Telegram client factory is not configured")
	}
	return cfg.ReadOnlyClientFactory(ctx, p.session)
}

func remoteChat(ctx context.Context, c client.Client, selector string) (client.ResolvedPeer, error) {
	peer, err := c.ResolveSelector(ctx, selector)
	if err != nil {
		return client.ResolvedPeer{}, err
	}
	if peer.ChatID == 0 {
		return client.ResolvedPeer{}, safety.NewBadArgs("selector resolved without a marked chat id")
	}
	return peer, nil
}

func parseRemoteDate(value string, end bool) (int64, error) {
	if value == "" {
		return 0, nil
	}
	layout := "2006-01-02"
	parsed, err := time.ParseInLocation(layout, value, time.UTC)
	if err != nil {
		return 0, safety.NewBadArgs("invalid date %q; expected YYYY-MM-DD", value)
	}
	if end {
		parsed = parsed.Add(24*time.Hour - time.Second)
	}
	return parsed.Unix(), nil
}

func remoteRows(page client.RemotePage, offsetID int64, limit int) ([]client.BackfillMessage, int64) {
	seen := make(map[int64]struct{}, len(page.Messages))
	rows := make([]client.BackfillMessage, 0, len(page.Messages))
	for _, row := range page.Messages {
		if row.MessageID <= 0 || row.MessageID == offsetID {
			continue
		}
		if _, ok := seen[row.MessageID]; ok {
			continue
		}
		seen[row.MessageID] = struct{}{}
		rows = append(rows, row)
	}
	next := page.NextOffsetID
	if next == offsetID {
		next = 0
	}
	if len(rows) < limit {
		next = 0
	}
	return rows, next
}

func remoteCursor(raw string, expected store.RemoteCursor) (store.RemoteCursor, error) {
	got, err := store.DecodeRemoteCursor(raw, expected)
	if err != nil {
		return store.RemoteCursor{}, safety.NewBadArgs("%v", err)
	}
	return got, nil
}

func RemoteHistoryRunner(ctx context.Context, cfg CommandsConfig, p readPaths, selector string, limit int, since, until, cursor string) (any, error) {
	if limit < 1 || limit > 100 {
		return nil, safety.NewBadArgs("remote history limit must be between 1 and 100")
	}
	if since != "" {
		if _, err := parseRemoteDate(since, false); err != nil {
			return nil, err
		}
	}
	untilDate, err := parseRemoteDate(until, true)
	if err != nil {
		return nil, err
	}
	c, err := openRemoteReadClient(ctx, cfg, p)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	peer, err := remoteChat(ctx, c, selector)
	if err != nil {
		return nil, err
	}
	expected := store.RemoteCursor{Account: p.account, Operation: "history", Chat: peer.ChatID, Since: since, Until: until}
	pageCursor, err := remoteCursor(cursor, expected)
	if err != nil {
		return nil, err
	}
	page, err := c.RemoteHistory(ctx, client.RemoteHistoryReq{ChatID: peer.ChatID, OffsetID: pageCursor.OffsetID, OffsetDate: untilDate, Limit: limit})
	if err != nil {
		return nil, err
	}
	rows, nextID := remoteRows(page, pageCursor.OffsetID, limit)
	if since != "" {
		minDate := since + "T00:00:00"
		filtered := rows[:0]
		for _, row := range rows {
			if row.Date >= minDate {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	var next string
	if nextID != 0 {
		next = store.EncodeRemoteCursor(store.RemoteCursor{Account: p.account, Operation: "history", Chat: peer.ChatID, Since: since, Until: until, OffsetID: nextID})
	}
	out := make([]MessageSummaryDTO, len(rows))
	for i, row := range rows {
		out[i] = remoteSummaryDTO(row)
	}
	return map[string]any{"source": "telegram", "coverage": "one bounded server page; not a frozen snapshot", "next_cursor": next, "chat": ChatRef{ChatID: peer.ChatID, Title: peer.Title}, "limit": limit, "messages": out}, nil
}

func RemoteSearchRunner(ctx context.Context, cfg CommandsConfig, p readPaths, selector, query string, limit int, sender, media, since, until, cursor string) (any, error) {
	if query == "" {
		return nil, safety.NewBadArgs("Search query cannot be empty")
	}
	if limit < 1 || limit > 100 {
		return nil, safety.NewBadArgs("remote search limit must be between 1 and 100")
	}
	if media != "" {
		allowed := map[string]bool{"photo": true, "video": true, "photo-video": true, "document": true, "voice": true, "audio": true}
		if !allowed[media] {
			return nil, safety.NewBadArgs("unsupported --media-type %q", media)
		}
	}
	minDate, err := parseRemoteDate(since, false)
	if err != nil {
		return nil, err
	}
	maxDate, err := parseRemoteDate(until, true)
	if err != nil {
		return nil, err
	}
	c, err := openRemoteReadClient(ctx, cfg, p)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	peer, err := remoteChat(ctx, c, selector)
	if err != nil {
		return nil, err
	}
	var senderID int64
	if sender != "" {
		resolved, resolveErr := remoteChat(ctx, c, sender)
		if resolveErr != nil {
			return nil, resolveErr
		}
		senderID = resolved.ChatID
	}
	expected := store.RemoteCursor{Account: p.account, Operation: "search", Chat: peer.ChatID, Query: query, Sender: senderID, Media: media, Since: since, Until: until}
	pageCursor, err := remoteCursor(cursor, expected)
	if err != nil {
		return nil, err
	}
	page, err := c.RemoteSearch(ctx, client.RemoteSearchReq{ChatID: peer.ChatID, SenderID: senderID, Query: query, Filter: media, MinDate: minDate, MaxDate: maxDate, OffsetID: pageCursor.OffsetID, Limit: limit})
	if err != nil {
		return nil, err
	}
	rows, nextID := remoteRows(page, pageCursor.OffsetID, limit)
	var next string
	if nextID != 0 {
		next = store.EncodeRemoteCursor(store.RemoteCursor{Account: p.account, Operation: "search", Chat: peer.ChatID, Query: query, Sender: senderID, Media: media, Since: since, Until: until, OffsetID: nextID})
	}
	out := make([]MessageSummaryDTO, len(rows))
	for i, row := range rows {
		out[i] = remoteSummaryDTO(row)
	}
	return map[string]any{"source": "telegram", "coverage": "one bounded server page; not a frozen snapshot", "next_cursor": next, "chat": ChatRef{ChatID: peer.ChatID, Title: peer.Title}, "query": query, "filters": map[string]any{"sender": nullIfEmpty(sender), "media_type": nullIfEmpty(media), "since": nullIfEmpty(since), "until": nullIfEmpty(until)}, "messages": out}, nil
}

func RemoteGetRunner(ctx context.Context, cfg CommandsConfig, p readPaths, selector string, messageID int64) (any, error) {
	c, err := openRemoteReadClient(ctx, cfg, p)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	peer, err := remoteChat(ctx, c, selector)
	if err != nil {
		return nil, err
	}
	message, err := c.RemoteGetMessage(ctx, peer.ChatID, messageID)
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, resolve.NewNotFound("message %d not found on Telegram", messageID)
	}
	return map[string]any{"source": "telegram", "chat": ChatRef{ChatID: peer.ChatID, Title: peer.Title}, "message": remoteFullMessageDTO(*message)}, nil
}

// ShowRunner is the runner for `tg show`. Errors map to dispatch error codes.
func ShowRunner(_ context.Context, dbPath, selector string, limit int, reverse, includeDeleted bool, cursors ...string) (any, error) {
	cursor := ""
	if len(cursors) > 0 {
		cursor = cursors[0]
	}
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	chatID, title, err := resolve.ResolveChatDB(db, selector)
	if err != nil {
		return nil, err
	}
	rows, err := store.Show(db, store.ShowOptions{
		Cursor: cursor, ChatID: chatID, Limit: limit, Reverse: reverse, IncludeDeleted: includeDeleted,
	})
	if err != nil {
		return nil, err
	}
	out := make([]MessageSummaryDTO, len(rows))
	for i, r := range rows {
		out[i] = toSummaryDTO(r)
	}
	order := "newest_first"
	if reverse {
		order = "oldest_first"
	}
	return map[string]any{"source": "cache", "coverage": "cached history only", "next_cursor": store.MessageCursor(chatID, reverse, rows, limit),
		"chat":     ChatRef{ChatID: chatID, Title: title},
		"order":    order,
		"messages": out,
	}, nil
}

// SearchRunner mirrors Python `_search_runner`.
func SearchRunner(_ context.Context, dbPath, selector, query string, caseSensitive bool, limit int, includeDeleted bool, cursors ...string) (any, error) {
	if query == "" {
		return nil, safety.NewBadArgs("Search query cannot be empty")
	}
	cursor := ""
	if len(cursors) > 0 {
		cursor = cursors[0]
	}
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	chatID, title, err := resolve.ResolveChatDB(db, selector)
	if err != nil {
		return nil, err
	}
	limit = positiveLimit(limit, 50)
	rows, err := store.Search(db, store.SearchOptions{
		Cursor: cursor, ChatID: chatID, Query: query, CaseSensitive: caseSensitive, Limit: limit, IncludeDeleted: includeDeleted,
	})
	if err != nil {
		return nil, err
	}
	out := make([]MessageSummaryDTO, len(rows))
	for i, r := range rows {
		out[i] = toSummaryDTO(r)
	}
	return map[string]any{"source": "cache", "coverage": "cached history only", "next_cursor": store.MessageCursor(chatID, false, rows, limit),
		"chat":           ChatRef{ChatID: chatID, Title: title},
		"query":          query,
		"case_sensitive": caseSensitive,
		"limit":          limit,
		"messages":       out,
	}, nil
}

// ListMsgsRunner mirrors Python `_list_runner`.
func ListMsgsRunner(_ context.Context, dbPath, selector string, since, until string, limit int, reverse, includeDeleted bool, cursors ...string) (any, error) {
	sinceTS, err := dateStart(since)
	if err != nil {
		return nil, err
	}
	untilTS, err := dateEnd(until)
	if err != nil {
		return nil, err
	}
	cursor := ""
	if len(cursors) > 0 {
		cursor = cursors[0]
	}
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	chatID, title, err := resolve.ResolveChatDB(db, selector)
	if err != nil {
		return nil, err
	}
	limit = positiveLimit(limit, 50)
	rows, err := store.List(db, store.ListOptions{
		Cursor: cursor, ChatID: chatID, Since: sinceTS, Until: untilTS,
		Limit: limit, Reverse: reverse, IncludeDeleted: includeDeleted,
	})
	if err != nil {
		return nil, err
	}
	out := make([]MessageSummaryDTO, len(rows))
	for i, r := range rows {
		out[i] = toSummaryDTO(r)
	}
	order := "newest_first"
	if reverse {
		order = "oldest_first"
	}
	return map[string]any{"source": "cache", "coverage": "cached history only", "next_cursor": store.MessageCursor(chatID, reverse, rows, limit),
		"chat":  ChatRef{ChatID: chatID, Title: title},
		"order": order,
		"filters": map[string]any{
			"limit": limit,
			"since": nullIfEmpty(since),
			"until": nullIfEmpty(until),
		},
		"messages": out,
	}, nil
}

// GetMsgRunner mirrors Python `_get_runner`.
func GetMsgRunner(_ context.Context, dbPath, selector string, messageID int64, includeDeleted bool) (any, error) {
	db, err := store.ConnectReadonly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	chatID, title, err := resolve.ResolveChatDB(db, selector)
	if err != nil {
		return nil, err
	}
	msg, err := store.GetOne(db, chatID, messageID, includeDeleted)
	if err == sql.ErrNoRows {
		return nil, resolve.NewNotFound("message %d not cached in chat %d", messageID, chatID)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"chat":    ChatRef{ChatID: chatID, Title: title},
		"message": toFullMessageDTO(msg),
	}, nil
}

func positiveLimit(value, def int) int {
	if value < 1 {
		return def
	}
	return value
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func dateStart(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !dateRE.MatchString(value) {
		return "", safety.NewBadArgs("Invalid --since date %q; expected YYYY-MM-DD", value)
	}
	return value + "T00:00:00", nil
}

func dateEnd(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !dateRE.MatchString(value) {
		return "", safety.NewBadArgs("Invalid --until date %q; expected YYYY-MM-DD", value)
	}
	return value + "T23:59:59", nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// readPaths bundles the per-account paths each read runner needs.
type readPaths struct {
	account            string
	db, session, audit string
	readOnly           bool
}

func registerReadCommands(root *cobra.Command, paths AccountPathProvider, cfgs ...CommandsConfig) {
	var cfg CommandsConfig
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	root.AddCommand(showCommand(paths, cfg))
	root.AddCommand(searchCommand(paths, cfg))
	root.AddCommand(listMsgsCommand(paths, cfg))
	root.AddCommand(getMsgCommand(paths, cfg))
	registerExtraReadCommands(root, paths)
}

func resolvePaths(cmd *cobra.Command, paths AccountPathProvider) (readPaths, error) {
	account, err := selectedAccount(cmd, paths)
	if err != nil {
		return readPaths{}, err
	}
	readOnly := commandReadOnly(cmd)
	db, session, audit, err := accountPathsForMode(paths, account, readOnly)
	if err != nil {
		return readPaths{}, err
	}
	if readOnly {
		audit = ""
	}
	return readPaths{account: account, db: db, session: session, audit: audit, readOnly: readOnly}, nil
}

func commandReadOnly(cmd *cobra.Command) bool {
	return safety.ReadOnlyEnabled(RootConfigFrom(cmd.Root()).ReadOnly)
}

func connectReadDB(p readPaths) (*sql.DB, error) {
	if p.readOnly {
		return store.ConnectReadonly(p.db)
	}
	return store.Connect(p.db)
}

func openReadClient(ctx context.Context, cfg CommandsConfig, p readPaths) (client.Client, error) {
	if p.readOnly {
		if cfg.ReadOnlyClientFactory == nil {
			return nil, fmt.Errorf("read-only Telegram client factory is not configured")
		}
		return cfg.ReadOnlyClientFactory(ctx, p.session)
	}
	return cfg.ClientFactory(ctx, p.session, p.db)
}

func runDispatchedRead(cmd *cobra.Command, name string, args map[string]any, paths AccountPathProvider, runner func(ctx context.Context, p readPaths) (any, error)) error {
	p, err := resolvePaths(cmd, paths)
	if err != nil {
		return emitDispatchedFailure(cmd, name, err)
	}
	code := dispatch.Run(name, dispatch.Options{Context: cmd.Context(),
		JSON:      jsonMode(cmd),
		Stdout:    cmd.OutOrStdout(),
		Stderr:    cmd.ErrOrStderr(),
		AuditPath: p.audit,
		Args:      args,
	}, func(ctx context.Context) (any, error) {
		return runner(ctx, p)
	})
	storeExitCode(cmd, code)
	return nil
}

func showCommand(paths AccountPathProvider, cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "show <chat>",
		Short:        "Show recent cached messages in a chat",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			source, _ := cmd.Flags().GetString("source")
			cursor, _ := cmd.Flags().GetString("cursor")
			limit, _ := cmd.Flags().GetInt("limit")
			reverse, _ := cmd.Flags().GetBool("reverse")
			includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
			selector := args[0]
			return runDispatchedRead(cmd, "show", map[string]any{
				"chat": selector, "limit": limit, "reverse": reverse, "include_deleted": includeDeleted, "source": source,
			}, paths, func(ctx context.Context, p readPaths) (any, error) {
				if source != "cache" && source != "telegram" {
					return nil, safety.NewBadArgs("--source must be cache or telegram")
				}
				if source == "telegram" {
					if reverse {
						return nil, safety.NewBadArgs("--reverse is only supported for cache source")
					}
					return RemoteHistoryRunner(ctx, cfg, p, selector, positiveLimit(limit, 20), "", "", cursor)
				}
				return ShowRunner(ctx, p.db, selector, positiveLimit(limit, 20), reverse, includeDeleted, cursor)
			})
		},
	}
	cmd.Flags().Int("limit", 20, "Max messages to return")
	cmd.Flags().Bool("reverse", false, "Show oldest first")
	cmd.Flags().Bool("include-deleted", false, "Include tombstoned messages")
	cmd.Flags().String("source", "cache", "Read source: cache or telegram")
	cmd.Flags().String("cursor", "", "Continue from next_cursor using the same filters and order")
	AddOutputFlags(cmd)
	return cmd
}

func searchCommand(paths AccountPathProvider, cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "search <chat> <query>",
		Short:        "Search cached messages in a chat",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cursor, _ := cmd.Flags().GetString("cursor")
			limit, _ := cmd.Flags().GetInt("limit")
			cs, _ := cmd.Flags().GetBool("case-sensitive")
			source, _ := cmd.Flags().GetString("source")
			sender, _ := cmd.Flags().GetString("sender")
			media, _ := cmd.Flags().GetString("media-type")
			since, _ := cmd.Flags().GetString("since")
			until, _ := cmd.Flags().GetString("until")
			includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
			return runDispatchedRead(cmd, "search", map[string]any{
				"chat": args[0], "query": args[1], "limit": limit,
				"case_sensitive": cs, "include_deleted": includeDeleted, "source": source, "sender": sender, "media_type": media, "since": since, "until": until,
			}, paths, func(ctx context.Context, p readPaths) (any, error) {
				if source != "cache" && source != "telegram" {
					return nil, safety.NewBadArgs("--source must be cache or telegram")
				}
				if source == "telegram" {
					if cs {
						return nil, safety.NewBadArgs("--case-sensitive is only supported for cache source")
					}
					return RemoteSearchRunner(ctx, cfg, p, args[0], args[1], positiveLimit(limit, 50), sender, media, since, until, cursor)
				}
				return SearchRunner(ctx, p.db, args[0], args[1], cs, limit, includeDeleted, cursor)
			})
		},
	}
	cmd.Flags().Int("limit", 50, "Max messages to return")
	cmd.Flags().Bool("case-sensitive", false, "Case-sensitive matching")
	cmd.Flags().Bool("include-deleted", false, "Include tombstoned messages")
	cmd.Flags().String("source", "cache", "Read source: cache or telegram")
	cmd.Flags().String("sender", "", "Filter by sender selector in Telegram source")
	cmd.Flags().String("media-type", "", "Filter by media type: photo, video, photo-video, document, voice, or audio")
	cmd.Flags().String("since", "", "YYYY-MM-DD inclusive lower bound (Telegram source)")
	cmd.Flags().String("until", "", "YYYY-MM-DD inclusive upper bound (Telegram source)")
	cmd.Flags().String("cursor", "", "Continue from next_cursor using the same filters and order")
	AddOutputFlags(cmd)
	return cmd
}

func listMsgsCommand(paths AccountPathProvider, cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list-msgs <chat>",
		Short:        "List cached messages in a chat with optional date filters",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			source, _ := cmd.Flags().GetString("source")
			cursor, _ := cmd.Flags().GetString("cursor")
			limit, _ := cmd.Flags().GetInt("limit")
			reverse, _ := cmd.Flags().GetBool("reverse")
			since, _ := cmd.Flags().GetString("since")
			until, _ := cmd.Flags().GetString("until")
			includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
			return runDispatchedRead(cmd, "list-msgs", map[string]any{
				"chat": args[0], "since": since, "until": until,
				"limit": limit, "reverse": reverse, "include_deleted": includeDeleted, "source": source,
			}, paths, func(ctx context.Context, p readPaths) (any, error) {
				if source != "cache" && source != "telegram" {
					return nil, safety.NewBadArgs("--source must be cache or telegram")
				}
				if source == "telegram" {
					if reverse {
						return nil, safety.NewBadArgs("--reverse is only supported for cache source")
					}
					return RemoteHistoryRunner(ctx, cfg, p, args[0], positiveLimit(limit, 50), since, until, cursor)
				}
				return ListMsgsRunner(ctx, p.db, args[0], since, until, limit, reverse, includeDeleted, cursor)
			})
		},
	}
	cmd.Flags().Int("limit", 50, "Max messages to return")
	cmd.Flags().Bool("reverse", false, "Oldest first")
	cmd.Flags().String("since", "", "YYYY-MM-DD inclusive lower bound")
	cmd.Flags().String("until", "", "YYYY-MM-DD inclusive upper bound")
	cmd.Flags().Bool("include-deleted", false, "Include tombstoned messages")
	cmd.Flags().String("source", "cache", "Read source: cache or telegram")
	cmd.Flags().String("cursor", "", "Continue from next_cursor using the same filters and order")
	AddOutputFlags(cmd)
	return cmd
}

func getMsgCommand(paths AccountPathProvider, cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "get-msg <chat> <message-id>",
		Short:        "Print one cached message in full",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			source, _ := cmd.Flags().GetString("source")
			msgID, err := parsePositiveInt32Decimal(args[1], "message-id")
			if err != nil {
				return err
			}
			includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
			return runDispatchedRead(cmd, "get-msg", map[string]any{
				"chat": args[0], "message_id": msgID, "include_deleted": includeDeleted, "source": source,
			}, paths, func(ctx context.Context, p readPaths) (any, error) {
				if source != "cache" && source != "telegram" {
					return nil, safety.NewBadArgs("--source must be cache or telegram")
				}
				if source == "telegram" {
					return RemoteGetRunner(ctx, cfg, p, args[0], msgID)
				}
				return GetMsgRunner(ctx, p.db, args[0], msgID, includeDeleted)
			})
		},
	}
	cmd.Flags().Bool("include-deleted", false, "Look up tombstoned messages too")
	cmd.Flags().String("source", "cache", "Read source: cache or telegram")
	AddOutputFlags(cmd)
	return cmd
}
