package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/reader"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
	"github.com/b1rd33/tgctl-go/internal/writes"
	"github.com/spf13/cobra"
)

func registerReader(root *cobra.Command, cfg CommandsConfig) {
	cmd := &cobra.Command{Use: "reader <chat>", Short: "Open a temporary local browser reader for one chat", Long: "Serve bounded chat history on loopback, with sponsored messages and genuine browser interaction reporting.\nRequires explicit --account and --allow-write. Read-only mode rejects startup.\nThe ready event contains a private local URL; keep it out of logs and shared links.\nSession ownership is held only during Telegram requests. Closing the reader or its timeout stops the server.\nOrdinary channel videos open in Telegram; this reader does not provide channel-video playback.", Args: cobra.ExactArgs(1), SilenceUsage: true}
	cmd.Flags().Int("limit", 50, "Messages per page (1–100; at most 20 continuation pages)")
	cmd.Flags().Duration("duration", 15*time.Minute, "Maximum reader lifetime (1–60 minutes)")
	cmd.Flags().Bool("allow-write", false, "Allow genuine sponsored view/click/report callbacks and local audit/ledger writes")
	cmd.Flags().Bool("dry-run", false, "Validate the local reader plan without opening a listener or contacting Telegram")
	AddOutputFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		allow, _ := cmd.Flags().GetBool("allow-write")
		gate := safety.Args{AllowWrite: allow, ReadOnly: commandReadOnly(cmd)}
		if err := safety.RequireWriteAllowed(gate); err != nil {
			return emitDispatchedFailure(cmd, "reader", err)
		}
		if strings.TrimSpace(RootConfigFrom(cmd).Account) == "" {
			return emitDispatchedFailure(cmd, "reader", safety.NewBadArgs("reader requires explicit --account"))
		}
		if err := safety.RequireExplicitOrFuzzy(gate, args[0]); err != nil {
			return emitDispatchedFailure(cmd, "reader", err)
		}
		limit, _ := cmd.Flags().GetInt("limit")
		duration, _ := cmd.Flags().GetDuration("duration")
		if limit < 1 || limit > 100 || duration < time.Minute || duration > time.Hour {
			return emitDispatchedFailure(cmd, "reader", safety.NewBadArgs("invalid reader limit or duration"))
		}
		account, err := selectedAccount(cmd, cfg.Paths)
		if err != nil {
			return emitDispatchedFailure(cmd, "reader", err)
		}
		dry, _ := cmd.Flags().GetBool("dry-run")
		code := dispatch.Run("reader", dispatch.Options{Context: cmd.Context(), JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()}, func(ctx context.Context) (any, error) {
			if dry {
				return map[string]any{"dry_run": true, "limit": limit, "duration_seconds": duration.Seconds(), "host": "127.0.0.1", "genuine_ad_interactions_only": true}, nil
			}
			db, session, audit, err := accountPathsForMode(cfg.Paths, account, true)
			if err != nil {
				return nil, err
			}
			p := readPaths{account: account, db: db, session: session, audit: audit}
			ctx, stop := context.WithTimeout(ctx, duration)
			defer stop()
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				return nil, errors.New("cannot start local reader")
			}
			defer listener.Close()
			expires := time.Now().Add(duration)
			s, err := reader.New(&readerBackend{cfg: cfg, paths: p, selector: args[0], limit: limit, gate: gate}, listener.Addr().String(), expires, stop)
			if err != nil {
				return nil, err
			}
			if jsonMode(cmd) {
				env := output.Success("reader.ready", map[string]any{"url": s.URL(), "expires_at": expires.UTC().Format(time.RFC3339)}, output.NewRequestID(), nil)
				if output.Emit(env, output.EmitOptions{JSON: true, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()}) != output.OK {
					return nil, errors.New("cannot deliver reader URL")
				}
			} else {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Open in your browser:", s.URL()); err != nil {
					return nil, err
				}
			}
			if err := s.Serve(ctx, listener); err != nil {
				return nil, err
			}
			return map[string]bool{"closed": true}, nil
		})
		storeExitCode(cmd, code)
		return nil
	}
	root.AddCommand(cmd)
}

type readerBackend struct {
	cfg      CommandsConfig
	paths    readPaths
	selector string
	limit    int
	peerID   int64
	userID   int64
	gate     safety.Args
}

func (b *readerBackend) Page(ctx context.Context, offset int64, ads bool) (reader.SourcePage, error) {
	c, err := openRemoteReadClient(ctx, b.cfg, b.paths)
	if err != nil {
		return reader.SourcePage{}, err
	}
	defer c.Close()
	if err := b.checkIdentity(ctx, c, true); err != nil {
		return reader.SourcePage{}, err
	}
	peer, err := remoteChat(ctx, c, b.selector)
	if err != nil {
		return reader.SourcePage{}, err
	}
	if b.peerID != 0 && peer.ChatID != b.peerID {
		return reader.SourcePage{}, safety.NewBadArgs("reader peer changed; close this reader and resolve the target again")
	}
	info, err := c.GetChatsInfo(ctx, []int64{peer.ChatID})
	if err != nil {
		return reader.SourcePage{}, err
	}
	if len(info) != 1 || info[0].ID != peer.ChatID {
		return reader.SourcePage{}, safety.NewBadArgs("reader chat metadata unavailable")
	}
	b.peerID = peer.ChatID
	page, err := c.RemoteHistory(ctx, client.RemoteHistoryReq{ChatID: peer.ChatID, OffsetID: offset, Limit: b.limit})
	if err != nil {
		return reader.SourcePage{}, err
	}
	out := reader.SourcePage{Page: reader.Page{Title: info[0].Title, Kind: info[0].Type, NextOffset: page.NextOffsetID, Limit: b.limit, Messages: []reader.Message{}}}
	if info[0].Bot {
		out.Kind = "bot"
	}
	for i := len(page.Messages) - 1; i >= 0; i-- {
		m := page.Messages[i]
		author := "Message"
		switch {
		case out.Kind == "channel":
			author = info[0].Title
		case m.IsOutgoing:
			author = "You"
		case out.Kind == "user" || out.Kind == "bot":
			author = info[0].Title
		case m.SenderID != 0:
			author = "Sender " + strconv.FormatInt(m.SenderID, 10)
		}
		out.Messages = append(out.Messages, reader.Message{ID: m.MessageID, Author: author, Text: m.Text, Date: m.Date, Media: m.MediaType, Deleted: m.Deleted})
	}
	if ads && (out.Kind == "channel" || out.Kind == "bot") {
		api, ok := c.(client.ReaderAPI)
		if !ok {
			return reader.SourcePage{}, errors.New("reader adapter unavailable")
		}
		batch, err := api.GetSponsoredMessages(ctx, peer.ChatID)
		if err != nil {
			return reader.SourcePage{}, err
		}
		out.Batch = &batch
	}
	return out, nil
}

