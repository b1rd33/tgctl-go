package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"

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
	Date       string  `json:"date"`
	IsOutgoing bool    `json:"is_outgoing"`
	Text       *string `json:"text"`
	MediaType  *string `json:"media_type"`
}

// FullMessageDTO mirrors Python `_full_message`.
type FullMessageDTO struct {
	ChatID       int64   `json:"chat_id"`
	MessageID    int64   `json:"message_id"`
	SenderID     *int64  `json:"sender_id"`
	Date         string  `json:"date"`
	Text         *string `json:"text"`
	IsOutgoing   bool    `json:"is_outgoing"`
	ReplyToMsgID *int64  `json:"reply_to_msg_id"`
	HasMedia     bool    `json:"has_media"`
	MediaType    *string `json:"media_type"`
	MediaPath    *string `json:"media_path"`
	RawJSON      any     `json:"raw_json"`
}

func toSummaryDTO(s store.MessageSummary) MessageSummaryDTO {
	return MessageSummaryDTO{
		MessageID:  s.MessageID,
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

// ShowRunner is the runner for `tg show`. Errors map to dispatch error codes.
func ShowRunner(_ context.Context, dbPath, selector string, limit int, reverse, includeDeleted bool) (any, error) {
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
		ChatID: chatID, Limit: limit, Reverse: reverse, IncludeDeleted: includeDeleted,
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
	return map[string]any{
		"chat":     ChatRef{ChatID: chatID, Title: title},
		"order":    order,
		"messages": out,
	}, nil
}

// SearchRunner mirrors Python `_search_runner`.
func SearchRunner(_ context.Context, dbPath, selector, query string, caseSensitive bool, limit int, includeDeleted bool) (any, error) {
	if query == "" {
		return nil, safety.NewBadArgs("Search query cannot be empty")
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
		ChatID: chatID, Query: query, CaseSensitive: caseSensitive, Limit: limit, IncludeDeleted: includeDeleted,
	})
	if err != nil {
		return nil, err
	}
	out := make([]MessageSummaryDTO, len(rows))
	for i, r := range rows {
		out[i] = toSummaryDTO(r)
	}
	return map[string]any{
		"chat":           ChatRef{ChatID: chatID, Title: title},
		"query":          query,
		"case_sensitive": caseSensitive,
		"limit":          limit,
		"messages":       out,
	}, nil
}

// ListMsgsRunner mirrors Python `_list_runner`.
func ListMsgsRunner(_ context.Context, dbPath, selector string, since, until string, limit int, reverse, includeDeleted bool) (any, error) {
	sinceTS, err := dateStart(since)
	if err != nil {
		return nil, err
	}
	untilTS, err := dateEnd(until)
	if err != nil {
		return nil, err
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
		ChatID: chatID, Since: sinceTS, Until: untilTS,
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
	return map[string]any{
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
	db, session, audit string
	readOnly           bool
}

func registerReadCommands(root *cobra.Command, paths AccountPathProvider) {
	root.AddCommand(showCommand(paths))
	root.AddCommand(searchCommand(paths))
	root.AddCommand(listMsgsCommand(paths))
	root.AddCommand(getMsgCommand(paths))
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
	return readPaths{db: db, session: session, audit: audit, readOnly: readOnly}, nil
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
	code := dispatch.Run(name, dispatch.Options{
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

func showCommand(paths AccountPathProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "show <chat>",
		Short:        "Show recent cached messages in a chat",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			reverse, _ := cmd.Flags().GetBool("reverse")
			includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
			selector := args[0]
			return runDispatchedRead(cmd, "show", map[string]any{
				"chat": selector, "limit": limit, "reverse": reverse, "include_deleted": includeDeleted,
			}, paths, func(ctx context.Context, p readPaths) (any, error) {
				return ShowRunner(ctx, p.db, selector, positiveLimit(limit, 20), reverse, includeDeleted)
			})
		},
	}
	cmd.Flags().Int("limit", 20, "Max messages to return")
	cmd.Flags().Bool("reverse", false, "Show oldest first")
	cmd.Flags().Bool("include-deleted", false, "Include tombstoned messages")
	AddOutputFlags(cmd)
	return cmd
}

func searchCommand(paths AccountPathProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "search <chat> <query>",
		Short:        "Search cached messages in a chat",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			cs, _ := cmd.Flags().GetBool("case-sensitive")
			includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
			return runDispatchedRead(cmd, "search", map[string]any{
				"chat": args[0], "query": args[1], "limit": limit,
				"case_sensitive": cs, "include_deleted": includeDeleted,
			}, paths, func(ctx context.Context, p readPaths) (any, error) {
				return SearchRunner(ctx, p.db, args[0], args[1], cs, limit, includeDeleted)
			})
		},
	}
	cmd.Flags().Int("limit", 50, "Max messages to return")
	cmd.Flags().Bool("case-sensitive", false, "Case-sensitive matching")
	cmd.Flags().Bool("include-deleted", false, "Include tombstoned messages")
	AddOutputFlags(cmd)
	return cmd
}

func listMsgsCommand(paths AccountPathProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "list-msgs <chat>",
		Short:        "List cached messages in a chat with optional date filters",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			reverse, _ := cmd.Flags().GetBool("reverse")
			since, _ := cmd.Flags().GetString("since")
			until, _ := cmd.Flags().GetString("until")
			includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
			return runDispatchedRead(cmd, "list-msgs", map[string]any{
				"chat": args[0], "since": since, "until": until,
				"limit": limit, "reverse": reverse, "include_deleted": includeDeleted,
			}, paths, func(ctx context.Context, p readPaths) (any, error) {
				return ListMsgsRunner(ctx, p.db, args[0], since, until, limit, reverse, includeDeleted)
			})
		},
	}
	cmd.Flags().Int("limit", 50, "Max messages to return")
	cmd.Flags().Bool("reverse", false, "Oldest first")
	cmd.Flags().String("since", "", "YYYY-MM-DD inclusive lower bound")
	cmd.Flags().String("until", "", "YYYY-MM-DD inclusive upper bound")
	cmd.Flags().Bool("include-deleted", false, "Include tombstoned messages")
	AddOutputFlags(cmd)
	return cmd
}

func getMsgCommand(paths AccountPathProvider) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "get-msg <chat> <message-id>",
		Short:        "Print one cached message in full",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			msgID, err := parsePositiveInt32Decimal(args[1], "message-id")
			if err != nil {
				return err
			}
			includeDeleted, _ := cmd.Flags().GetBool("include-deleted")
			return runDispatchedRead(cmd, "get-msg", map[string]any{
				"chat": args[0], "message_id": msgID, "include_deleted": includeDeleted,
			}, paths, func(ctx context.Context, p readPaths) (any, error) {
				return GetMsgRunner(ctx, p.db, args[0], msgID, includeDeleted)
			})
		},
	}
	cmd.Flags().Bool("include-deleted", false, "Look up tombstoned messages too")
	AddOutputFlags(cmd)
	return cmd
}
