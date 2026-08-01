package commands

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

type RootConfig struct {
	ReadOnly        bool
	LockWaitSeconds float64
	Full            bool
	Account         string
	// ExitCode is set by command runners that emit their own envelope so the
	// process exit matches the dispatch classification rather than cobra's
	// default exit-1-on-RunE-error.
	ExitCode int
}

type rootConfigKey struct{}

func NewRootCommand() *cobra.Command {
	cfg := &RootConfig{}
	cmd := &cobra.Command{
		Use:           "tg",
		Short:         "Telegram agent CLI",
		Long:          "Telegram agent CLI — read/write/listen against your own Telegram account.",
		Version:       semverVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(c *cobra.Command, _ []string) {
			fmt.Fprintln(c.ErrOrStderr(), c.Long)
		},
	}
	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.PersistentFlags().BoolVar(&cfg.ReadOnly, "read-only", false, "Reject any write to Telegram or local DB. Also via TG_READONLY=1.")
	cmd.PersistentFlags().Float64Var(&cfg.LockWaitSeconds, "lock-wait", 0, "Seconds to wait for the Telegram session lock (default 0 = fail-fast).")
	cmd.PersistentFlags().BoolVar(&cfg.Full, "full", false, "Disable column truncation in human-mode output.")
	cmd.PersistentFlags().StringVar(&cfg.Account, "account", "", "Account name (uses accounts/<NAME>/). Default selected via accounts-use or TG_ACCOUNT env.")
	cmd.SetContext(context.WithValue(context.Background(), rootConfigKey{}, cfg))
	registerVersion(cmd)
	return cmd
}

func RootConfigFrom(cmd *cobra.Command) RootConfig {
	if cfg, ok := cmd.Context().Value(rootConfigKey{}).(*RootConfig); ok && cfg != nil {
		return *cfg
	}
	return RootConfig{}
}

func registerVersion(root *cobra.Command) {
	v := &cobra.Command{
		Use:          "version",
		Short:        "Print build version",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env := output.Success("version", map[string]any{
				"version": semverVersion(),
				"commit":  shortCommit(),
			}, output.NewRequestID(), nil)
			code := output.Emit(env, output.EmitOptions{
				JSON:   jsonMode(cmd),
				Stdout: cmd.OutOrStdout(),
				Stderr: cmd.ErrOrStderr(),
			})
			if code != output.OK {
				return fmt.Errorf("version failed")
			}
			return nil
		},
	}
	AddOutputFlags(v)
	root.AddCommand(v)
}

func semverVersion() string {
	if VersionSource == "git-describe" {
		if base, _, ok := parseGitDescribe(Version); ok {
			return base
		}
	}
	return Version
}

func shortCommit() string {
	return selectShortCommit(Version, Commit, VersionSource, vcsRevision())
}

func selectShortCommit(version, explicitCommit, source, revision string) string {
	if explicitCommit != "" {
		return shortenRevision(explicitCommit)
	}
	if revision != "" {
		return shortenRevision(revision)
	}
	if source != "git-describe" {
		return ""
	}
	if _, commit, ok := parseGitDescribe(version); ok {
		return commit
	}
	if commit, ok := parseBareGitDescribe(version); ok {
		return commit
	}
	return ""
}

func shortenRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

func parseBareGitDescribe(value string) (commit string, ok bool) {
	commit = strings.TrimSuffix(value, "-dirty")
	if len(commit) < 4 || !allHex(commit) {
		return "", false
	}
	return commit, true
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}

// parseGitDescribe recognizes the long output shape produced by
// `git describe --tags --dirty --always` when the nearest tag is a
// v-prefixed semantic version. Callers must separately verify that the value
// has explicit git-describe provenance.
func parseGitDescribe(value string) (base, commit string, ok bool) {
	described := value
	if strings.HasSuffix(described, "-dirty") {
		described = strings.TrimSuffix(described, "-dirty")
	}
	commitSeparator := strings.LastIndex(described, "-g")
	if commitSeparator < 0 {
		return "", "", false
	}
	commit = described[commitSeparator+2:]
	beforeCommit := described[:commitSeparator]
	countSeparator := strings.LastIndexByte(beforeCommit, '-')
	if countSeparator < 0 {
		return "", "", false
	}
	base = beforeCommit[:countSeparator]
	count := beforeCommit[countSeparator+1:]
	if !strings.HasPrefix(base, "v") || !isSemanticVersion(base) || !canonicalPositiveDecimal(count) || len(commit) < 4 || !allHex(commit) {
		return "", "", false
	}
	return base, commit, true
}

