package commands

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

func registerLiveCommands(root *cobra.Command, cfg CommandsConfig) {
	cmd := &cobra.Command{
		Use:          "listen",
		Short:        "Listen for live Telegram updates",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			once, _ := cmd.Flags().GetBool("once")
			dbPath, sessionPath, auditPath := resolveWritePaths(cmd, cfg.Paths)
			code := dispatch.Run("listen", dispatch.Options{
				JSON: jsonMode(cmd), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), AuditPath: auditPath,
			}, func(ctx context.Context) (any, error) {
				if err := safety.RequireWriteAllowed(localWriteArgs(cmd)); err != nil {
					return nil, err
				}
				c, err := cfg.ClientFactory(ctx, sessionPath, dbPath)
				if err != nil {
					return nil, err
				}
				defer c.Close()
				if once {
					event, err := c.ListenOnce(ctx)
					if err != nil {
						return nil, err
					}
					_ = cacheListenEvent(dbPath, event)
					env := output.Success("listen.event", event, output.NewRequestID(), nil)
					output.Emit(env, output.EmitOptions{JSON: true, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
					return map[string]any{"events": 1}, nil
				}
				for {
					event, err := c.ListenOnce(ctx)
					if err != nil {
						return nil, err
					}
					_ = cacheListenEvent(dbPath, event)
					env := output.Success("listen.event", event, output.NewRequestID(), nil)
					output.Emit(env, output.EmitOptions{JSON: true, Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr()})
					time.Sleep(100 * time.Millisecond)
				}
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	cmd.Flags().Bool("once", false, "Exit after one update")
	addLocalWriteFlags(cmd)
	root.AddCommand(cmd)
}

func cacheListenEvent(dbPath string, event client.ListenEvent) error {
	if event.ChatID == 0 || event.MessageID == 0 {
		return nil
	}
	db, err := store.Connect(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	text := event.Text
	return store.InsertMessage(db, store.Message{
		ChatID: event.ChatID, MessageID: event.MessageID, SenderID: optEventSender(event.SenderID),
		Date: time.Now().UTC().Format(time.RFC3339), Text: &text, HasMedia: event.MediaType != "",
		MediaType: optEventString(event.MediaType),
	})
}

func optEventSender(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func optEventString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
