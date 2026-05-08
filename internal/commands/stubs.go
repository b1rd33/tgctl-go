// Package-level stubs for command runners that depend on functionality not
// yet wired in this Go port. They register on the root command and emit a
// clear, dispatch-classified envelope so scripts get the same shape they
// would in production. The full implementations land alongside the gotd/td
// adapter and the per-domain runners they depend on.
//
// Each stub still:
//   - registers its flags so `tg <cmd> --help` works
//   - exits with NOT_AUTHED (3) so callers can trap stub usage distinctly
//   - keeps the JSON envelope shape uniform
//
// Stubbed phases:
//   11. Topics       (topic-create, topic-edit, topic-pin, topic-unpin)
//   11. Folders      (folder-create, folder-edit, folder-delete,
//                     folder-add-chat, folder-remove-chat,
//                     folders-reorder, folders-list, folder-show,
//                     chat-pinned-list)
//   12. Admin        (chat-title, chat-photo, chat-description,
//                     set-permissions, chat-invite-link, promote, demote,
//                     ban-from-chat, kick, unban-from-chat, chat-members,
//                     chats-info, topics-list, account-sessions)
//   14. Local DB ops (backfill, discover, sync-contacts)
//   15. Live         (listen)
//   plus: stats, contacts, unread, chats-info-style read commands
//          that need real client integration to populate.
//
// The contracts (flag names, envelope keys) are committed here so future
// implementation work doesn't change the CLI surface.
package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

// registerStubCommands wires every command that needs the live MTProto path
// or heavier integration (Phase 10/11/12/14/15 partial). Each stub returns a
// dispatch-classified failure with code NOT_AUTHED so callers can trap it.
func registerStubCommands(root *cobra.Command, _ CommandsConfig) {
	stubGroups := map[string][]string{
		// Phase 11
		"topics": {"topic-create", "topic-edit", "topic-pin", "topic-unpin", "topics-list"},
		"folders": {
			"folder-create", "folder-edit", "folder-delete",
			"folder-add-chat", "folder-remove-chat", "folders-reorder",
			"folders-list", "folder-show", "chat-pinned-list",
		},
		// Phase 12
		"admin": {
			"chat-title", "chat-photo", "chat-description",
			"set-permissions", "chat-invite-link",
			"promote", "demote", "ban-from-chat", "kick", "unban-from-chat",
			"chat-members", "chats-info", "account-sessions",
		},
		// Phase 14
		"localdb": {"backfill", "discover", "sync-contacts"},
		// Phase 15 partial
		"live": {"listen"},
		// Read commands that need a populated cache (live integration).
		"reads": {"stats", "contacts", "unread"},
	}
	for group, cmds := range stubGroups {
		for _, name := range cmds {
			root.AddCommand(stubCommand(name, group))
		}
	}
}

func stubCommand(name, group string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          name,
		Short:        fmt.Sprintf("[%s] not yet implemented in tgctl-go (live MTProto path pending)", group),
		SilenceUsage: true,
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			code := dispatch.Run(name, dispatch.Options{
				JSON:   jsonMode(cmd),
				Stdout: cmd.OutOrStdout(),
				Stderr: cmd.ErrOrStderr(),
			}, func(_ context.Context) (any, error) {
				return nil, safety.NewMissingCredentials(
					fmt.Sprintf(
						"command %q requires the live MTProto path; this build of "+
							"tgctl-go ships the safety, store, dispatch, and CLI "+
							"layers only. The Python reference at "+
							"https://github.com/b1rd33/tg-cli is the working "+
							"implementation today.",
						name,
					),
				)
			})
			storeExitCode(cmd, code)
			return nil
		},
	}
	AddOutputFlags(cmd)
	return cmd
}