func canonicalPositiveDecimal(value string) bool {
	return allDecimal(value) && value[0] != '0'
}

// The helpers below validate the SemVer base of marked git-describe output.
// Unmarked Version values are never validated or normalized.
func isSemanticVersion(value string) bool {
	if strings.HasPrefix(value, "v") {
		value = strings.TrimPrefix(value, "v")
	}
	if value == "" || strings.Count(value, "+") > 1 {
		return false
	}
	if beforeBuild, build, found := strings.Cut(value, "+"); found {
		if !validIdentifiers(build, false) {
			return false
		}
		value = beforeBuild
	}
	if beforePrerelease, prerelease, found := strings.Cut(value, "-"); found {
		if !validIdentifiers(prerelease, true) {
			return false
		}
		value = beforePrerelease
	}
	core := strings.Split(value, ".")
	if len(core) != 3 {
		return false
	}
	for _, part := range core {
		if !validCoreNumber(part) {
			return false
		}
	}
	return true
}

func validCoreNumber(value string) bool {
	return allDecimal(value) && (len(value) == 1 || value[0] != '0')
}

func validIdentifiers(value string, prerelease bool) bool {
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, char := range []byte(identifier) {
			if !isASCIIDigit(char) {
				numeric = false
			}
			if !isASCIIDigit(char) && !isASCIIAlpha(char) && char != '-' {
				return false
			}
		}
		if prerelease && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func allDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range []byte(value) {
		if !isASCIIDigit(char) {
			return false
		}
	}
	return true
}

func allHex(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range []byte(value) {
		if !isASCIIDigit(char) && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

func isASCIIDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func isASCIIAlpha(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func ExecuteRoot(root *cobra.Command) int {
	classifyCobraSyntaxErrors(root)
	executed, err := root.ExecuteC()
	cfg := rootConfigPtr(root)
	if cfg != nil && cfg.ExitCode != 0 {
		return cfg.ExitCode
	}
	if err != nil {
		if executed == nil {
			executed = root
		}
		code := dispatch.Run(executed.Name(), dispatch.Options{
			JSON:   jsonMode(executed),
			Stdout: root.OutOrStdout(),
			Stderr: root.ErrOrStderr(),
		}, func(context.Context) (any, error) { return nil, err })
		return code
	}
	return 0
}

// classifyCobraSyntaxErrors brings parser- and arity-level failures into the
// same typed error contract used by command validation. RunE errors retain
// their original classification and are handled by ExecuteRoot's fallback.
func classifyCobraSyntaxErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return safety.NewBadArgs("%s", err)
	})
	var wrapArgs func(*cobra.Command)
	wrapArgs = func(cmd *cobra.Command) {
		if validate := cmd.Args; validate != nil {
			cmd.Args = func(cmd *cobra.Command, args []string) error {
				err := validate(cmd, args)
				if err == nil {
					return nil
				}
				var badArgs *safety.BadArgs
				if errors.As(err, &badArgs) {
					return err
				}
				return safety.NewBadArgs("%s", err)
			}
		}
		for _, child := range cmd.Commands() {
			wrapArgs(child)
		}
	}
	wrapArgs(root)
}

// rootConfigPtr exposes the live *RootConfig so command runners can write
// their dispatch-issued exit code back into it. Distinct from RootConfigFrom,
// which returns a copy for read-only consumers.
func rootConfigPtr(cmd *cobra.Command) *RootConfig {
	if v, ok := cmd.Context().Value(rootConfigKey{}).(*RootConfig); ok {
		return v
	}
	return nil
}

func Execute() int {
	return ExecuteRoot(NewRootCommand())
}

// RegisterAll wires every command group onto root. The accounts manager is
// the source of truth for per-account paths and also receives accounts-*
// subcommand calls.
func RegisterAll(root *cobra.Command, mgr *accounts.Manager, cfg CommandsConfig) {
	registerAuth(root, mgr)
	registerReadCommands(root, mgr)
	registerWriteCommands(root, cfg)
	registerMediaCommands(root, cfg)
	registerTopicCommands(root, cfg)
	registerFolderCommands(root, cfg)
	registerDestructiveCommands(root, cfg)
	registerAdminCommands(root, cfg)
	registerLocalDBCommands(root, cfg)
	registerLiveCommands(root, cfg)
	registerAccountCommands(root, mgr)
	registerDoctor(root, mgr)
	registerLogin(root, mgr)
	registerImportTelethon(root, mgr)
	registerSendByUsername(root, mgr)
	registerBackfillEntities(root, mgr)
	installLegacyMigrationPreflight(root, mgr)
}
