package commands

import (
	"context"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/spf13/cobra"
)

func registerContactManagement(root *cobra.Command, cfg CommandsConfig) {
	for _, name := range []string{"contact-add", "contact-remove"} {
		cmd := &cobra.Command{Use: name + " <user>", Short: "Add a known user to contacts", Args: cobra.ExactArgs(1)}
		if name == "contact-remove" {
			cmd.Short = "Remove a known contact without deleting the conversation"
		} else {
			cmd.Flags().String("first-name", "", "Required contact first name")
			cmd.Flags().String("last-name", "", "Contact last name")
			cmd.Flags().Bool("share-phone", false, "Explicitly allow this contact to see your phone number")
		}
		addWriteFlags(cmd)
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			first, _ := cmd.Flags().GetString("first-name")
			last, _ := cmd.Flags().GetString("last-name")
			share, _ := cmd.Flags().GetBool("share-phone")
			if name == "contact-add" {
				if err := client.ValidateContactName(first, last); err != nil {
					return emitDispatchedFailure(cmd, name, err)
				}
			}
			// Contact changes always confirm the resolved user, including phone sharing.
			op, err := prepareResolvedTypedWrite(cmd, cfg.Paths, args[0], "user_id", func(id int64) any { return id })
			if err != nil {
				return emitDispatchedFailure(cmd, name, err)
			}
			if op.target.ChatID <= 0 || args[0] == "self" || args[0] == "me" {
				return emitDispatchedFailure(cmd, name, safety.NewBadArgs("contact target must be another user"))
			}
			payload := map[string]any{"first_name": first, "last_name": last, "share_phone": share}
			method := "contacts.AddContact"
			if name == "contact-remove" {
				method = "contacts.DeleteContacts"
			}
			return runWriteResolvedTargetDurable(cmd, name, method, args[0], cfg, op.paths, payload, &op.target, map[string]any{"user_id": op.target.ChatID}, func(ctx context.Context, c client.Client, id int64, _ string) (map[string]any, error) {
				var err error
				if name == "contact-add" {
					err = c.AddContact(ctx, client.AddContactReq{UserID: id, FirstName: first, LastName: last, SharePhone: share})
				} else {
					err = c.RemoveContact(ctx, id)
				}
				if err != nil {
					return nil, err
				}
				return map[string]any{"user_id": id, "is_contact": name == "contact-add", "cache_refresh": "required", "refresh_command": "sync-contacts"}, nil
			})
		}
		root.AddCommand(cmd)
	}
}
