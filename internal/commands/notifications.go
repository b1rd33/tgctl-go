package commands

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/client"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

const foreverMuteUntil = math.MaxInt32

type mutePlan struct {
	Mode      string
	MuteUntil int
	Until     string
	Spec      string
}

func registerNotificationCommands(root *cobra.Command, cfg CommandsConfig) {
	root.AddCommand(muteCommand(cfg))
	root.AddCommand(unmuteCommand(cfg))
}

func muteCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "mute <chat>",
		Short:        "Mute notifications from one chat",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := parseMutePlan(cmd, time.Now())
			if err != nil {
				return emitDispatchedFailure(cmd, "mute", err)
			}
			setNotificationFingerprint(cmd, "mute", args[0], plan)
			return runPeerMuteCommand(cmd, cfg, "mute", args[0], plan)
		},
	}
	cmd.Flags().Duration("for", 0, "Mute duration, for example 30m or 8h")
	cmd.Flags().String("until", "", "Mute until an RFC3339 timestamp with timezone")
	cmd.Flags().Bool("forever", false, "Mute indefinitely")
	cmd.Flags().String("idempotency-fingerprint", "", "internal canonical request fingerprint")
	_ = cmd.Flags().MarkHidden("idempotency-fingerprint")
	addPeerNotificationWriteFlags(cmd)
	return cmd
}

func unmuteCommand(cfg CommandsConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "unmute <chat>",
		Short:        "Unmute notifications from one chat",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan := mutePlan{Mode: "unmuted", Spec: "0"}
			setNotificationFingerprint(cmd, "unmute", args[0], plan)
			return runPeerMuteCommand(cmd, cfg, "unmute", args[0], plan)
		},
	}
	cmd.Flags().String("idempotency-fingerprint", "", "internal canonical request fingerprint")
	_ = cmd.Flags().MarkHidden("idempotency-fingerprint")
	addPeerNotificationWriteFlags(cmd)
	return cmd
}

func addPeerNotificationWriteFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("allow-write", false, "Required for any Telegram-side write")
	cmd.Flags().Bool("dry-run", false, "Print payload preview without contacting Telegram")
	cmd.Flags().Bool("fuzzy", false, "Allow title-based selectors for write commands")
	cmd.Flags().String("idempotency-key", "", "Per-account replay-safe key")
	AddOutputFlags(cmd)
}

func parseMutePlan(cmd *cobra.Command, now time.Time) (mutePlan, error) {
	duration, _ := cmd.Flags().GetDuration("for")
	untilValue, _ := cmd.Flags().GetString("until")
	forever, _ := cmd.Flags().GetBool("forever")
	selected := 0
	if cmd.Flags().Changed("for") {
		selected++
	}
	if cmd.Flags().Changed("until") {
		selected++
	}
	if forever {
		selected++
	}
	if selected != 1 {
		return mutePlan{}, safety.NewBadArgs("mute requires exactly one of --for, --until, or --forever")
	}
	if cmd.Flags().Changed("for") {
		if duration <= 0 {
			return mutePlan{}, safety.NewBadArgs("--for must be greater than zero")
		}
		plan, err := mutePlanForDeadline("duration", now.Add(duration))
		plan.Spec = duration.String()
		return plan, err
	}
	if cmd.Flags().Changed("until") {
		deadline, err := time.Parse(time.RFC3339, untilValue)
		if err != nil {
			return mutePlan{}, safety.NewBadArgs("--until must be an RFC3339 timestamp with timezone")
		}
		if !deadline.After(now) {
			return mutePlan{}, safety.NewBadArgs("--until must be in the future")
		}
		plan, err := mutePlanForDeadline("until", deadline)
		plan.Spec = strconv.FormatInt(deadline.Unix(), 10)
		return plan, err
	}
	return mutePlan{Mode: "forever", MuteUntil: foreverMuteUntil, Spec: "forever"}, nil
}

func setNotificationFingerprint(cmd *cobra.Command, name, selector string, plan mutePlan) {
	fingerprint := sha256.Sum256([]byte(name + "\x00" + selector + "\x00" + plan.Mode + "\x00" + plan.Spec))
	_ = cmd.Flags().Set("idempotency-fingerprint", fmt.Sprintf("%x", fingerprint))
}

func mutePlanForDeadline(mode string, deadline time.Time) (mutePlan, error) {
	unix := deadline.Unix()
	if unix <= 0 || unix >= foreverMuteUntil {
		return mutePlan{}, safety.NewBadArgs("mute deadline exceeds Telegram's supported timestamp range")
	}
	return mutePlan{Mode: mode, MuteUntil: int(unix), Until: time.Unix(unix, 0).UTC().Format(time.RFC3339)}, nil
}

func runPeerMuteCommand(cmd *cobra.Command, cfg CommandsConfig, name, selector string, plan mutePlan) error {
	paths, target, account, err := prepareResolvedPeerWriteTarget(cmd, cfg.Paths, selector)
	if err != nil {
		return emitDispatchedFailure(cmd, name, err)
	}
	muted := plan.MuteUntil != 0
	payload := map[string]any{
		"account":         account,
		"chat_id":         target.ChatID,
		"muted":           muted,
		"mode":            plan.Mode,
		"mute_until_unix": plan.MuteUntil,
	}
	if plan.Until != "" {
		payload["mute_until"] = plan.Until
	}
	committed := map[string]any{"chat_id": target.ChatID, "muted": muted, "mode": plan.Mode, "mute_until_unix": plan.MuteUntil}
	return runWriteResolvedTargetDurable(cmd, name, "account.UpdateNotifySettings", selector, cfg, paths, payload, &target, committed,
		func(ctx context.Context, c client.Client, chatID int64, chatTitle string) (map[string]any, error) {
			if err := c.SetPeerNotifySettings(ctx, client.PeerNotifySettingsReq{ChatID: chatID, MuteUntil: plan.MuteUntil}); err != nil {
				return nil, err
			}
			result := map[string]any{
				"chat":            ChatRef{ChatID: chatID, Title: chatTitle},
				"muted":           muted,
				"mode":            plan.Mode,
				"mute_until_unix": plan.MuteUntil,
				"cache_refresh":   "required",
			}
			if plan.Until != "" {
				result["mute_until"] = plan.Until
			}
			return result, nil
		})
}