func (b *readerBackend) File(ctx context.Context, file client.ReaderFile) ([]byte, error) {
	c, err := openRemoteReadClient(ctx, b.cfg, b.paths)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := b.checkIdentity(ctx, c, false); err != nil {
		return nil, err
	}
	api, ok := c.(client.ReaderAPI)
	if !ok {
		return nil, errors.New("reader adapter unavailable")
	}
	return api.DownloadReaderFile(ctx, file)
}

func (b *readerBackend) Action(ctx context.Context, a reader.Action) (client.SponsoredReport, error) {
	if err := safety.RequireWriteAllowed(b.gate); err != nil {
		return client.SponsoredReport{}, err
	}
	if b.peerID == 0 || b.userID == 0 {
		return client.SponsoredReport{}, safety.NewBadArgs("reader target is not established")
	}
	methods := map[string]string{"view": "messages.ViewSponsoredMessage", "click": "messages.ClickSponsoredMessage", "report": "messages.ReportSponsoredMessage"}
	method, ok := methods[a.Kind]
	if !ok {
		return client.SponsoredReport{}, safety.NewBadArgs("invalid reader action")
	}
	var captured client.SponsoredReport
	var resultErr error
	var privateOutput bytes.Buffer
	code := dispatch.Run("reader."+a.Kind, dispatch.Options{Context: ctx, JSON: true, Stdout: &privateOutput, Stderr: io.Discard, AuditPath: b.paths.audit, DurableAudit: true}, func(ctx context.Context) (any, error) {
		db, err := store.Connect(b.paths.db)
		if err != nil {
			resultErr = err
			return nil, err
		}
		defer db.Close()
		digest := sha256.Sum256(a.RandomID)
		option := sha256.Sum256(a.Option)
		value, err := writes.Run(ctx, db, writes.PipelineInput{Cmd: "reader." + a.Kind, RawSelector: strconv.FormatInt(b.peerID, 10), ConfirmedTarget: &writes.ConfirmedTarget{ChatID: b.peerID}, Args: writes.Args{Args: b.gate, IdempotencyKey: a.Key}, DBPath: b.paths.db, AuditPath: b.paths.audit, RPCMethod: method, DurableAudit: true, PayloadPreview: map[string]any{"ad_digest": fmt.Sprintf("%x", digest), "option_digest": fmt.Sprintf("%x", option), "media": a.Media, "fullscreen": a.Fullscreen}, Run: func(ctx context.Context, _ int64, _ string) (map[string]any, error) {
			c, err := b.cfg.ClientFactory(ctx, b.paths.session, b.paths.db)
			if err != nil {
				return nil, err
			}
			if err := b.checkIdentity(ctx, c, false); err != nil {
				_ = c.Close()
				return nil, err
			}
			api, ok := c.(client.ReaderAPI)
			if !ok {
				_ = c.Close()
				return nil, errors.New("reader adapter unavailable")
			}
			switch a.Kind {
			case "view":
				err = api.ViewSponsoredMessage(ctx, a.RandomID)
			case "click":
				err = api.ClickSponsoredMessage(ctx, a.RandomID, a.Media, a.Fullscreen)
			case "report":
				captured, err = api.ReportSponsoredMessage(ctx, a.RandomID, a.Option)
			}
			closeErr := c.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, safety.NewCommittedWriteWithExtras("sponsored action committed but finalization failed", errors.New("client finalization failed"), nil)
			}
			return map[string]any{"confirmed": true, "report_state": captured.State}, nil
		}})
		if closeErr := db.Close(); err == nil && closeErr != nil {
			err = safety.NewCommittedWriteWithExtras("sponsored action finalized but database close failed", errors.New("database finalization failed"), nil)
		}
		resultErr = err
		return value, err
	})
	if code != 0 {
		if resultErr != nil {
			return client.SponsoredReport{}, resultErr
		}
		return client.SponsoredReport{}, errors.New("sponsored action finalization failed; do not retry")
	}
	if a.Kind == "report" && captured.State == "" {
		return client.SponsoredReport{}, errors.New("report was already processed; do not repeat automatically")
	}
	return captured, nil
}

func (b *readerBackend) checkIdentity(ctx context.Context, c client.Client, establish bool) error {
	me, err := c.GetMe(ctx)
	if err != nil {
		return err
	}
	if me.ID <= 0 || b.userID == 0 && !establish || b.userID != 0 && me.ID != b.userID {
		return safety.NewBadArgs("reader account identity changed; close this reader")
	}
	b.userID = me.ID
	return nil
}
